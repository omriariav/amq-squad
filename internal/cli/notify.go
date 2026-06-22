package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const defaultOperatorRenotifyAfter = 30 * time.Minute

type notifyExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	BaseRoot        string
	StatePath       string
	RenotifyAfter   time.Duration
	DryRun          bool
	JSON            bool
	Out             io.Writer
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
}

type notifyEnvelopeData struct {
	ProjectDir    string              `json:"project_dir"`
	BaseRoot      string              `json:"base_root,omitempty"`
	Profile       string              `json:"profile"`
	Operator      team.OperatorView   `json:"operator"`
	RenotifyAfter string              `json:"renotify_after"`
	Notifications []operatorAttention `json:"notifications"`
	Suppressed    int                 `json:"suppressed"`
	StatePath     string              `json:"state_path,omitempty"`
	OperatorGates bool                `json:"operator_gates"`
	Message       string              `json:"message,omitempty"`
}

type operatorAttention struct {
	Key         string           `json:"key"`
	Session     string           `json:"session"`
	Thread      string           `json:"thread"`
	LatestID    string           `json:"latest_id"`
	From        string           `json:"from,omitempty"`
	Subject     string           `json:"subject"`
	Kind        state.Kind       `json:"kind"`
	Reason      state.AttnReason `json:"reason"`
	Age         string           `json:"age"`
	LastEventAt time.Time        `json:"last_event_at,omitempty"`
	Inspect     string           `json:"inspect"`
	Respond     string           `json:"respond"`
}

type notifyStateFile struct {
	Schema int                          `json:"schema"`
	Items  map[string]notifyStateRecord `json:"items"`
}

type notifyStateRecord struct {
	LatestID     string    `json:"latest_id"`
	LastNotified time.Time `json:"last_notified"`
}

func runNotify(args []string) error {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "scope notifications to one AMQ workstream")
	renotifyAfter := fs.Duration("renotify-after", defaultOperatorRenotifyAfter, "re-notify unchanged operator gates after this duration (0 disables repeats)")
	dryRun := fs.Bool("dry-run", false, "print notifications without updating the de-duplication state file")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned notification envelope instead of text")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad notify - emit operator attention notifications

Usage:
  amq-squad notify [--project DIR] [--profile NAME] [--session NAME]
                   [--renotify-after 30m] [--dry-run] [--json]

Scans AMQ state for live needs-you threads addressed to the configured operator
handle, prints only new or stale-threshold notifications, and records what was
shown under .amq-squad/notify-state.json. It is an event/hook-friendly attention
primitive: it does not approve, answer, clear, or poll in a loop.

Examples:
  amq-squad notify
  amq-squad notify --project ~/Code/app --profile review
  amq-squad notify --session issue-96 --renotify-after 1h
  amq-squad notify --json | jq '.data.notifications[]'
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("notify takes no positional arguments; got %d", fs.NArg())
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	return executeNotify(notifyExecution{
		ProjectDir:    projectDir,
		Profile:       profile,
		Session:       *sessionFlag,
		RenotifyAfter: *renotifyAfter,
		DryRun:        *dryRun,
		JSON:          *jsonOut,
		Out:           os.Stdout,
	})
}

func executeNotify(n notifyExecution) error {
	out := n.Out
	if out == nil {
		out = os.Stdout
	}
	now := n.Now
	if now == nil {
		now = time.Now
	}
	profile := strings.TrimSpace(n.Profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	t, err := team.ReadProfile(n.ProjectDir, profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", profile, err)
	}
	operator := team.EffectiveOperator(t)
	if !team.SupportsOperatorGates(t) {
		data := notifyEnvelopeData{
			ProjectDir:    n.ProjectDir,
			Profile:       profile,
			Operator:      operator,
			OperatorGates: false,
			Message:       "operator gates disabled for this profile",
		}
		if n.JSON {
			return writeJSONEnvelope(out, "notify", data)
		}
		fmt.Fprintln(out, "amq-squad notify: operator gates disabled for this profile.")
		return nil
	}
	if n.RenotifyAfter < 0 {
		return usageErrorf("--renotify-after must be >= 0")
	}
	resolve := n.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	baseRoot := strings.TrimSpace(n.BaseRoot)
	if baseRoot == "" {
		baseRoot, err = resolve(n.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	if baseRoot == "" {
		return fmt.Errorf("resolve AMQ base root: empty root")
	}
	snap, err := state.BuildWithThresholds(n.ProjectDir, baseRoot, n.Probe, state.Thresholds{OperatorHandle: operator.Handle})
	if err != nil {
		return fmt.Errorf("scan AMQ base root: %w", err)
	}
	items := collectOperatorAttention(n.ProjectDir, snap, operator.Handle, strings.TrimSpace(n.Session), now())
	statePath := strings.TrimSpace(n.StatePath)
	if statePath == "" {
		statePath = defaultNotifyStatePath(n.ProjectDir)
	}
	prior, err := readNotifyState(statePath)
	if err != nil {
		return err
	}
	notifications, suppressed, next := selectNotifications(items, prior, n.RenotifyAfter, now())
	if !n.DryRun {
		if err := writeNotifyState(statePath, next); err != nil {
			return err
		}
	}
	data := notifyEnvelopeData{
		ProjectDir:    n.ProjectDir,
		BaseRoot:      snap.BaseRoot,
		Profile:       profile,
		Operator:      operator,
		RenotifyAfter: n.RenotifyAfter.String(),
		Notifications: notifications,
		Suppressed:    suppressed,
		StatePath:     statePath,
		OperatorGates: true,
	}
	if n.JSON {
		return writeJSONEnvelope(out, "notify", data)
	}
	return renderNotify(out, data)
}

func collectOperatorAttention(projectDir string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time) []operatorAttention {
	var out []operatorAttention
	for _, sess := range snap.Sessions {
		if onlySession != "" && sess.Name != onlySession {
			continue
		}
		for _, th := range sess.Coordination.NeedsYouThreads() {
			if th.Historical {
				continue
			}
			age := now.Sub(th.LastEventAt)
			if age < 0 {
				age = 0
			}
			item := operatorAttention{
				Key:         notifyKey(sess.Name, th.ID),
				Session:     sess.Name,
				Thread:      th.ID,
				LatestID:    th.LatestID,
				Subject:     th.Subject,
				Kind:        th.Kind,
				Reason:      th.AttnReason,
				Age:         roundDuration(age).String(),
				LastEventAt: th.LastEventAt,
				Inspect:     notifyInspectCommand(projectDir, sess.Name, th.ID),
				Respond:     notifyRespondCommand(operatorHandle, firstNonOperatorParticipant(th, operatorHandle), th.ID, th.AttnReason),
			}
			if len(th.Participants) > 0 {
				item.From = firstNonOperatorParticipant(th, operatorHandle)
			}
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].Reason.Rank(), out[j].Reason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.Before(out[j].LastEventAt)
		}
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		return out[i].Thread < out[j].Thread
	})
	return out
}

func selectNotifications(items []operatorAttention, prior notifyStateFile, renotifyAfter time.Duration, now time.Time) ([]operatorAttention, int, notifyStateFile) {
	next := notifyStateFile{Schema: 1, Items: map[string]notifyStateRecord{}}
	var selected []operatorAttention
	suppressed := 0
	for _, item := range items {
		rec := prior.Items[item.Key]
		notify := rec.LatestID != item.LatestID || rec.LastNotified.IsZero()
		if !notify && renotifyAfter > 0 && now.Sub(rec.LastNotified) >= renotifyAfter {
			notify = true
		}
		if notify {
			selected = append(selected, item)
			rec = notifyStateRecord{LatestID: item.LatestID, LastNotified: now}
		} else {
			suppressed++
		}
		next.Items[item.Key] = rec
	}
	return selected, suppressed, next
}

func renderNotify(out io.Writer, data notifyEnvelopeData) error {
	if len(data.Notifications) == 0 {
		if data.Suppressed > 0 {
			fmt.Fprintf(out, "amq-squad notify: no new operator attention items (%d suppressed by throttle).\n", data.Suppressed)
		} else {
			fmt.Fprintln(out, "amq-squad notify: no operator attention items.")
		}
		return nil
	}
	fmt.Fprintf(out, "amq-squad notify: %d operator attention %s for %s\n", len(data.Notifications), pluralize(len(data.Notifications), "item", "items"), data.Operator.Handle)
	for _, n := range data.Notifications {
		reason := string(n.Reason)
		if reason == "" {
			reason = "generic"
		}
		fmt.Fprintf(out, "- %s %s %s (%s, age %s)\n", n.Session, n.Thread, n.Subject, reason, n.Age)
		fmt.Fprintf(out, "  inspect: %s\n", n.Inspect)
		fmt.Fprintf(out, "  respond: %s\n", n.Respond)
	}
	if data.Suppressed > 0 {
		fmt.Fprintf(out, "%d unchanged %s suppressed by throttle.\n", data.Suppressed, pluralize(data.Suppressed, "item", "items"))
	}
	return nil
}

func defaultNotifyStatePath(projectDir string) string {
	return filepath.Join(projectDir, ".amq-squad", "notify-state.json")
}

func readNotifyState(path string) (notifyStateFile, error) {
	st := notifyStateFile{Schema: 1, Items: map[string]notifyStateRecord{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, fmt.Errorf("read notify state: %w", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("parse notify state %s: %w", path, err)
	}
	if st.Items == nil {
		st.Items = map[string]notifyStateRecord{}
	}
	return st, nil
}

func writeNotifyState(path string, st notifyStateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure notify state dir: %w", err)
	}
	st.Schema = 1
	if st.Items == nil {
		st.Items = map[string]notifyStateRecord{}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notify state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write notify state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename notify state: %w", err)
	}
	return nil
}

func notifyKey(session, thread string) string {
	return session + "\x00" + thread
}

func notifyInspectCommand(projectDir, session, thread string) string {
	return fmt.Sprintf("amq-squad thread --project %s --session %s --id %s --include-body", notifyShellQuote(projectDir), notifyShellQuote(session), notifyShellQuote(thread))
}

func notifyRespondCommand(operatorHandle, to, thread string, reason state.AttnReason) string {
	if strings.TrimSpace(to) == "" {
		to = "<agent-handle>"
	}
	subject := "ANSWER: <response>"
	if reason == state.AttnApprove {
		subject = "APPROVED: <decision>"
	}
	return fmt.Sprintf("amq send --me %s --to %s --thread %s --kind answer --subject %s",
		notifyShellQuote(operatorHandle), notifyShellQuote(to), notifyShellQuote(thread), notifyShellQuote(subject))
}

func firstNonOperatorParticipant(th state.ThreadSummary, operatorHandle string) string {
	for _, p := range th.Participants {
		if p != operatorHandle {
			return p
		}
	}
	return ""
}

func roundDuration(d time.Duration) time.Duration {
	if d < time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(time.Minute)
}

func notifyShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}
