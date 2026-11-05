package schedproc

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
)

// Brief 3.2.0-3a item 6: the lead's merge-train re-bind is a decision of the
// socket like a widening: proved from the lead's pane and applied by the
// scheduler's Rebind hook; from any other process it is not_lead and nothing
// is applied; a scheduler with no hook refuses it as rebind_unsupported.
func TestRebindDecisionIsProvedLikeAWidening(t *testing.T) {
	lead := panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	serve := func(t *testing.T, fromLead bool, hook func(string, RebindRequest) DecideReply) *DecisionServer {
		t.Helper()
		root, _ := filepath.EvalSymlinks(t.TempDir())
		s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
			Apply: func(string, WidenRequest) DecideReply {
				t.Fatal("a re-bind reached the widening")
				return DecideReply{}
			},
			Rebind:   hook,
			LeadPane: func(string, string) (panechain.Pane, error) { return lead, nil },
			Reaches: func(int, string, []panechain.Pane) (*panechain.Pane, bool) {
				if fromLead {
					return &lead, true
				}
				return nil, true
			},
			StartToken: func(int) (string, bool) { return "s", true },
			Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	req := RebindRequest{PR: 894, From: "68792071d942003613da13562ed2c7cb1dd34529", To: "2555d34aa77084145bd7b3ca58f252c5178ac55a"}
	var applied []RebindRequest
	hook := func(lead string, r RebindRequest) DecideReply {
		applied = append(applied, r)
		return DecideReply{OK: true, Detail: "re-bound by " + lead}
	}
	s := serve(t, true, hook)
	reply, err := SendRebind(s.Root(), s.Path(), req)
	if err != nil || !reply.OK || reply.Detail != "re-bound by lead" || len(applied) != 1 || applied[0].Kind != RebindRequestKind || applied[0].PR != 894 || applied[0].To != req.To {
		t.Fatalf("the lead's re-bind: %v %+v (applied %+v)", err, reply, applied)
	}
	s.Close()
	applied = nil
	s = serve(t, false, hook)
	if reply, err = SendRebind(s.Root(), s.Path(), req); err != nil || reply.OK || reply.Code != RefusedNotLead || len(applied) != 0 {
		t.Fatalf("a seat's re-bind: %v %+v (applied %d)", err, reply, len(applied))
	}
	s.Close()
	s = serve(t, true, nil)
	if reply, err = SendRebind(s.Root(), s.Path(), req); err != nil || reply.OK || reply.Code != RefusedRebindUnsupported {
		t.Fatalf("no hook: %v %+v", err, reply)
	}
}
