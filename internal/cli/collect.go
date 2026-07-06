package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	collectJournalRetention     = 7 * 24 * time.Hour
	collectJournalMaxDelivered  = 200
	collectJournalFilePerm      = 0o600
	collectJournalDirectoryPerm = 0o700
)

var collectNow = time.Now

type collectJournalEntry struct {
	ID           string    `json:"id"`
	From         string    `json:"from"`
	To           []string  `json:"to,omitempty"`
	Thread       string    `json:"thread"`
	Subject      string    `json:"subject,omitempty"`
	Created      string    `json:"created"`
	Body         string    `json:"body"`
	Priority     string    `json:"priority,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Labels       []string  `json:"labels,omitempty"`
	FromProject  string    `json:"from_project,omitempty"`
	ReplyProject string    `json:"reply_project,omitempty"`
	SourcePath   string    `json:"source_path,omitempty"`
	JournaledAt  time.Time `json:"journaled_at"`
	DeliveredAt  time.Time `json:"delivered_at,omitempty"`
}

type collectJournal struct {
	Root         string
	PendingDir   string
	DeliveredDir string
}

func runCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ session/workstream name")
	meFlag := fs.String("me", "", "AMQ handle to collect for")
	timeoutFlag := fs.String("timeout", "0", "maximum time to wait for one message after an empty drain (0 = do not wait)")
	includeBody := fs.Bool("include-body", false, "include message bodies in collect output")
	projectFlag := fs.String("project", "", "project/team-home directory to resolve AMQ from (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	overrideBoundary := fs.Bool("override-boundary", false, "allow collecting another project-team member's mailbox and write an audit record")
	boundaryReason := fs.String("reason", "", "required reason when --override-boundary is set")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad collect - safely collect once, optionally wait once, then collect once

Usage:
  amq-squad collect --session S --me HANDLE [--timeout D] [--include-body] [--project DIR]
                    [--profile NAME] [--override-boundary --reason WHY]

Resolves the workstream AMQ root like 'amq-squad amq drain', then performs a
kill-safe report-collection procedure:
  1. Snapshot unread message bodies to a profile/session/recipient journal,
     then acknowledge each message.
  2. If that collection pass is empty and --timeout is greater than zero, run one bounded
     'amq watch --timeout D'.
  3. After that watch returns, run one final safe collection pass.

This command deliberately does not poll. With the default --timeout 0 it
collects once and exits. Interrupted output is replayed at least once on the
next collect rather than losing message bodies.

Examples:
  amq-squad collect --session issue-96 --me cto --include-body
  amq-squad collect --session issue-96 --me cto --timeout 60s --include-body
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*sessionFlag) == "" {
		return usageErrorf("collect requires --session")
	}
	if strings.TrimSpace(*meFlag) == "" {
		return usageErrorf("collect requires --me")
	}
	timeout, err := time.ParseDuration(*timeoutFlag)
	if err != nil {
		return usageErrorf("invalid --timeout %q: %v", *timeoutFlag, err)
	}
	if timeout < 0 {
		return usageErrorf("--timeout must be non-negative")
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	ctx, err := resolveAMQContextForNamespace(projectDir, profile, *sessionFlag, *meFlag)
	if err != nil {
		return err
	}
	ctx = inferAMQContextProfileFromRoot(ctx, flagWasSet(fs, "profile"))
	if err := guardAMQMailboxConsume("collect", ctx, amqPassthroughOptions{
		OverrideBoundary: *overrideBoundary,
		BoundaryReason:   *boundaryReason,
	}); err != nil {
		return err
	}
	return executeCollect(os.Stdout, ctx, timeout, *includeBody)
}

func executeCollect(out io.Writer, ctx amqContext, timeout time.Duration, includeBody bool) error {
	if out == nil {
		out = os.Stdout
	}
	nonEmpty, err := executeCollectDrain(out, ctx, includeBody)
	if err != nil {
		return err
	}
	if nonEmpty || timeout <= 0 {
		return nil
	}
	if err := runCollectWatch(ctx, timeout); err != nil {
		return err
	}
	_, err = executeCollectDrain(out, ctx, includeBody)
	return err
}

func executeCollectDrain(out io.Writer, ctx amqContext, includeBody bool) (bool, error) {
	journal := newCollectJournal(ctx)
	unlock, err := lockCollectJournal(journal.Root)
	if err != nil {
		return false, err
	}
	defer unlock()

	if err := journal.ensure(); err != nil {
		return false, err
	}
	now := collectNow()
	if err := journal.cleanupDelivered(now); err != nil {
		return false, err
	}
	if err := journal.snapshotUnread(ctx, now); err != nil {
		return false, err
	}
	entries, err := journal.pendingEntries()
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	for _, entry := range entries {
		if err := acknowledgeCollectEntry(ctx, entry); err != nil {
			return true, err
		}
	}
	output := renderCollectOutput(ctx.Me, entries, includeBody)
	if len(bytes.TrimSpace(output)) == 0 {
		return false, nil
	}
	if _, err := out.Write(output); err != nil {
		return true, err
	}
	if err := journal.markDelivered(entries, collectNow()); err != nil {
		return true, err
	}
	return true, nil
}

func acknowledgeCollectEntry(ctx amqContext, entry collectJournalEntry) error {
	cmd := []string{"read", "--root", ctx.Root, "--me", ctx.Me, "--id", entry.ID}
	out, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: cmd})
	if err != nil {
		if isCollectAckRace(err, entry.ID) {
			return nil
		}
		return fmt.Errorf("collect ack %s: %w", entry.ID, err)
	}
	_ = out
	return nil
}

func runCollectWatch(ctx amqContext, timeout time.Duration) error {
	cmd := []string{"watch", "--root", ctx.Root, "--me", ctx.Me, "--timeout", timeout.String()}
	if _, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: cmd}); err != nil {
		if isCollectWatchTimeout(err) {
			return nil
		}
		return fmt.Errorf("collect watch: %w", err)
	}
	return nil
}

func isCollectWatchTimeout(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 4 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no new messages") && strings.Contains(msg, "timeout")
}

func isCollectAckRace(err error, id string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	if !strings.Contains(msg, id) {
		return false
	}
	return strings.Contains(msg, "message not found") ||
		strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "not in new") ||
		strings.Contains(msg, "not found")
}

func newCollectJournal(ctx amqContext) collectJournal {
	profile := sanitizeWorkstreamName(squadnamespace.NormalizeProfile(ctx.Profile))
	session := strings.TrimSpace(ctx.Env.SessionName)
	if session == "" {
		session = strings.TrimSpace(ctx.Root)
	}
	session = sanitizeWorkstreamName(session)
	me := sanitizeWorkstreamName(ctx.Me)
	root := filepath.Join(ctx.ProjectDir, team.DirName, "collect-journal", profile, session, me)
	return collectJournal{
		Root:         root,
		PendingDir:   filepath.Join(root, "pending"),
		DeliveredDir: filepath.Join(root, "delivered"),
	}
}

func (j collectJournal) ensure() error {
	for _, dir := range []string{j.PendingDir, j.DeliveredDir} {
		if err := os.MkdirAll(dir, collectJournalDirectoryPerm); err != nil {
			return fmt.Errorf("ensure collect journal %s: %w", dir, err)
		}
	}
	if err := syncCollectDir(j.Root); err != nil {
		return fmt.Errorf("sync collect journal root: %w", err)
	}
	return nil
}

func (j collectJournal) snapshotUnread(ctx amqContext, now time.Time) error {
	delivered, err := j.deliveredIDs()
	if err != nil {
		return err
	}
	pending, err := j.pendingIDs()
	if err != nil {
		return err
	}
	msgs, warns := state.ScanSessionMessages(ctx.Root, collectNow)
	for _, warn := range warns {
		_, _ = fmt.Fprintf(os.Stderr, "warning: collect skipped mailbox item %s: %s\n", warn.Path, warn.Reason)
	}
	if len(msgs) == 0 {
		return nil
	}
	for _, msg := range msgs {
		if msg.Owner != ctx.Me || msg.State != state.MailboxNew {
			continue
		}
		if delivered[msg.ID] || pending[msg.ID] {
			continue
		}
		entry := collectEntryFromMessage(msg, now)
		if err := j.writePending(entry); err != nil {
			return err
		}
		pending[msg.ID] = true
	}
	return nil
}

func collectEntryFromMessage(msg state.Message, now time.Time) collectJournalEntry {
	thread := msg.RawThread
	if strings.TrimSpace(thread) == "" {
		thread = msg.Thread
	}
	return collectJournalEntry{
		ID:           msg.ID,
		From:         msg.From,
		To:           msg.To,
		Thread:       thread,
		Subject:      msg.Subject,
		Created:      msg.Created.UTC().Format(time.RFC3339Nano),
		Body:         msg.Body,
		Priority:     string(msg.Priority),
		Kind:         string(msg.Kind),
		Labels:       msg.Labels,
		FromProject:  msg.FromProject,
		ReplyProject: msg.ReplyProject,
		SourcePath:   msg.Path,
		JournaledAt:  now.UTC(),
	}
}

func (j collectJournal) pendingEntries() ([]collectJournalEntry, error) {
	delivered, err := j.deliveredIDs()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(j.PendingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read collect pending journal: %w", err)
	}
	var out []collectJournalEntry
	for _, file := range entries {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(j.PendingDir, file.Name())
		entry, err := readCollectJournalEntry(path)
		if err != nil {
			return nil, err
		}
		if delivered[entry.ID] {
			_ = os.Remove(path)
			continue
		}
		out = append(out, entry)
	}
	sortCollectEntries(out)
	return out, nil
}

func (j collectJournal) pendingIDs() (map[string]bool, error) {
	entries, err := j.pendingEntries()
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(entries))
	for _, entry := range entries {
		ids[entry.ID] = true
	}
	return ids, nil
}

func (j collectJournal) deliveredIDs() (map[string]bool, error) {
	entries, err := os.ReadDir(j.DeliveredDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("read collect delivered journal: %w", err)
	}
	ids := make(map[string]bool, len(entries))
	for _, file := range entries {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		entry, err := readCollectJournalEntry(filepath.Join(j.DeliveredDir, file.Name()))
		if err != nil {
			return nil, err
		}
		ids[entry.ID] = true
	}
	return ids, nil
}

func (j collectJournal) writePending(entry collectJournalEntry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return fmt.Errorf("collect journal entry missing id")
	}
	path := j.pendingPath(entry.ID)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat collect pending entry: %w", err)
	}
	if _, err := os.Stat(j.deliveredPath(entry.ID)); err == nil {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat collect delivered entry: %w", err)
	}
	return writeCollectJournalEntryAtomic(path, entry)
}

func (j collectJournal) markDelivered(entries []collectJournalEntry, deliveredAt time.Time) error {
	for _, entry := range entries {
		entry.DeliveredAt = deliveredAt.UTC()
		if err := writeCollectJournalEntryAtomic(j.deliveredPath(entry.ID), entry); err != nil {
			return err
		}
		pendingPath := j.pendingPath(entry.ID)
		if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove collect pending entry %s: %w", pendingPath, err)
		}
	}
	if err := syncCollectDir(j.PendingDir); err != nil {
		return fmt.Errorf("sync collect pending dir: %w", err)
	}
	return syncCollectDir(j.DeliveredDir)
}

func (j collectJournal) cleanupDelivered(now time.Time) error {
	entries, err := os.ReadDir(j.DeliveredDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read collect delivered journal: %w", err)
	}
	type deliveredFile struct {
		Path string
		At   time.Time
	}
	var files []deliveredFile
	for _, file := range entries {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(j.DeliveredDir, file.Name())
		entry, err := readCollectJournalEntry(path)
		if err != nil {
			return err
		}
		at := entry.DeliveredAt
		if at.IsZero() {
			info, statErr := file.Info()
			if statErr != nil {
				return fmt.Errorf("stat collect delivered entry %s: %w", path, statErr)
			}
			at = info.ModTime()
		}
		files = append(files, deliveredFile{Path: path, At: at})
	}
	var kept []deliveredFile
	for _, file := range files {
		if !file.At.IsZero() && now.Sub(file.At) > collectJournalRetention {
			if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove old collect delivered entry %s: %w", file.Path, err)
			}
			continue
		}
		kept = append(kept, file)
	}
	sort.SliceStable(kept, func(i, k int) bool {
		if !kept[i].At.Equal(kept[k].At) {
			return kept[i].At.Before(kept[k].At)
		}
		return kept[i].Path < kept[k].Path
	})
	for len(kept) > collectJournalMaxDelivered {
		file := kept[0]
		kept = kept[1:]
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove excess collect delivered entry %s: %w", file.Path, err)
		}
	}
	return syncCollectDir(j.DeliveredDir)
}

func (j collectJournal) pendingPath(id string) string {
	return filepath.Join(j.PendingDir, collectJournalFilename(id))
}

func (j collectJournal) deliveredPath(id string) string {
	return filepath.Join(j.DeliveredDir, collectJournalFilename(id))
}

func collectJournalFilename(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:]) + ".json"
}

func readCollectJournalEntry(path string) (collectJournalEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return collectJournalEntry{}, fmt.Errorf("read collect journal entry %s: %w", path, err)
	}
	var entry collectJournalEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return collectJournalEntry{}, fmt.Errorf("decode collect journal entry %s: %w", path, err)
	}
	if strings.TrimSpace(entry.ID) == "" {
		return collectJournalEntry{}, fmt.Errorf("decode collect journal entry %s: missing id", path)
	}
	return entry, nil
}

func writeCollectJournalEntryAtomic(path string, entry collectJournalEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), collectJournalDirectoryPerm); err != nil {
		return fmt.Errorf("ensure collect journal dir: %w", err)
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal collect journal entry: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-collect-*.json")
	if err != nil {
		return fmt.Errorf("create collect journal temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(collectJournalFilePerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod collect journal temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write collect journal temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync collect journal temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close collect journal temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit collect journal entry: %w", err)
	}
	return syncCollectDir(filepath.Dir(path))
}

func syncCollectDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return f.Sync()
}

func sortCollectEntries(entries []collectJournalEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a := collectEntryTime(entries[i])
		b := collectEntryTime(entries[j])
		if !a.Equal(b) {
			return a.Before(b)
		}
		return entries[i].ID < entries[j].ID
	})
}

func collectEntryTime(entry collectJournalEntry) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Created)); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(entry.Created)); err == nil {
		return t
	}
	if !entry.JournaledAt.IsZero() {
		return entry.JournaledAt
	}
	return time.Time{}
}

func renderCollectOutput(me string, entries []collectJournalEntry, includeBody bool) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "[AMQ] %d new message(s) for %s:\n\n", len(entries), me)
	for _, entry := range entries {
		subject := entry.Subject
		if subject == "" {
			subject = "(no subject)"
		}
		priority := entry.Priority
		if priority == "" {
			priority = "-"
		}
		kind := entry.Kind
		if kind == "" {
			kind = "-"
		}
		fromDisplay := entry.From
		if entry.FromProject != "" {
			fromDisplay = entry.From + " (project: " + entry.FromProject + ")"
		}
		fmt.Fprintf(&out, "- From: %s\n  Thread: %s\n  ID: %s\n  Subject: %s\n  Priority: %s\n  Kind: %s\n  Created: %s\n",
			fromDisplay, entry.Thread, entry.ID, subject, priority, kind, entry.Created)
		if includeBody && entry.Body != "" {
			fmt.Fprintf(&out, "  Body:\n%s\n", entry.Body)
		}
		out.WriteString("---\n")
	}
	return out.Bytes()
}
