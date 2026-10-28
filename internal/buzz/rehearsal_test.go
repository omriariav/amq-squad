package buzz

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// rehearseOwnerRun runs the commands of docs/v3-buzz/owner-run.md steps 0.3
// to 7 through the amq-squad binary, after the dry-run enrollment, with the
// throwaway owner key standing in for the owner's phone. It returns the
// command outputs for the canary grep.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rehearseOwnerRun(t *testing.T, env []string, amqSquad, home string, relay *wsRelay, owner throwaway, body string) []run {
	t.Helper()
	var runs []run
	sh := func(name string, argv ...string) run {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env, cmd.Dir = env, home
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		err := cmd.Run()
		r := run{name: "rehearsal: " + name, out: o.String(), errOut: e.String()}
		if ee, ok := err.(*exec.ExitError); ok {
			r.rc = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
		t.Logf("%s: rc %d\n%s%s", r.name, r.rc, r.out, r.errOut)
		return r
	}
	// 0.3 the scratch squad (lead cto) and the operator session (user)
	p := filepath.Join(home, "owner45", "project")
	op := filepath.Join(home, "owner45", "operator")
	for _, d := range []string{p, op} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	teamArgs := []string{"--project", p, "--roles", "cto", "--runtime", "cto=codex", "--lead", "cto", "--session", "owner45"}
	r := sh("0.3 team propose", append([]string{amqSquad, "team", "propose", "--json"}, teamArgs...)...)
	var proposed struct {
		Data struct {
			Digest string `json:"digest"`
		} `json:"data"`
	}
	if r.rc != 0 || json.Unmarshal([]byte(r.out), &proposed) != nil || proposed.Data.Digest == "" {
		t.Fatalf("team propose: rc %d", r.rc)
	}
	if r := sh("0.3 team apply", append([]string{amqSquad, "team", "apply", "--digest", proposed.Data.Digest}, teamArgs...)...); r.rc != 0 {
		t.Fatalf("team apply: rc %d", r.rc)
	}
	root := filepath.Join(p, ".agent-mail", "owner45")
	opRoot := filepath.Join(op, ".agent-mail", "ops")
	if r := sh("0.3 amq init (squad)", "amq", "init", "--root", root, "--agents", "buzz,cto"); r.rc != 0 {
		t.Fatalf("amq init: rc %d", r.rc)
	}
	// stand-in for the launch record the lead's launch leaves (owner-run 0.3):
	// routing checks the seat's role, not only the registry
	writeLaunchRecord(t, root, "cto", "cto")
	if r := sh("0.3 amq init (operator)", "amq", "init", "--root", opRoot, "--agents", "buzz,user"); r.rc != 0 {
		t.Fatalf("amq init: rc %d", r.rc)
	}
	// AMQ cross-project routing: the operator project names the squad as a peer
	writeFile(t, filepath.Join(op, ".amqrc"), `{"root": ".agent-mail", "project": "fleet", "peers": {"app": "`+filepath.Join(p, ".agent-mail")+`"}}`)
	writeFile(t, filepath.Join(p, ".amqrc"), `{"root": ".agent-mail", "project": "app", "peers": {"fleet": "`+filepath.Join(op, ".agent-mail")+`"}}`)
	if r := sh("0.3 fleet operator", amqSquad, "fleet", "operator", "--root", opRoot, "--amq-project", "fleet", "--session", "ops"); r.rc != 0 {
		t.Fatalf("fleet operator: rc %d", r.rc)
	}
	if r := sh("0.3 fleet add", amqSquad, "fleet", "add", "--name", "app", "--root", root, "--project-path", p, "--repository", "owner/app", "--lead", "cto", "--session", "owner45"); r.rc != 0 {
		t.Fatalf("fleet add: rc %d", r.rc)
	}
	presence := func(state string) {
		raw, _ := json.Marshal(map[string]any{"status": state, "last_seen": time.Now().UTC()})
		writeFile(t, filepath.Join(opRoot, "agents", "user", "presence.json"), string(raw))
	}
	// 4.1, 4.2 the operator session runs, attach (no --lead: the operator)
	presence("active")
	attach := exec.Command(amqSquad, "remote", "attach", "--project", p, "--session", "owner45", "--me", "cto", "--interval", "1s", "--json")
	attach.Env, attach.Dir = env, home
	var aout, aerr syncBuffer
	attach.Stdout, attach.Stderr = &aout, &aerr
	if err := attach.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stopAttach := func() {
		if stopped {
			return
		}
		stopped = true
		_ = attach.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- attach.Wait() }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = attach.Process.Kill()
			<-done
		}
		runs = append(runs, run{name: "rehearsal: 4.2 remote attach", out: aout.String(), errOut: aerr.String()})
		t.Logf("rehearsal: 4.2 remote attach output:\n%s%s", aout.String(), aerr.String())
	}
	t.Cleanup(stopAttach)
	waitFor(t, 30*time.Second, "attach starts the endpoint and the agent is online", func() bool {
		return strings.Contains(aout.String(), `"action":"started"`) && lastStatus(relay, body) == "online"
	})
	// 5.1 the gate
	r = sh("5.1 gate raise", amqSquad, "gate", "raise", "--project", p, "--session", "owner45", "--me", "cto", "--gate", "g45-owner-01", "--action", "tag", "--target", "git tag v3.0.0-owner45", "--json")
	var raised struct {
		Data struct {
			QuestionSent    bool   `json:"question_sent"`
			QuestionEventID string `json:"question_event_id"`
			Gate            struct {
				Digest, Text string
			} `json:"gate"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.out), &raised); err != nil || r.rc != 0 || !raised.Data.QuestionSent || !strings.Contains(raised.Data.Gate.Text, "Reply in this thread") {
		t.Fatalf("gate raise: rc %d %+v %v", r.rc, raised, err)
	}
	tRaise := time.Now()
	// 5.2 the owner's thread reply (the throwaway key stands in for the phone)
	dm := func(content, thread string) string {
		tags := [][]string{{"h", "dm-4-5-dryrun"}, {"p", body}}
		if thread != "" {
			tags = append(tags, []string{"e", thread, "", "reply"})
		}
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: tags, Content: content}
		_ = owner.sk.Sign(&e)
		_ = relay.mem.Publish(t.Context(), e.Marshal())
		return e.ID
	}
	prefix := strings.TrimPrefix(raised.Data.Gate.Digest, "sha256:")[:16]
	dm("approve g45-owner-01 release "+prefix, raised.Data.QuestionEventID)
	waitFor(t, 20*time.Second, "the attach verifies the thread answer", func() bool { return strings.Contains(aout.String(), `"outcome":"approved"`) })
	t.Logf("MEASURED (rehearsal, local relay): raise to verified through remote attach %s (tick 1 s)", time.Since(tRaise).Round(time.Millisecond))
	// 5.3
	if r := sh("5.3 gate status", amqSquad, "gate", "status", "--project", p, "--session", "owner45", "--gate", "g45-owner-01", "--json"); r.rc != 0 || !strings.Contains(r.out, "accepted") {
		t.Fatalf("gate status: rc %d", r.rc)
	}
	if r := sh("5.3 verify action", amqSquad, "verify", "action", "--project", p, "--session", "owner45", "--gate", "g45-owner-01", "--action", "tag", "--target", "git tag v3.0.0-owner45"); r.rc != 0 {
		t.Fatalf("verify action: rc %d", r.rc)
	}
	// the owner sees serve's result row before the next DM: the mailbox
	// adapter runs one request at a time and refuses a DM that arrives
	// while one is open as busy (v0.116.2 internal/remote/core/admission.go)
	waitFor(t, 20*time.Second, "serve's accepted result row", func() bool { return bodyPosted(relay, body, "accepted g45-owner-01 tag") })
	// 6.1 /status is serve's own command (v0.116.2 buzzio/normalize.go): it
	// never reaches the mailbox
	dm("/status", "")
	time.Sleep(time.Second)
	// 6.2
	pingID := dm("@cto owner45 ping from the phone", "")
	waitFor(t, 20*time.Second, "the routed message", func() bool { return strings.Contains(aout.String(), `"lead":"app/cto"`) })
	waitFor(t, 20*time.Second, "serve's routed result row", func() bool { return bodyPosted(relay, body, "routed app/cto") })
	r = sh("6.2 amq list (operator mailbox)", "amq", "list", "--root", opRoot, "--me", "user", "--new", "--json")
	if r.rc != 0 || !strings.Contains(r.out, "nostr:"+pingID) {
		t.Fatalf("operator mailbox: rc %d\n%s", r.rc, r.out)
	}
	r = sh("6.2 amq list (the squad lead)", "amq", "list", "--root", root, "--me", "cto", "--new", "--json")
	if r.rc != 0 || !strings.Contains(r.out, "nostr:"+pingID) || !strings.Contains(r.out, "routed by user") {
		t.Fatalf("the lead's inbox: rc %d\n%s", r.rc, r.out)
	}
	// 7 presence off
	presence("stopped")
	waitFor(t, 20*time.Second, "offline after the operator session stops", func() bool {
		return strings.Contains(aout.String(), `"action":"stopped"`) && lastStatus(relay, body) == "offline"
	})
	stopAttach()
	if !strings.Contains(aout.String(), `"kind":"detached"`) || strings.Contains(aout.String(), `/status`) {
		t.Fatalf("attach output lacks the detach, or the mailbox saw /status:\n%s", aout.String())
	}
	// the DMs the earlier handler took (for another squad root) are not
	// handled again: serve never delivers a DM twice, and the taken
	// messages left the attach inbox
	if strings.Contains(aout.String(), "rel-dry") || strings.Contains(aout.String(), "please rebase") {
		t.Fatalf("an earlier DM was handled again:\n%s", aout.String())
	}
	return runs
}
