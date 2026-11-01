package buzz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// Mail is one AMQ message the attachment sends.
type Mail struct {
	Root    string
	From    string
	To      string
	Subject string
	Body    string
	Labels  []string
	Refs    []string
	Thread  string // the AMQ thread ("" for AMQ's default p2p thread)
	Project string // AMQ peer project of the recipient's root ("" for the same root)
	Session string // its session
}

// Mailer sends AMQ messages and returns the message id.
type Mailer interface {
	Send(ctx context.Context, m Mail) (string, error)
}

// AMQMailer sends with the amq CLI (`amq send --json`); the body goes on
// stdin, never in argv.
type AMQMailer struct{ Bin string }

func (a AMQMailer) Send(ctx context.Context, m Mail) (string, error) {
	bin := a.Bin
	if bin == "" {
		bin = "amq"
	}
	args := []string{"send", "--root", m.Root, "--me", m.From, "--to", m.To, "--body", "-", "--json"}
	if m.Subject != "" {
		args = append(args, "--subject", m.Subject)
	}
	if len(m.Labels) > 0 {
		args = append(args, "--labels", strings.Join(m.Labels, ","))
	}
	if m.Project != "" {
		args = append(args, "--project", m.Project, "--session", m.Session)
	}
	if len(m.Refs) > 0 {
		args = append(args, "--refs", strings.Join(m.Refs, ","))
	}
	if m.Thread != "" {
		args = append(args, "--thread", m.Thread)
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Env = amqexec.NoUpdateCheckEnv(nil)
	cmd.Stdin = strings.NewReader(m.Body)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("amq send to %s: %v: %s", m.To, err, strings.TrimSpace(errb.String()))
	}
	var res struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out.Bytes(), &res) != nil || res.ID == "" {
		return "", fmt.Errorf("amq send to %s: no message id in %q", m.To, strings.TrimSpace(out.String()))
	}
	return res.ID, nil
}

// Route is where one phone message goes.
type Route struct {
	Lead string // the named lead, "" when the message names none
	Text string // the text to deliver (the lead prefix removed)
}

// ParseRoute reads a lead name at the start of a message: "@lead text" or
// "lead: text". A name that is not one of the leads is not a route, and the
// whole message goes to the bound mailbox unchanged.
func ParseRoute(content string, leads []string) Route {
	s := strings.TrimSpace(content)
	var name, rest string
	switch {
	case strings.HasPrefix(s, "@"):
		name, rest, _ = strings.Cut(s[1:], " ")
		name = strings.TrimSuffix(name, ":")
	default:
		head, tail, ok := strings.Cut(s, ":")
		if !ok || strings.ContainsAny(head, " \t\n") {
			return Route{Text: s}
		}
		name, rest = head, tail
	}
	name = strings.ToLower(strings.TrimSpace(name))
	rest = strings.TrimSpace(rest)
	for _, l := range leads {
		if l == name && rest != "" {
			return Route{Lead: l, Text: rest}
		}
	}
	return Route{Text: s}
}

// Delivery is what Deliver did with one phone message.
type Delivery struct {
	Mailbox   string `json:"mailbox"`
	MailboxID string `json:"mailbox_message_id"`
	Lead      string `json:"lead,omitempty"`
	LeadID    string `json:"lead_message_id,omitempty"`
	// LeadHandle is the forward's AMQ handle (Lead may be squad/handle);
	// Thread the request's cockpit thread both messages went on.
	LeadHandle string   `json:"lead_handle,omitempty"`
	Thread     string   `json:"thread,omitempty"`
	Refused    string   `json:"refused,omitempty"`   // why a named destination was not forwarded to
	Ambiguous  []string `json:"ambiguous,omitempty"` // the squads a bare lead name matches
}

// Deliver puts one owner message into the bound mailbox as a message from
// the carrier with the nostr:<event id> label (the same shape as AMQ's
// mailbox binding, amq-acp README "Binding mode"), then, when it names a lead
// other than the mailbox, forwards it to that lead through AMQ with a ref to
// the mailbox copy. The label is a pointer, not proof: no consumer may treat
// it as the owner's word without fetching and verifying the event (R32).
// Both bodies pass redact.Block: everything written to a seat or a mailbox
// is redacted, the owner's own signed text included (ruling 18; 5-amq-B fix
// round 2).
func Deliver(ctx context.Context, m Mailer, t Target, eventID, content string) (Delivery, error) {
	return DeliverOn(ctx, m, t, eventID, content, "", nil)
}

// DeliverOn is Deliver on an AMQ thread, the request's cockpit thread
// (brief 31-4). cont, when set, is the request a continuation belongs to:
// its text goes the way the request's question went (cont.Lead, "" for the
// mailbox only), whatever lead the text names.
func DeliverOn(ctx context.Context, m Mailer, t Target, eventID, content, thread string, cont *Route) (Delivery, error) {
	if m == nil {
		return Delivery{}, errors.New("no mailer")
	}
	r := ParseRoute(content, t.Leads)
	if cont != nil {
		r = Route{Lead: cont.Lead, Text: strings.TrimSpace(content)}
	}
	labels := []string{"buzz", "nostr:" + eventID}
	d := Delivery{Mailbox: t.Handle, Thread: thread}
	subject := "Buzz: message from the owner"
	if r.Lead != "" && r.Lead != t.Handle {
		subject = "Buzz: message from the owner for " + r.Lead
	}
	id, err := m.Send(ctx, Mail{Root: t.Root, From: CarrierHandle, To: t.Handle, Subject: subject, Body: redact.Block(strings.TrimSpace(content)), Labels: labels, Thread: thread})
	if err != nil {
		return d, err
	}
	d.MailboxID = id
	if r.Lead == "" || r.Lead == t.Handle {
		return d, nil
	}
	// the destination must be a lead by the authoritative seat roles, not
	// only a handle the attachment was started with
	if t.Registry == nil {
		d.Refused = r.Lead + ": no lead registry; not forwarded"
		return d, nil
	}
	dest, err := t.Registry.Lead(t.Root, r.Lead)
	if err != nil {
		d.Refused = err.Error()
		var amb *AmbiguousLeadError
		if errors.As(err, &amb) {
			d.Ambiguous = amb.Squads
		}
		return d, nil
	}
	// a lead in another squad's root is reached by AMQ cross-project routing
	// from the mailbox's root (fleet.RelayArgv's route); the ref names the
	// mailbox copy only when both are in one root
	fwd := Mail{Root: t.Root, From: CarrierHandle, To: dest.Handle, Project: dest.Project, Session: dest.Session,
		Subject: "Buzz: from the owner, routed by " + t.Handle, Body: redact.Block(r.Text), Labels: append(labels, "routed"), Thread: thread}
	if dest.Project == "" {
		fwd.Refs = []string{id}
	}
	lid, err := m.Send(ctx, fwd)
	if err != nil {
		return d, err
	}
	d.Lead, d.LeadID, d.LeadHandle = r.Lead, lid, dest.Handle
	if dest.Squad != "" && !strings.Contains(r.Lead, "/") {
		d.Lead = dest.Squad + "/" + r.Lead
	}
	return d, nil
}
