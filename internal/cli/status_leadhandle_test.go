package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// 3.3.1 soak, bound (i): status's lead_handle names the squad's lead as
// squad.yaml names it (its lead: handle), the way v3's other readers name
// the lead (remote attach's team.ReadFor, the lead's own start), not only a
// seat whose role id is "lead": v31's lead is cto with role cto. The rows
// drive the real producer: a real squad.yaml and the real status command
// (`amq-squad status --json`, the collector), never a hand-built document.

// leadHandleSquad writes a repository with squad.yaml (session s, lead
// lead, the seats given as handle:role) and the session root's mailboxes.
func leadHandleSquad(t *testing.T, lead string, seats ...string) (project, root string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(dir, "proj")
	root = filepath.Join(project, ".agent-mail", "s")
	var b strings.Builder
	b.WriteString("schema: 1\nsession: s\n")
	if lead != "" {
		b.WriteString("lead: " + lead + "\n")
	}
	b.WriteString("seats:\n")
	for _, s := range seats {
		h, role, _ := strings.Cut(s, ":")
		runtime := "codex"
		if h == lead {
			runtime = "pi"
		}
		b.WriteString("  - handle: " + h + "\n    role: " + role + "\n    runtime: " + runtime + "\n")
		if err := os.MkdirAll(filepath.Join(root, "agents", h, "inbox", "new"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(project, "squad.yaml"), b.String())
	return project, root
}

// statusLeadHandle is `amq-squad status --root R --project P --json`'s
// lead_handle ("" when null).
func statusLeadHandle(t *testing.T, project, root string) string {
	t.Helper()
	out, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--root", root, "--project", project, "--json"}, "test")
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	var doc struct {
		LeadHandle *string `json:"lead_handle"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.LeadHandle == nil {
		return ""
	}
	return *doc.LeadHandle
}

// The failing row first: v31's shape, a lead whose role is cto, shows as
// the lead.
func TestStatusLeadHandleIsSquadYAMLsLead(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "dev:senior-dev")
	if got := statusLeadHandle(t, project, root); got != "cto" {
		t.Errorf("lead_handle = %q, want cto (squad.yaml's lead, role cto)", got)
	}
}

// squad.yaml's lead wins over a seat whose role id is "lead": the document
// names the squad's lead, not any lead-roled seat.
func TestStatusLeadHandlePrefersSquadYAMLsLeadToALeadRole(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	if got := statusLeadHandle(t, project, root); got != "cto" {
		t.Errorf("lead_handle = %q, want cto (squad.yaml's lead)", got)
	}
}

// Where squad.yaml names no lead for this session (another session's
// squad.yaml, or none), the seat whose role is lead still fills it, as
// before.
func TestStatusLeadHandleFallsBackToTheLeadRole(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(mustRead(t, filepath.Join(project, "squad.yaml")), "session: s\n", "session: other\n", 1))
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("another session's squad.yaml: lead_handle = %q, want helper (the lead-roled seat)", got)
	}
	if err := os.Remove(filepath.Join(project, "squad.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("no squad.yaml: lead_handle = %q, want helper (the lead-roled seat)", got)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Codex pass 1: the squad.yaml read is the root's own squad's, wherever
// status runs: a different lead handle (arch) is named; status run from a
// subdirectory with --root alone names it; a repository elsewhere whose
// squad.yaml names the same session (run from there) does not; a named
// profile's root (<repo>/.agent-mail/<profile>/<session>) does not inherit
// the default squad's lead.
func TestStatusLeadHandleIsTheRootsOwnSquads(t *testing.T) {
	project, root := leadHandleSquad(t, "arch", "arch:architect", "dev:senior-dev")
	sub := filepath.Join(project, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, sub)
	statusOf := func(args ...string) string {
		t.Helper()
		out, _, err := captureOutput(t, func() error { return runStatusWithVersion(append(args, "--json"), "test") })
		if err != nil {
			t.Fatalf("status %v: %v\n%s", args, err, out)
		}
		var doc struct {
			LeadHandle *string `json:"lead_handle"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if doc.LeadHandle == nil {
			return ""
		}
		return *doc.LeadHandle
	}
	if got := statusOf("--root", root); got != "arch" {
		t.Errorf("--root from a subdirectory: lead_handle = %q, want arch", got)
	}
	other, _ := leadHandleSquad(t, "boss", "boss:cto")
	chdir(t, other)
	if got := statusOf("--root", root); got != "arch" {
		t.Errorf("--root from another repository (same session name): lead_handle = %q, want arch (the root's own squad)", got)
	}
	if got := statusOf("--root", root, "--project", other); got != "arch" {
		t.Errorf("--root with another --project: lead_handle = %q, want arch", got)
	}
	profileRoot := filepath.Join(project, ".agent-mail", "review", "s")
	if err := os.MkdirAll(filepath.Join(profileRoot, "agents", "arch", "inbox", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := statusOf("--root", profileRoot); got != "" {
		t.Errorf("a named profile's root: lead_handle = %q, want none (not the default squad's lead)", got)
	}
}

// Codex pass 2 (test strength): a squad.yaml of this session that names no
// lead leaves the lead-roled seat's fallback, and one larger than the read
// bound is not read (the fallback again).
func TestStatusLeadHandleWithoutALeadOrPastTheBound(t *testing.T) {
	project, root := leadHandleSquad(t, "", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("a squad.yaml with no lead: lead_handle = %q, want helper (the lead-roled seat)", got)
	}
	big := "schema: 1\nsession: s\nlead: cto\n" + strings.Repeat("# padding\n", (1<<20)/10+16)
	mustWrite(t, filepath.Join(project, "squad.yaml"), big)
	// refused at the stat, before any read (Codex r2 pass 2: the seam runs
	// between the stat and the read)
	prev := readSmallRegularAfterStat
	t.Cleanup(func() { readSmallRegularAfterStat = prev })
	read := false
	readSmallRegularAfterStat = func(p string) {
		if p == filepath.Join(project, "squad.yaml") {
			read = true
		}
	}
	if got := squadLeadFor(root); got != "" {
		t.Errorf("a squad.yaml past the 1 MiB bound was read: %q", got)
	}
	if read {
		t.Error("a squad.yaml past the bound at its stat reached the read")
	}
	readSmallRegularAfterStat = prev
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("a squad.yaml past the bound: lead_handle = %q, want helper", got)
	}
}

// statusWithin runs the real `amq-squad status --root R --json` and returns
// its lead_handle, failing the row when status does not return within
// limit (a blocking read in the status path).
func statusWithin(t *testing.T, root string, limit time.Duration) (string, []string) {
	t.Helper()
	return statusLeadAndSeats(t, statusDocWithin(t, root, limit))
}

// statusLeadAndSeats is a status document's lead_handle ("" when null)
// and its seats' handles.
func statusLeadAndSeats(t *testing.T, raw string) (string, []string) {
	t.Helper()
	var doc struct {
		LeadHandle *string `json:"lead_handle"`
		Seats      []struct {
			Handle string `json:"handle"`
		} `json:"seats"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	var seats []string
	for _, s := range doc.Seats {
		seats = append(seats, s.Handle)
	}
	if doc.LeadHandle == nil {
		return "", seats
	}
	return *doc.LeadHandle, seats
}

// statusDocWithoutLead is a status document with lead_handle removed, its
// keys in a canonical order: what the owner grant may not change (the
// reviewer's ruling (b): nothing from the grant appears in status output;
// only lead_handle can change). The callers fix status's clock
// (statusNow), so no field needs normalizing: generated_at and every age
// status derives from it are equal across the runs compared.
func statusDocWithoutLead(t *testing.T, raw string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	if _, ok := doc["lead_handle"]; !ok {
		t.Fatalf("the status document has no lead_handle:\n%s", raw)
	}
	delete(doc, "lead_handle")
	b, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fixStatusClock pins status's clock for the rows that compare documents.
func fixStatusClock(t *testing.T) {
	t.Helper()
	prev := statusNow
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	statusNow = func() time.Time { return at }
	t.Cleanup(func() { statusNow = prev })
}

// statusDocWithin is the real `amq-squad status --root R --json` document,
// failing the row when status exits non-zero or does not return within
// limit (a blocking read in the status path).
func statusDocWithin(t *testing.T, root string, limit time.Duration) string {
	t.Helper()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--root", root, "--json"}, "test") })
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("status exits non-zero: %v\n%s", r.err, r.out)
		}
		return r.out
	case <-time.After(limit):
		t.Fatalf("status did not return within %s (a blocking read in the status path)", limit)
	}
	return ""
}

// #1045 round 2, finding 1: a symlinked session root whose target is
// another repository's session is not the root's own squad: status
// collects the target's seats, exits 0, and lead_handle falls back to the
// lead-roled seat (never the link's repository's squad.yaml lead).
func TestStatusLeadHandleASymlinkedRootFallsBack(t *testing.T) {
	_, targetRoot := leadHandleSquad(t, "boss", "boss:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(targetRoot, "agents", "helper"), launch.Record{Handle: "helper", Root: targetRoot, Role: "lead"})
	link, _ := leadHandleSquad(t, "arch", "arch:architect")
	linkRoot := filepath.Join(link, ".agent-mail", "s")
	if err := os.RemoveAll(linkRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	lead, seats := statusWithin(t, linkRoot, 10*time.Second)
	if lead != "helper" {
		t.Errorf("a symlinked root: lead_handle = %q, want helper (the fallback; not the link's squad.yaml lead arch)", lead)
	}
	if !slices.Contains(seats, "helper") || !slices.Contains(seats, "boss") || slices.Contains(seats, "arch") {
		t.Errorf("a symlinked root: seats %v, want the target's (boss, helper)", seats)
	}
}

// #1045 round 2, finding 3: a FIFO squad.yaml never hangs the real
// `amq-squad status --json` (a blocking open fails this row by its
// timeout).
func TestStatusLeadHandleAFIFOSquadYAMLNeverHangsStatus(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	if err := os.Remove(filepath.Join(project, "squad.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(project, "squad.yaml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if lead, _ := statusWithin(t, root, 10*time.Second); lead != "helper" {
		t.Errorf("a FIFO squad.yaml: lead_handle = %q, want helper (the fallback)", lead)
	}
}

// #1045 round 2, finding 2: a squad.yaml that grows past the 1 MiB bound
// between readSmallRegular's stat and its read is refused (the fallback),
// through the real `amq-squad status --json`. The growth is placed between
// the two by the readSmallRegularAfterStat seam: a growth raced from
// another goroutine would land before or after the stat at random.
func TestStatusLeadHandleAFileGrowingPastTheBoundFallsBack(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	squad := filepath.Join(project, "squad.yaml")
	prev := readSmallRegularAfterStat
	t.Cleanup(func() { readSmallRegularAfterStat = prev })
	grown := false
	readSmallRegularAfterStat = func(path string) {
		if path != squad || grown {
			return
		}
		grown = true
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		if _, err := f.WriteString(strings.Repeat("# padding\n", (1<<20)/10+16)); err != nil {
			t.Error(err)
		}
	}
	if lead, _ := statusWithin(t, root, 10*time.Second); lead != "helper" {
		t.Errorf("a squad.yaml grown past the bound after the stat: lead_handle = %q, want helper (the fallback)", lead)
	}
	if !grown {
		t.Error("the growth seam never ran: the row did not test the read after the stat")
	}
}

// #1045 round 2 (Codex pass 1): a malformed squad.yaml (a line that is no
// entry) falls back, as squad.yaml's own strict parser refuses it; and a
// root reached through a symlinked repository (or an ancestor such as
// /var -> /private/var) is still its own squad's root.
func TestStatusLeadHandleAMalformedSquadYAMLOrARepositoryAlias(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	if lead, _ := statusWithin(t, filepath.Join(alias, ".agent-mail", "s"), 10*time.Second); lead != "cto" {
		t.Errorf("a root through a symlinked repository: lead_handle = %q, want cto (its own squad's lead)", lead)
	}
	for _, bad := range []string{
		"schema: 1\nsession: s\nlead: cto\n- broken\nseats:\n  - handle: cto\n    role: cto\n    runtime: pi\n",
		"schema: 1\nsession: s\nlead: cto\nseats: [\n", // Codex r2 pass 2: a malformed value
	} {
		mustWrite(t, filepath.Join(project, "squad.yaml"), bad)
		if lead, _ := statusWithin(t, root, 10*time.Second); lead != "helper" {
			t.Errorf("a malformed squad.yaml %q: lead_handle = %q, want helper (the fallback)", bad, lead)
		}
	}
}

// #1045 round 2 (Codex pass 2): the owner grant squad.yaml is parsed
// against is read non-blocking too: a FIFO at the owner's grants file never
// hangs the real `amq-squad status --json`, and status falls back.
func TestStatusLeadHandleAFIFOOwnerGrantNeverHangsStatus(t *testing.T) {
	_, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixStatusClock(t)
	// the document with no owner grant (the defaults: squad.yaml's lead)
	absent := statusDocWithin(t, root, 10*time.Second)
	if lead, _ := statusLeadAndSeats(t, absent); lead != "cto" {
		t.Fatalf("no owner grant: lead_handle = %q, want cto (the defaults)", lead)
	}
	if err := syscall.Mkfifo(filepath.Join(home, ".amq-squad", "grants.yaml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	fifo := statusDocWithin(t, root, 10*time.Second)
	if lead, _ := statusLeadAndSeats(t, fifo); lead != "helper" {
		t.Errorf("a FIFO owner grant: lead_handle = %q, want helper (the fallback)", lead)
	}
	// ruling (b): nothing from the grant appears in status output; only
	// lead_handle changes
	if a, b := statusDocWithoutLead(t, absent), statusDocWithoutLead(t, fifo); a != b {
		t.Errorf("the owner grant changed status beyond lead_handle:\nno grant: %s\nFIFO grant: %s", a, b)
	}
}

// #1045 round 2 (Codex pass 3, test strength): an owner grant file that
// parses but is untrusted (group- or world-writable) falls back, as
// grants.LoadFrom fails it closed: status exits 0 with the same seats.
// The same file made 0600 names the lead.
func TestStatusLeadHandleAnUntrustedOwnerGrantFallsBack(t *testing.T) {
	_, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(home, ".amq-squad", "grants.yaml")
	if err := os.WriteFile(owner, []byte("schema: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixStatusClock(t)
	trustedDoc := statusDocWithin(t, root, 10*time.Second)
	trusted, seats := statusLeadAndSeats(t, trustedDoc)
	if trusted != "cto" {
		t.Fatalf("a trusted owner grant: lead_handle = %q, want cto", trusted)
	}
	if err := os.Chmod(owner, 0o666); err != nil {
		t.Fatal(err)
	}
	untrustedDoc := statusDocWithin(t, root, 10*time.Second)
	lead, after := statusLeadAndSeats(t, untrustedDoc)
	if lead != "helper" {
		t.Errorf("an untrusted owner grant: lead_handle = %q, want helper (the fallback)", lead)
	}
	if !slices.Equal(seats, after) {
		t.Errorf("seats changed with the owner grant's trust: %v -> %v", seats, after)
	}
	// ruling (b): nothing from the grant appears in status output; only
	// lead_handle changes (the trusted 0600 and untrusted 0666 documents,
	// lead_handle removed)
	if a, b := statusDocWithoutLead(t, trustedDoc), statusDocWithoutLead(t, untrustedDoc); a != b {
		t.Errorf("the owner grant's trust changed status beyond lead_handle:\n0600: %s\n0666: %s", a, b)
	}
}
