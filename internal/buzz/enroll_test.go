package buzz

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// bech32 encodes data under hrp (BIP-173), for the canary's nsec1 form.
func bech32(hrp string, data []byte) string {
	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	var five []byte
	acc, bits := 0, 0
	for _, b := range data {
		acc = acc<<8 | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			five = append(five, byte(acc>>bits&31))
		}
	}
	if bits > 0 {
		five = append(five, byte(acc<<(5-bits)&31))
	}
	polymod := func(v []byte) uint32 {
		gen := []uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
		chk := uint32(1)
		for _, x := range v {
			top := chk >> 25
			chk = (chk&0x1ffffff)<<5 ^ uint32(x)
			for i := 0; i < 5; i++ {
				if top>>uint(i)&1 == 1 {
					chk ^= gen[i]
				}
			}
		}
		return chk
	}
	var exp []byte
	for _, c := range hrp {
		exp = append(exp, byte(c>>5))
	}
	exp = append(exp, 0)
	for _, c := range hrp {
		exp = append(exp, byte(c&31))
	}
	mod := polymod(append(append(exp, five...), 0, 0, 0, 0, 0, 0)) ^ 1
	out := hrp + "1"
	for _, x := range five {
		out += string(charset[x])
	}
	for i := 0; i < 6; i++ {
		out += string(charset[mod>>uint(5*(5-i))&31])
	}
	return out
}

type run struct {
	name   string
	rc     int
	out    string // stdout, or the pty stream (stdout and stderr together)
	errOut string
	took   time.Duration
}

// TestOwnerScriptDryRun runs the owner script docs/v3-buzz/enroll.sh end to
// end with a THROWAWAY owner key, the real amq, amq-remote and
// amq-owner-sign binaries and a local websocket relay, then the attachment
// (presence through the real amq-remote endpoint, a gate and a routed
// message through the real relay and the real amq), and finally greps every
// output channel for the canary key.
//
//   - acceptance 1: profile and presence on by default; 8 grants recorded;
//   - acceptance 2: no output carries the key (stdout, stderr, the pty, every
//     file under HOME and the squad root, the environment);
//   - acceptance 3: the gate question the attachment posts says "reply in
//     this thread";
//   - acceptance 5: the profile shows the session present (kind 10100
//     online) while its status row is live and not present (offline) after
//     it stops.
//
// The key is a throwaway generated here. It is never a real owner key.
//
// Each wait blocks on what the serve loop emits on the local relay (await:
// presence and profile rows, serve's running row at a delivery, its result
// rows, the verifier's connection closing), never on a polling deadline.
// One budget (runBudget, from after the build) ends a hung run and names
// the step; the behaviour checks run after each signal and no duration
// fails the test.
func TestOwnerScriptDryRun(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	amqBinaries(t)
	for _, bin := range []string{"script", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("the owner script is bash")
	}
	home := shortTempDir(t) // the endpoint socket path must fit sun_path (104 bytes)
	pm := realTempDir(t)    // the password manager stand-in, outside HOME
	bin := filepath.Join(home, "bin")
	amqSquad := filepath.Join(bin, "amq-squad")
	buildOffline(t, amqSquad)
	t.Setenv("HOME", home) // after the build (the Go cache stays in the real HOME); the test reads the share as the owner would
	ctx := budget(t)       // the run's one deadline, from after the build
	script, _ := filepath.Abs("../../docs/v3-buzz/enroll.sh")
	relay := newWSRelay(t)

	owner := newThrowaway(t)
	raw, _ := hex.DecodeString(owner.hex())
	canaryNsec := bech32("nsec", raw)
	canaryHex := owner.hex()
	canaries := []string{canaryNsec, canaryHex, strings.ToUpper(canaryHex)}
	keyFile := filepath.Join(pm, "owner-throwaway.key")
	if err := os.WriteFile(keyFile, []byte(canaryNsec+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	npub, _ := nostr.EncodeNpub(owner.pubHex())
	keyCmd := "cat '" + keyFile + "'" // stands in for: op read "op://vault/item/field"

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "BUZZ_") && !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "AM_") {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "AMQ_SQUAD="+amqSquad,
		"BUZZ_NO_CLIPBOARD=1", "BUZZ_UP_SETTLE=4", "AMQ_NO_UPDATE_CHECK=1")
	var runs []run
	plain := func(name string, extra ...string) run {
		cmd := exec.CommandContext(ctx, "bash", script, npub, relay.url())
		tree := guardTree(cmd)
		cmd.Env = append(slices.Clone(env), extra...)
		var o, e bytes.Buffer
		cmd.Stdout, cmd.Stderr = &o, &e
		start := time.Now()
		err := cmd.Run()
		r := run{name: name, out: o.String(), errOut: e.String(), took: time.Since(start)}
		if ctx.Err() != nil {
			tree.reap(t, name) // before any cleanup removes the scratch HOME
			t.Fatalf("hung: %s did not finish (%v)\n%s%s", name, context.Cause(ctx), r.out, r.errOut)
		}
		if ee, ok := err.(*exec.ExitError); ok {
			r.rc = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, r)
		t.Logf("%s: rc %d in %s\n%s%s", name, r.rc, r.took.Round(time.Millisecond), r.out, r.errOut)
		return r
	}
	buzzDir := filepath.Join(home, ".amq-squad", "buzz")
	shareRoot := filepath.Join(buzzDir, "root")
	t.Cleanup(func() { stopPID(filepath.Join(buzzDir, "enroll-up.pid")) })
	t.Cleanup(func() {
		if t.Failed() {
			for _, p := range []string{filepath.Join(shareRoot, "attach", "up.log"), filepath.Join(buzzDir, "doctor.json")} {
				data, _ := os.ReadFile(p)
				t.Logf("%s:\n%s", p, data)
			}
		}
	})

	// run 1: phase A, then the owner step asks for the pipe (exit 10)
	r1 := plain("run 1 (phase A)")
	if r1.rc != 10 || !strings.Contains(r1.out, "BUZZ_OWNER_KEY_CMD") {
		t.Fatalf("run 1: rc %d", r1.rc)
	}
	shareTxt, _ := os.ReadFile(filepath.Join(buzzDir, "share.txt"))
	var kinds []int
	for _, line := range strings.Split(string(shareTxt), "\n") {
		var k int
		if _, err := fmt.Sscanf(line, "kind %d preimage:", &k); err == nil {
			kinds = append(kinds, k)
		}
	}
	slices.Sort(kinds)
	want := slices.Sorted(slices.Values(DefaultKinds))
	if !slices.Equal(kinds, want) {
		t.Fatalf("requested kinds %v, want the default %v (profile on)", kinds, want)
	}
	body := ""
	for _, line := range strings.Split(string(shareTxt), "\n") {
		if v, ok := strings.CutPrefix(line, "body-pubkey:"); ok {
			body = strings.TrimSpace(v)
		}
	}

	// run 2: the owner step through the pipe, the body prefix typed on the pty
	r2 := ptyRun(ctx, t, "run 2 (owner signs through the pipe)", script, []string{npub, relay.url()}, append(slices.Clone(env), "BUZZ_OWNER_KEY_CMD="+keyCmd), body[:8]+"\n")
	runs = append(runs, r2)
	if r2.rc != 11 {
		t.Fatalf("run 2: rc %d\n%s", r2.rc, r2.out)
	}
	sh, err := LoadShare(shareRoot, DefaultShareSession)
	if err != nil {
		t.Fatal(err)
	}
	if sh.GrantCount() != 8 || sh.Owner != owner.pubHex() || sh.BodyHex() != body {
		t.Fatalf("enrolled: %d grants, owner %s, body %s", sh.GrantCount(), sh.Owner, sh.BodyHex())
	}
	window := time.Until(sh.Expires())
	if window < 29*24*time.Hour || window > 31*24*time.Hour {
		t.Fatalf("grant window %s, want 30 days (ruling 2)", window)
	}
	t.Logf("MEASURED: grants at enrollment %d (kinds %v), window %s, expires %s", sh.GrantCount(), sh.Kinds(), window.Round(time.Hour), sh.Expires().Format(time.RFC3339))
	manifest, _ := os.ReadFile(filepath.Join(shareRoot, "extensions", "remote", "manifest.json"))
	if !bytes.Contains(manifest, []byte(`"presence": true`)) || !bytes.Contains(manifest, []byte(`"name": "amq-squad"`)) || bytes.Contains(manifest, []byte("dm_channel_id")) ||
		!bytes.Contains(manifest, []byte(`"mailbox"`)) || !bytes.Contains(manifest, []byte(`"attach"`)) {
		t.Fatalf("manifest is not a presence-on mailbox target with DM commands not yet on:\n%s", manifest)
	}
	t.Logf("RECORDED (scratch-built amq-remote): manifest after phase D:\n%s", manifest)
	// the owner opens the DM: the relay now holds its NIP-29 snapshots
	relay.dmChannel(t, "dm-4-5-dryrun", owner.pubHex(), body)
	if len(relay.mem.events(30177, owner.pubHex())) != 1 {
		t.Fatal("the owner's kind 30177 policy is not on the relay")
	}
	await(ctx, t, &relay.bell, "profile and online status from the enrollment endpoint", func() bool {
		return len(relay.mem.events(0, body)) > 0 && lastStatus(relay, body) == "online"
	})
	if !relay.signedIn(body) {
		t.Fatal("the endpoint did not sign in with the body")
	}

	// run 3: the DM channel arrives, the binding is written, the endpoint stops
	r3 := plain("run 3 (bind)", "BUZZ_DM_CHANNEL=dm-4-5-dryrun")
	if r3.rc != 0 {
		t.Fatalf("run 3: rc %d", r3.rc)
	}
	await(ctx, t, &relay.bell, "offline at the enrollment endpoint's graceful stop", func() bool { return lastStatus(relay, body) == "offline" })
	// run 4: renewal for the same body, the key through the pipe again
	time.Sleep(1100 * time.Millisecond) // a new window must end after the current one
	r4 := ptyRun(ctx, t, "run 4 (renew)", script, []string{npub, relay.url()}, append(slices.Clone(env), "BUZZ_OWNER_KEY_CMD="+keyCmd, "BUZZ_RENEW=1", "BUZZ_DM_CHANNEL=dm-4-5-dryrun"), body[:8]+"\n")
	runs = append(runs, r4)
	renewed, err := LoadShare(shareRoot, DefaultShareSession)
	if r4.rc != 0 || err != nil || renewed.BodyHex() != body || renewed.GrantCount() != 8 || !renewed.Expires().After(sh.Expires()) {
		t.Fatalf("renew: rc %d, %v, body %s grants %d expires %s (was %s)", r4.rc, err, renewed.BodyHex(), renewed.GrantCount(), renewed.Expires(), sh.Expires())
	}
	t.Setenv("HOME", home)
	b, pin, err := gateverify.LoadBinding(gateverify.DefaultBindingPath())
	if err != nil || pin.Body != body || pin.Owner != owner.pubHex() || b.Channel != "dm-4-5-dryrun" || b.ShareRoot != shareRoot {
		t.Fatalf("binding %+v %+v, %v", b, pin, err)
	}

	// the squad: one lead (lead-a) the agent attaches to, lead-b routable
	squad := filepath.Join(realTempDir(t), "root")
	// the scratch HOME and AMQ_NO_UPDATE_CHECK=1: amq starts its update notifier before init
	initCmd := amqCmd(ctx, t, env, "init", "--root", squad, "--agents", "buzz,lead-a,lead-b")
	initTree := guardTree(initCmd)
	if out, err := initCmd.CombinedOutput(); ctx.Err() != nil {
		initTree.reap(t, "amq init")
		t.Fatalf("hung: amq init did not finish (%v)\n%s", context.Cause(ctx), out)
	} else if err != nil {
		t.Fatalf("amq init: %v\n%s", err, out)
	}
	target, err := Resolve(squad, "lead-a", []string{"lead-b"})
	writeLaunchRecord(t, squad, "lead-a", "lead")
	writeLaunchRecord(t, squad, "lead-b", "lead")
	target.Registry = LaunchRecordLeads{Roles: DefaultLeadRoles}
	if err != nil {
		t.Fatal(err)
	}
	doc := func() []byte {
		st := status.Build(status.Collect(status.CollectOptions{Root: squad, Session: "s", Now: time.Now().UTC(), PIDAlive: func(int) bool { return false }}))
		raw, _ := json.Marshal(st)
		return raw
	}
	setPresence := func(state string, seen time.Time) {
		raw, _ := json.Marshal(map[string]any{"status": state, "last_seen": seen.UTC()})
		if err := os.WriteFile(filepath.Join(squad, "agents", "lead-a", "presence.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// acceptance 5: presence follows the status document through amq-remote
	ep := &ExecEndpoint{Root: shareRoot}
	sup := Supervisor{Endpoint: ep}
	t.Cleanup(func() { _ = ep.Stop() })
	setPresence("active", time.Now())
	p, err := FromStatus(doc(), "lead-a")
	if err != nil || !p.Present {
		t.Fatalf("live lead = %+v, %v", p, err)
	}
	t0 := time.Now()
	if act, err := sup.Apply(p); act != "started" || err != nil {
		t.Fatalf("apply live: %q %v", act, err)
	}
	await(ctx, t, &relay.bell, "online while the lead runs", func() bool { return lastStatus(relay, body) == "online" })
	onlineAfter := time.Since(t0)
	setPresence("active", time.Now().Add(-time.Hour)) // the session stopped: its presence goes stale
	p, _ = FromStatus(doc(), "lead-a")
	if p.Present {
		t.Fatalf("stopped lead = %+v", p)
	}
	t1 := time.Now()
	if act, err := sup.Apply(p); act != "stopped" || err != nil {
		t.Fatalf("apply stopped: %q %v", act, err)
	}
	await(ctx, t, &relay.bell, "offline after the lead stops", func() bool { return lastStatus(relay, body) == "offline" })
	offlineAfter := time.Since(t1)
	t.Logf("MEASURED: presence online %s after the status row went live; offline %s after it went stale", onlineAfter.Round(time.Millisecond), offlineAfter.Round(time.Millisecond))

	// acceptance 1 and 6 through the real serve (5-amq-B): the share is bound
	// to the mailbox adapter; serve delivers the owner's DMs into the share
	// root's attach inbox; the handler verifies the gate answer on its own
	// short connection and routes the phone message; serve posts each reply
	// as the body's result row
	mb, err := CheckMailboxShare(shareRoot, DefaultShareSession, "dm-4-5-dryrun", relay.url())
	if err != nil {
		t.Fatalf("the share is not bound to the mailbox adapter: %v", err)
	}
	t.Logf("MEASURED: share binding %+v", mb)
	wsr, sh2, post, err := Connect(ctx, b, pin, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := operatorauth.LoadEd25519Signer(b.IssuerKey)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := gatestore.Open(squad)
	core := &gateverify.Core{Store: store, Pin: pin, Relay: wsr, RelayURL: b.Relay, Body: sh2.Body, BodyGrant: post, Issuer: issuer,
		Carriers: b.Carriers, Version: "3", Namespace: gateverify.Namespace{ProjectDir: squad, Profile: "p", Session: "s", Generation: "g1"}}
	rec, q, err := core.Raise(ctx, gatestore.Spec{GateID: "rel-dry", Action: "tag", Target: "git tag v3.0.0-dry", RaisedBy: "lead-a", Window: 30 * time.Minute})
	wsr.Close() // gate raise's connection: the question is posted, the connection ends
	core.Relay = nil
	if err != nil {
		t.Fatal(err)
	}
	questions := relay.mem.events(9, body)
	if len(questions) == 0 || questions[len(questions)-1].ID != q.EventID || !strings.Contains(questions[len(questions)-1].Content, "\nYes lets it run. No stops it.\n") {
		t.Fatalf("gate question on the relay: %+v", questions)
	}
	t.Logf("gate question text:\n%s", questions[len(questions)-1].Content)
	setPresence("active", time.Now())
	p, _ = FromStatus(doc(), "lead-a")
	if act, err := sup.Apply(p); act != "started" || err != nil {
		t.Fatalf("apply live for the serve rows: %q %v", act, err)
	}
	await(ctx, t, &relay.bell, "online before the DMs", func() bool { return lastStatus(relay, body) == "online" })
	ownerDM := func(content, thread string) string {
		tags := [][]string{{"h", pin.Channel}, {"p", body}}
		if thread != "" {
			tags = append(tags, []string{"e", thread, "", "reply"})
		}
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: tags, Content: content}
		_ = owner.sk.Sign(&e)
		_ = relay.mem.Publish(ctx, e.Marshal())
		return e.ID
	}
	inbox := AMQInbox{Root: shareRoot, Handle: MailboxHandle}
	h := &Handler{Core: core, Dial: VerifyDialer(b, pin), Inbox: inbox, Mail: AMQMailer{}, Target: target}
	prefix := strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	// one request at a time: the answer first, then the route
	tAnswer := time.Now()
	answerID := ownerDM("Approve rel-dry release "+prefix, q.EventID)
	// serve's running row for the answer: the delivery into the attach
	// inbox is done (serveAdmitted)
	await(ctx, t, &relay.bell, "serve delivers the answer into the attach inbox", func() bool { return serveAdmitted(relay, body, answerID) })
	tDelivered := time.Since(tAnswer)
	delivered, err := inbox.New(ctx)
	if err != nil || len(delivered) != 1 {
		t.Fatalf("the attach inbox after serve's delivery of the answer: %+v, %v", delivered, err)
	}
	if d := delivered[0]; d.From != CarrierHandle || d.Thread != "cockpit/"+pin.Channel || !slices.Equal(d.Labels, []string{"acp", "buzz", "nostr:" + answerID}) {
		t.Fatalf("serve's delivery shape: %+v", d)
	}
	t.Logf("RECORDED (scratch-built amq-remote): serve's delivery header %+v", delivered[0])
	openBefore := relay.open()
	tStep := time.Now()
	handled, err := h.Step(ctx)
	if err != nil || len(handled) != 1 || handled[0].Gate == nil || handled[0].Gate.Outcome != "approved" || handled[0].Reply != "accepted rel-dry tag" {
		t.Fatalf("answer: %+v, %v", handled, err)
	}
	stepTook := time.Since(tStep)
	idsSeen := relay.idsREQs()
	await(ctx, t, &relay.bell, "the verifier's connection closes", func() bool { return relay.open() == openBefore })
	if len(idsSeen) == 0 || idsSeen[len(idsSeen)-1] != openBefore+1 {
		t.Fatalf("connections at the verifier's REQ by id: %v (serve's: %d)", idsSeen, openBefore)
	}
	await(ctx, t, &relay.bell, "serve posts the accepted result row", func() bool { return bodyPosted(relay, body, "accepted rel-dry tag") })
	tRow := time.Since(tAnswer)
	for _, k := range []int{9, 40003} {
		for _, e := range relay.mem.events(k, body) {
			if strings.Contains(e.Content, "rel-dry") && !strings.Contains(e.Content, "\nYes lets it run. No stops it.\n") {
				t.Logf("RECORDED (scratch-built amq-remote): serve's result row kind %d tags %v content %q", e.Kind, e.Tags, e.Content)
			}
		}
	}
	t.Logf("MEASURED (scratch-built amq-remote, local relay): owner answer to serve's running row (delivery done) %s; handler step (fetch connection, verify, commit, reply) %s; answer to the result row on the relay %s; open connections at the fetch %d (serve %d), after %d",
		tDelivered.Round(time.Millisecond), stepTook.Round(time.Millisecond), tRow.Round(time.Millisecond), idsSeen[len(idsSeen)-1], openBefore, relay.open())
	if _, err := store.ReadAnswer("rel-dry"); err != nil {
		t.Fatalf("no answer file: %v", err)
	}
	routeID := ownerDM("@lead-b please rebase #12", "")
	await(ctx, t, &relay.bell, "serve delivers the route", func() bool { return serveAdmitted(relay, body, routeID) })
	if delivered, err = inbox.New(ctx); err != nil || len(delivered) != 1 {
		t.Fatalf("the attach inbox after serve's delivery of the route: %+v, %v", delivered, err)
	}
	handled, err = h.Step(ctx)
	if err != nil || len(handled) != 1 || handled[0].Delivery == nil || handled[0].Delivery.Lead != "lead-b" || !routedTo(handled[0].Reply, "lead-b") {
		t.Fatalf("route: %+v, %v", handled, err)
	}
	if !inboxHas(t, squad, "lead-a", "nostr:"+routeID) || !inboxHas(t, squad, "lead-b", "nostr:"+routeID) {
		t.Fatal("the routed message is not in lead-a's and lead-b's inboxes")
	}
	await(ctx, t, &relay.bell, "serve posts the routed result row", func() bool { return bodyPosted(relay, body, "routed lead-b") })
	for _, e := range append(relay.mem.events(9, body), relay.mem.events(40003, body)...) {
		if strings.Contains(e.Content, "please rebase") || strings.Contains(e.Content, "Approve rel-dry") {
			t.Fatalf("a body post echoes the owner's text: %q", e.Content)
		}
	}
	setPresence("active", time.Now().Add(-time.Hour))
	p, _ = FromStatus(doc(), "lead-a")
	if act, err := sup.Apply(p); act != "stopped" || err != nil {
		t.Fatalf("apply stopped after the serve rows: %q %v", act, err)
	}
	await(ctx, t, &relay.bell, "offline after the serve rows", func() bool { return lastStatus(relay, body) == "offline" })

	// the owner run (docs/v3-buzz/owner-run.md steps 0.3 to 7) rehearsed
	// through the binary with the throwaway key: team init, gate raise,
	// remote attach, the thread answer, gate status, verify action, the
	// routed message in fullstack's inbox, presence on and off
	runs = append(runs, rehearseOwnerRun(ctx, t, env, amqSquad, home, relay, owner, body)...)

	// acceptance 2: the canary is in no output channel
	var scanned, bytesScanned int
	hits := func(text string) bool {
		for _, c := range canaries {
			if strings.Contains(text, c) {
				return true
			}
		}
		return false
	}
	// positive controls: the scan finds the key where it is (the password
	// manager stand-in) and in any text that carries either form
	if pmKey, _ := os.ReadFile(keyFile); !hits(string(pmKey)) || !hits("x"+canaryHex+"y") || !hits(strings.ToUpper(canaryHex)) {
		t.Fatal("the canary scan does not detect the key")
	}
	check := func(where, text string) {
		if hits(text) {
			t.Errorf("CANARY FOUND in %s", where)
		}
	}
	for _, r := range runs {
		check(r.name+" stdout/pty", r.out)
		check(r.name+" stderr", r.errOut)
		scanned += 2
		bytesScanned += len(r.out) + len(r.errOut)
	}
	check("environment", strings.Join(env, "\n"))
	for _, dir := range []string{home, squad} {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() || strings.HasPrefix(path, bin+string(os.PathSeparator)) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			check(path, string(data))
			scanned++
			bytesScanned += len(data)
			return nil
		})
	}
	t.Logf("CANARY GREP: %d channels and files, %d bytes, canary forms %d (nsec1, hex, HEX): %s", scanned, bytesScanned, len(canaries), map[bool]string{true: "FOUND", false: "not found"}[t.Failed()])
}

// buildOffline builds amq-squad into out with GOPROXY=off. The tree is
// built from the go.mod the test binary was just built from, so the module
// cache holds every module it needs; a missing one fails the build closed
// with a clear message instead of a download. It uses the real HOME (the Go
// caches), so it runs before the test moves HOME.
func buildOffline(t *testing.T, out string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, "../../cmd/amq-squad")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build amq-squad offline (GOPROXY=off; the module cache must hold every module go.mod names, as it did for this test binary): %v\n%s", err, msg)
	}
}

// shortTempDir is a temporary directory under /tmp, symlinks resolved.
func shortTempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "4e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	d, err = filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func lastStatus(r *wsRelay, body string) string {
	evs := r.mem.events(10100, body)
	if len(evs) == 0 {
		return ""
	}
	var c struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(evs[len(evs)-1].Content), &c)
	return c.Status
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

func inboxHas(t *testing.T, root, handle, label string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(filepath.Join(root, "agents", handle, "inbox"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(path)
			found = found || (bytes.Contains(data, []byte(label)) && bytes.Contains(data, []byte(`"from": "buzz"`)))
		}
		return nil
	})
	return found
}

func stopPID(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); err == nil && pid > 0 {
		if p, err := os.FindProcess(pid); err == nil {
			// SIGTERM first: amq-remote up stops its serve child on it; a
			// SIGKILL would orphan serve
			_ = p.Signal(syscall.SIGTERM)
			for i := 0; i < 50 && p.Signal(syscall.Signal(0)) == nil; i++ {
				time.Sleep(100 * time.Millisecond)
			}
			_ = p.Kill()
		}
	}
}

// ptyRun runs the script under script(1), so /dev/tty is a pty and the body
// prefix is typed on it, while the key comes through BUZZ_OWNER_KEY_CMD's
// pipe. The pty input stays open until the script exits (an early EOF reads
// as "not confirmed"). ctx ends a run that hangs: the caller's budget.
func ptyRun(ctx context.Context, t *testing.T, name, scriptPath string, args, env []string, typed string) run {
	t.Helper()
	argv := append([]string{"bash", scriptPath}, args...)
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "script", append([]string{"-q", "/dev/null"}, argv...)...)
	} else {
		quoted := make([]string, len(argv))
		for i, a := range argv {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		cmd = exec.CommandContext(ctx, "script", "-q", "-e", "-c", strings.Join(quoted, " "), "/dev/null")
	}
	tree := guardTree(cmd)
	cmd.Env = env
	w, err := cmd.StdinPipe() // an OS pipe: Wait closes it after the exit
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(4 * time.Second)
		_, _ = io.WriteString(w, typed)
	}()
	err = cmd.Wait()
	r := run{name: name, out: out.String(), took: time.Since(start)}
	if ctx.Err() != nil { // Errorf: a caller may run this off the test goroutine
		tree.reap(t, name) // before any cleanup removes the scratch HOME
		t.Errorf("hung: %s did not finish (%v)\n%s", name, context.Cause(ctx), r.out)
		r.rc = -1
		return r
	}
	if ee, ok := err.(*exec.ExitError); ok {
		r.rc = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: rc %d in %s\n%s", name, r.rc, r.took.Round(time.Millisecond), r.out)
	return r
}

// AMQFloor is the amq-remote release the mailbox adapter path needs (briefs
// ruling 20 point 4; part A's doctor floor).
const AMQFloor = "0.116.2"

// amqBinaries puts the AMQ binaries the dry run uses first on PATH:
// AMQ_SQUAD_TEST_AMQ_BIN names a directory of amq, amq-remote and
// amq-owner-sign built from the floor release (5-amq-B: scratch-built
// v0.116.2, never installed); without it the installed ones. It skips when
// a binary is missing or amq-remote is below the floor, and logs the
// versions and where they came from.
func amqBinaries(t *testing.T) {
	t.Helper()
	if dir := os.Getenv("AMQ_SQUAD_TEST_AMQ_BIN"); dir != "" {
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for _, bin := range []string{"amq", "amq-remote", "amq-owner-sign"} {
		p, err := exec.LookPath(bin)
		if err != nil {
			t.Skipf("%s not installed", bin)
		}
		args := []string{"--version"}
		if bin == "amq-remote" {
			args = []string{"version"}
		}
		// amq through the scratch-HOME helper; the other two get its
		// environment (this runs before the test changes HOME)
		var cmd *exec.Cmd
		if filepath.Base(p) == "amq" {
			cmd = amqCmd(context.Background(), t, nil, args...)
		} else {
			cmd = exec.Command(p, args...) // exec: not amq (amq-remote, amq-owner-sign)
			cmd.Env = amqEnv(t, nil)
		}
		out, _ := cmd.CombinedOutput()
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "amq-remote"))
		t.Logf("AMQ binary %s: %s (%s)", bin, v, p)
		if bin == "amq-remote" && semverLess(v, AMQFloor) {
			t.Skipf("amq-remote %s is below the floor v%s (the mailbox adapter); set AMQ_SQUAD_TEST_AMQ_BIN to a v%s build", v, AMQFloor, AMQFloor)
		}
	}
}

// semverLess compares two x.y.z versions; an unreadable one is less.
func semverLess(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i >= len(pa) {
			return true
		}
		if _, err := fmt.Sscanf(pa[i], "%d", &x); err != nil {
			return true
		}
		_, _ = fmt.Sscanf(pb[i], "%d", &y)
		if x != y {
			return x < y
		}
	}
	return false
}

// bodyPosted reports whether the body posted a result row (kind 9) or an
// edit of one (kind 40003) whose text contains want.
func bodyPosted(r *wsRelay, body, want string) bool {
	for _, k := range []int{9, 40003} {
		for _, e := range r.mem.events(k, body) {
			if strings.Contains(e.Content, want) {
				return true
			}
		}
	}
	return false
}
