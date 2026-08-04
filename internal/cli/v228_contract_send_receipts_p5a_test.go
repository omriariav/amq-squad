// AC7 behavioral half, re-pointed to the P5a send seam.
//
// The driver moved: runOwnedDurableSend(durableSendOptions, amqCommandRequest)
// -> runOwnedAMQSend(ownedAMQSendOptions, amqCommandRequest). The old symbol
// still exists on this branch's base, and the new one does not, so this file is
// gated behind the v228p5a build tag while the untagged
// v228_contract_send_receipts_test.go keeps the production-deletion pin (which
// references no send symbols and compiles on any base).
//
// P5a INTEGRATION STEP: delete the //go:build line above.
//
// The ASSERTIONS are frozen — they are the contract, not the driver:
//  1. the transport accepted exactly one send, and the accepted result carries a
//     stable MessageID with Invoked=true;
//  2. zero receipt artifacts anywhere under .amq-squad/ (unchanged walk);
//  3. the returned result carries NO receipt-bearing field at all. This is the
//     stronger form of the old "returned struct is nil" check: it is enforced by
//     reflection over the result, so a future field named *Receipt* cannot
//     reintroduce the certification layer by a different name.
package cli

import (
	"reflect"
	"strings"
	"testing"
)

// v228AssertNoReceiptBearingFields fails on any field, at any depth, whose name
// mentions a receipt. Depth matters: a nested struct is exactly how a deleted
// concept comes back wearing a different hat.
func v228AssertNoReceiptBearingFields(t *testing.T, value any) {
	t.Helper()
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			where := path + "." + field.Name
			if strings.Contains(strings.ToLower(field.Name), "receipt") {
				t.Errorf("send result carries a receipt-bearing field %s (%s); v2.28 confirms by transport acceptance alone", where, field.Type)
			}
			if tag := strings.ToLower(field.Tag.Get("json")); strings.Contains(tag, "receipt") {
				t.Errorf("send result carries a receipt-bearing json field %s (tag %q)", where, field.Tag.Get("json"))
			}
			walk(field.Type, where)
		}
	}
	walk(reflect.TypeOf(value), reflect.TypeOf(value).String())
}

func TestV228ContractInterAgentSendLeavesNoReceiptArtifacts(t *testing.T) {
	requireV228Contract(t)
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile   = "v228"
		session   = "ac7"
		messageID = "msg-ac7-stable"
	)
	v228SeedProfile(t, project, profile, session, v228StartMembers(session, "cto", "dev"))
	root := v228CanonicalRoot(project, profile, session)

	// Transport acceptance IS the delivery confirmation under v2.28, so nothing
	// local needs to certify it.
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	var sent [][]string
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		sent = append(sent, req.Arg)
		return []byte(`{"id":"` + messageID + `","status":"delivered"}`), nil
	}

	_, transportResult, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{
		Dir: project,
		Arg: []string{"send", "--root", root, "--from", "cto", "--to", "dev",
			"--thread", "task/ac7", "--kind", "directive", "--subject", "review", "--body", "please review"},
	})
	result := &transportResult
	if err != nil {
		t.Fatalf("inter-agent send refused: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("transport invocations = %d, want exactly one accepted send", len(sent))
	}

	// (1) The accepted result is the observation of external reality: the
	// transport's own message id, and the fact that we really invoked it.
	if result == nil {
		t.Fatal("send returned no result; the accepted transport outcome must be observable")
	} else {
		if !result.Invoked {
			t.Error("send result reports Invoked=false after an accepted transport call")
		}
		if strings.TrimSpace(result.MessageID) != messageID {
			t.Errorf("send result MessageID = %q, want the transport-accepted %q", result.MessageID, messageID)
		}
	}

	// (2) Zero receipt artifacts on disk — unchanged from the pre-P5a form.
	if artifacts := v228ReceiptArtifactPaths(t, project); len(artifacts) > 0 {
		t.Errorf("send produced %d receipt artifact(s) under .amq-squad: %v", len(artifacts), artifacts)
	}

	// (3) No receipt-bearing field anywhere in the result.
	if result != nil {
		v228AssertNoReceiptBearingFields(t, *result)
	}
}
