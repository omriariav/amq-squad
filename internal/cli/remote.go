package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

const remoteUsage = `amq-squad remote - the Buzz attachment (plan 3.6, 9; R42)

Usage:
  amq-squad remote bind --relay URL --channel ID [--share-root DIR] [--share-session S] [--json]
  amq-squad remote attach --lead HANDLE [--leads a,b] [--project DIR] [--session S] [--once] [--interval 5s] [--json]
  amq-squad remote status --lead HANDLE [--project DIR] [--session S] [--json]
  amq-squad remote npub KEY

Enrollment is the owner script docs/v3-buzz/enroll.sh (briefs 4-5 ruling 1);
it ends with remote bind. bind writes the owner's gate binding from the
enrolled share (no secret in it), and the issuer key and trust store when
they are absent. attach binds the share's Buzz agent to one mailbox (one
squad lead until the operator session lands, brief 4-3), reads the owner's
DMs, sends gate answers to the core, answers /status, routes every other
message through AMQ, and runs the share's amq-remote endpoint only while the
mailbox's seat is present in the status document. status prints the
presence and activity line and the login service warning.
`

// remoteTransport, remoteEndpoint and remoteMailer are seams for tests.
var (
	remoteMailer   buzz.Mailer       = buzz.AMQMailer{}
	remoteService  buzz.LoginService = buzz.AMQRemoteService{}
	remoteEndpoint                   = func(shareRoot string) buzz.Endpoint { return &buzz.ExecEndpoint{Root: shareRoot} }
)

func runRemote(args []string, version string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stdout, remoteUsage)
		return nil
	}
	switch args[0] {
	case "bind":
		return runRemoteBind(args[1:], os.Stdout)
	case "attach":
		return runRemoteAttach(args[1:], version, os.Stdout)
	case "status":
		return runRemoteStatus(args[1:], version, os.Stdout)
	case "npub":
		return runRemoteNpub(args[1:], os.Stdout)
	}
	return usageErrorf("remote: unknown subcommand %q (bind, attach, status, npub)", args[0])
}

// runRemoteNpub prints "<hex> <npub>" for one public key given as either
// (the owner script's helper; public keys only).
func runRemoteNpub(args []string, out io.Writer) error {
	if len(args) != 1 {
		return usageErrorf("remote npub takes one public key (npub1... or 64 hex)")
	}
	hex, err := nostr.ParsePubKey(args[0])
	if err != nil {
		return usageErrorf("remote npub: %v", err)
	}
	npub, err := nostr.EncodeNpub(hex)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s %s\n", hex, npub)
	return err
}

type remoteBindData struct {
	Binding    string   `json:"binding"`
	Relay      string   `json:"relay"`
	Channel    string   `json:"channel"`
	Owner      string   `json:"owner"`
	Body       string   `json:"body"`
	BodyNpub   string   `json:"body_npub"`
	Grants     int      `json:"grants"`
	Kinds      []int    `json:"kinds"`
	Expires    string   `json:"expires"`
	IssuerKey  string   `json:"issuer_key"`
	TrustStore string   `json:"trust_store"`
	Created    []string `json:"created"`
}

func runRemoteBind(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("remote bind", flag.ContinueOnError)
	relay := fs.String("relay", "", "the Buzz relay (wss://)")
	channel := fs.String("channel", "", "the owner's DM channel id with the body")
	shareRoot := fs.String("share-root", buzz.DefaultShareRoot(), "AMQ root of the enrolled share")
	shareSession := fs.String("share-session", buzz.DefaultShareSession, "share session")
	carrier := fs.String("carrier", buzz.CarrierHandle, "AMQ carrier handle of phone messages")
	jsonOut := fs.Bool("json", false, "emit schema-versioned JSON")
	fs.Usage = func() { fmt.Fprint(os.Stderr, remoteUsage) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *relay == "" || *channel == "" {
		return usageErrorf("remote bind needs --relay and --channel")
	}
	sh, err := buzz.LoadShare(*shareRoot, *shareSession)
	if err != nil {
		return fmt.Errorf("remote bind: %w", err)
	}
	if _, err := sh.Grant(buzz.KindDM, time.Now()); err != nil {
		return fmt.Errorf("remote bind: %w", err)
	}
	path := gateBindingPath()
	if path == "" {
		return errors.New("remote bind: no home directory")
	}
	b := gateverify.Binding{Schema: gateverify.BindingSchema, Relay: *relay, Channel: *channel, Owner: sh.Owner, Body: sh.BodyHex(),
		Carriers: []string{*carrier}, ShareRoot: sh.Root, ShareSession: sh.Session}
	data := remoteBindData{Binding: path, Relay: b.Relay, Channel: b.Channel, Owner: b.Owner, Body: b.Body, Grants: sh.GrantCount(),
		Kinds: sh.Kinds(), Expires: sh.Expires().Format(time.RFC3339), IssuerKey: gateverify.DefaultIssuerKeyPath(), TrustStore: gateTrustStorePath()}
	data.BodyNpub, _ = nostr.EncodeNpub(b.Body)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(b, "", "  ")
	if err := writeOwnerFile(path, append(raw, '\n')); err != nil {
		return fmt.Errorf("remote bind: %w", err)
	}
	// the binding must read back through the 3-1 loader, or it is not written right
	if _, _, err := gateverify.LoadBinding(path); err != nil {
		return fmt.Errorf("remote bind: %w", err)
	}
	created, err := ensureIssuer(data.IssuerKey, data.TrustStore)
	if err != nil {
		return fmt.Errorf("remote bind: %w", err)
	}
	data.Created = created
	if *jsonOut {
		return writeJSONEnvelope(out, "remote_bind", data)
	}
	fmt.Fprintf(out, "gate binding written: %s\nbody %s (%s), owner %s\n%d grants (kinds %v), expire %s\n", path, data.BodyNpub, b.Body, b.Owner, data.Grants, data.Kinds, data.Expires)
	for _, c := range created {
		fmt.Fprintf(out, "created %s\n", c)
	}
	return nil
}

// writeOwnerFile replaces an owner file atomically at mode 0600.
func writeOwnerFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ensureIssuer creates the core's Ed25519 issuer key and a trust store that
// trusts it, each only when absent. An existing trust store that does not
// trust the issuer key is an error for the owner to resolve; it is never
// edited here.
func ensureIssuer(keyPath, trustPath string) ([]string, error) {
	var created []string
	if _, err := os.Lstat(keyPath); errors.Is(err, os.ErrNotExist) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		clear(priv)
		if err != nil {
			return nil, err
		}
		block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		clear(der)
		err = writeOwnerFile(keyPath, block)
		clear(block)
		if err != nil {
			return nil, err
		}
		created = append(created, keyPath)
	}
	signer, err := operatorauth.LoadEd25519Signer(keyPath)
	if err != nil {
		return nil, err
	}
	defer signer.Destroy()
	if _, err := os.Lstat(trustPath); errors.Is(err, os.ErrNotExist) {
		pub, err := issuerPublic(keyPath)
		if err != nil {
			return nil, err
		}
		store := map[string]any{"schema_version": operatorauth.AuthorizationTrustStoreSchemaVersion, "keys": []map[string]string{{
			"key_id": signer.KeyID(), "algorithm": "ed25519", "public_key": base64.StdEncoding.EncodeToString(pub), "status": "trusted"}}}
		raw, _ := json.MarshalIndent(store, "", "  ")
		if err := writeOwnerFile(trustPath, append(raw, '\n')); err != nil {
			return nil, err
		}
		created = append(created, trustPath)
	}
	store, err := operatorauth.LoadAuthorizationTrustStore(trustPath)
	if err != nil {
		return nil, err
	}
	probe := []byte("amq-squad remote bind trust probe")
	sig, err := signer.Sign(probe)
	if err != nil {
		return nil, err
	}
	if err := store.Verify(signer.KeyID(), probe, sig); err != nil {
		return nil, fmt.Errorf("trust store %s does not trust the issuer key %s (%v); add it as the owner", trustPath, signer.KeyID(), err)
	}
	return created, nil
}

// issuerPublic reads the public half of the PKCS#8 issuer key.
func issuerPublic(keyPath string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("issuer key is not PEM")
	}
	defer clear(block.Bytes)
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("issuer key is not Ed25519")
	}
	pub := append(ed25519.PublicKey(nil), priv.Public().(ed25519.PublicKey)...)
	clear(priv)
	return pub, nil
}

type remoteFlags struct {
	fs                            *flag.FlagSet
	project, profile, session, me *string
	lead, leads                   *string
	jsonOut                       *bool
}

func newRemoteFlags(name string) remoteFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, remoteUsage) }
	return remoteFlags{fs: fs,
		project: fs.String("project", "", "project/team-home directory"),
		profile: fs.String("profile", "", "team profile"),
		session: fs.String("session", "", "exact workstream/session"),
		me:      fs.String("me", "", "handle (default: resolved runtime handle)"),
		lead:    fs.String("lead", "", "the squad lead the Buzz agent attaches to (until brief 4-3)"),
		leads:   fs.String("leads", "", "comma-separated leads a phone message may name"),
		jsonOut: fs.Bool("json", false, "emit JSON"),
	}
}

func (f remoteFlags) target(sel contextResolution) (buzz.Target, error) {
	var leads []string
	for _, l := range strings.Split(*f.leads, ",") {
		if l = strings.TrimSpace(l); l != "" {
			leads = append(leads, l)
		}
	}
	return buzz.Resolve(sel.Root, *f.lead, leads)
}

func remoteStatusDoc(sel contextResolution, version string) func() ([]byte, error) {
	return func() ([]byte, error) {
		return json.Marshal(buildStatusContract(sel.Root, sel.ProjectDir, version, statusNow()))
	}
}

type remoteStatusData struct {
	Presence       buzz.Presence `json:"presence"`
	Share          string        `json:"share,omitempty"`
	Grants         int           `json:"grants"`
	GrantsExpire   string        `json:"grants_expire,omitempty"`
	ServiceWarning string        `json:"service_warning,omitempty"`
	Warnings       []string      `json:"warnings"`
}

func runRemoteStatus(args []string, version string, out io.Writer) error {
	f := newRemoteFlags("remote status")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	sel, err := resolveGateRaiseContext(*f.project, *f.profile, *f.session, *f.me, f.fs)
	if err != nil {
		return err
	}
	t, err := f.target(sel)
	if err != nil {
		return usageErrorf("remote status: %v", err)
	}
	data := remoteStatusData{Warnings: []string{}}
	raw, err := remoteStatusDoc(sel, version)()
	if err == nil {
		data.Presence, err = buzz.FromStatus(raw, t.Handle)
	}
	if err != nil {
		data.Warnings = append(data.Warnings, "status: "+err.Error())
	}
	if b, _, err := gateverify.LoadBinding(gateBindingPath()); err != nil {
		data.Warnings = append(data.Warnings, err.Error())
	} else {
		root, session := buzz.ShareFor(b)
		if sh, err := buzz.LoadShare(root, session); err != nil {
			data.Warnings = append(data.Warnings, err.Error())
		} else {
			data.Share, data.Grants, data.GrantsExpire = root+" "+session, sh.GrantCount(), sh.Expires().Format(time.RFC3339)
			if time.Until(sh.Expires()) < 72*time.Hour {
				data.Warnings = append(data.Warnings, "the share's grants expire "+data.GrantsExpire+"; renew with the owner script")
			}
		}
	}
	data.ServiceWarning = buzz.ServiceWarning(context.Background(), remoteService, "")
	if *f.jsonOut {
		return writeJSONEnvelope(out, "remote_status", data)
	}
	fmt.Fprintln(out, data.Presence.Activity)
	if data.Share != "" {
		fmt.Fprintf(out, "share %s: %d grants, expire %s\n", data.Share, data.Grants, data.GrantsExpire)
	}
	for _, w := range append(data.Warnings, data.ServiceWarning) {
		if w != "" {
			fmt.Fprintln(out, "warning: "+w)
		}
	}
	return nil
}

func runRemoteAttach(args []string, version string, out io.Writer) error {
	f := newRemoteFlags("remote attach")
	once := f.fs.Bool("once", false, "one tick, then stop the endpoint and exit")
	interval := f.fs.Duration("interval", 5*time.Second, "tick interval")
	noEndpoint := f.fs.Bool("no-endpoint", false, "do not run the share's amq-remote endpoint (no presence)")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	sel, err := resolveGateRaiseContext(*f.project, *f.profile, *f.session, *f.me, f.fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(sel)
	t, err := f.target(sel)
	if err != nil {
		return usageErrorf("remote attach: %v", err)
	}
	b, _, err := gateverify.LoadBinding(gateBindingPath())
	if err != nil {
		return fmt.Errorf("remote attach: %w", err)
	}
	shareRoot, _ := buzz.ShareFor(b)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var sup *buzz.Supervisor
	if !*noEndpoint {
		sup = &buzz.Supervisor{Endpoint: remoteEndpoint(shareRoot)}
		defer func() { _ = sup.Endpoint.Stop() }()
	}
	statusDoc := remoteStatusDoc(sel, version)
	enc := json.NewEncoder(out)
	emit := func(kind string, v any) {
		if *f.jsonOut {
			_ = enc.Encode(map[string]any{"kind": kind, "at": time.Now().UTC().Format(time.RFC3339), "data": v})
			return
		}
		raw, _ := json.Marshal(v)
		fmt.Fprintf(out, "%s %s\n", kind, raw)
	}
	emit("attached", map[string]any{"mailbox": t.Handle, "root": t.Root, "leads": t.Leads, "operator": buzz.AttachToOperator, "share_root": shareRoot})
	var pump *buzz.Pump
	for {
		if sup != nil {
			raw, err := statusDoc()
			var p buzz.Presence
			if err == nil {
				p, err = buzz.FromStatus(raw, t.Handle)
			}
			if err != nil {
				emit("error", map[string]string{"step": "status", "error": err.Error()})
			} else if act, err := sup.Apply(p); act != "" || err != nil {
				emit("presence", map[string]any{"action": act, "present": p.Present, "state": p.State, "error": errString(err)})
			}
		}
		if pump == nil {
			core, err := gateCore(ctx, sel, true)
			if err != nil {
				emit("error", map[string]string{"step": "connect", "error": err.Error()})
			} else {
				pump = &buzz.Pump{Core: core, Mail: remoteMailer, Target: t, Status: statusDoc, Marks: buzz.MarksDir(shareRoot, core.Pin.Channel)}
			}
		}
		if pump != nil {
			handled, err := pump.Step(ctx)
			for _, h := range handled {
				emit("handled", h)
			}
			if err != nil && ctx.Err() == nil {
				emit("error", map[string]string{"step": "dm", "error": err.Error()})
				if c, ok := pump.Core.Relay.(*gateverify.WSRelay); ok {
					c.Close()
				}
				pump = nil // reconnect next tick
			}
		}
		if *once {
			return nil
		}
		select {
		case <-ctx.Done():
			emit("detached", map[string]string{"mailbox": t.Handle})
			return nil
		case <-time.After(*interval):
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
