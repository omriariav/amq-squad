package state

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMessageRefsArePreservedExactly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "cto", "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---json
{"schema":1,"id":"terminal","from":"cto","to":["user"],"thread":"gate/x","subject":"CLOSED: x","created":"2026-07-15T00:00:00Z","kind":"status","refs":[" request ","request","request"]}
---
done
`
	if err := os.WriteFile(filepath.Join(dir, "terminal.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, warnings := ScanSessionMessages(root, time.Now)
	if len(warnings) != 0 || len(msgs) != 1 {
		t.Fatalf("msgs=%d warnings=%v", len(msgs), warnings)
	}
	want := []string{" request ", "request", "request"}
	if !msgs[0].RefsPresent || !msgs[0].RefsValid || !reflect.DeepEqual(msgs[0].Refs, want) || msgs[0].RefsRaw != `[" request ","request","request"]` {
		t.Fatalf("refs facts=%#v/%t/%t raw=%q want %#v", msgs[0].Refs, msgs[0].RefsPresent, msgs[0].RefsValid, msgs[0].RefsRaw, want)
	}
}

func TestMessageRefsPresenceDistinguishesMissingEmptyAndNull(t *testing.T) {
	for _, tc := range []struct {
		name       string
		field      string
		present    bool
		valid      bool
		wantNonNil bool
		wantRaw    string
	}{
		{name: "missing", valid: true},
		{name: "empty", field: `,"refs":[]`, present: true, valid: true, wantNonNil: true, wantRaw: "[]"},
		{name: "null", field: `,"refs":null`, present: true, valid: false, wantRaw: "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "message.md")
			raw := `---json
{"schema":1,"id":"terminal","from":"cto","to":["user"],"thread":"gate/x","subject":"CLOSED: x","created":"2026-07-15T00:00:00Z","kind":"status"` + tc.field + `}
---
done
`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			msg, ok, err := parseMessageFile(path, "user", MailboxNew, time.Now)
			if err != nil || !ok {
				t.Fatalf("parse ok=%t err=%v", ok, err)
			}
			if msg.RefsPresent != tc.present || msg.RefsValid != tc.valid || (msg.Refs != nil) != tc.wantNonNil || len(msg.Refs) != 0 || msg.RefsRaw != tc.wantRaw {
				t.Fatalf("refs=%#v present=%t valid=%t raw=%q", msg.Refs, msg.RefsPresent, msg.RefsValid, msg.RefsRaw)
			}
		})
	}
}

func TestMessageRecipientRawEvidencePreservesMutationSafetyFacts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		field      string
		present    bool
		arrayValid bool
		wantRawTo  []string
		wantClean  []string
		wantRaw    string
	}{
		{name: "canonical single user", field: `,"to":["user"]`, present: true, arrayValid: true, wantRawTo: []string{"user"}, wantClean: []string{"user"}, wantRaw: `["user"]`},
		{name: "qa then user", field: `,"to":["qa","user"]`, present: true, arrayValid: true, wantRawTo: []string{"qa", "user"}, wantClean: []string{"qa", "user"}, wantRaw: `["qa","user"]`},
		{name: "user then qa", field: `,"to":["user","qa"]`, present: true, arrayValid: true, wantRawTo: []string{"user", "qa"}, wantClean: []string{"user", "qa"}, wantRaw: `["user","qa"]`},
		{name: "duplicate", field: `,"to":["user","user"]`, present: true, arrayValid: true, wantRawTo: []string{"user", "user"}, wantClean: []string{"user", "user"}, wantRaw: `["user","user"]`},
		{name: "empty", field: `,"to":[]`, present: true, arrayValid: true, wantRawTo: []string{}, wantClean: []string{}, wantRaw: `[]`},
		{name: "trim repaired", field: `,"to":[" user "]`, present: true, arrayValid: true, wantRawTo: []string{" user "}, wantClean: []string{"user"}, wantRaw: `[" user "]`},
		{name: "bare string tolerated read only", field: `,"to":"user"`, present: true, wantClean: []string{"user"}, wantRaw: `"user"`},
		{name: "missing", wantClean: []string{}},
		{name: "null", field: `,"to":null`, present: true, wantClean: []string{}, wantRaw: `null`},
		{name: "malformed array", field: `,"to":["user",1]`, present: true, wantClean: []string{}, wantRaw: `["user",1]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "message.md")
			raw := `---json
{"schema":1,"id":"request","from":"cto","thread":"gate/x","subject":"APPROVAL: x","created":"2026-07-15T00:00:00Z","kind":"question"` + tc.field + `}
---
approve?
`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			msg, ok, err := parseMessageFile(path, "user", MailboxNew, time.Now)
			if err != nil || !ok {
				t.Fatalf("parse ok=%t err=%v", ok, err)
			}
			if msg.ToPresent != tc.present || msg.ToArrayValid != tc.arrayValid || !reflect.DeepEqual(msg.RawTo, tc.wantRawTo) || !reflect.DeepEqual(msg.To, tc.wantClean) || msg.ToRaw != tc.wantRaw {
				t.Fatalf("to=%#v raw_to=%#v present=%t array_valid=%t raw=%q", msg.To, msg.RawTo, msg.ToPresent, msg.ToArrayValid, msg.ToRaw)
			}
		})
	}
}

func TestMalformedTypedApprovalContextIsRetainedAsBarrierEvidence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "cto", "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---json
{"schema":1,"id":"a1","from":"user","to":["cto"],"thread":"gate/x","subject":"APPROVED: x","created":"2026-07-11T00:00:00Z","kind":"answer","context":{"approval":{"schema_version":1,"source":"human","self_approved":false,"unknown":true}}}
---
Action: protected_branch_push
Target: x
`
	if err := os.WriteFile(filepath.Join(dir, "a1.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, warnings := ScanSessionMessages(root, time.Now)
	if len(warnings) != 0 || len(msgs) != 1 {
		t.Fatalf("msgs=%d warnings=%v", len(msgs), warnings)
	}
	msg := msgs[0]
	if !msg.ApprovalPresent || msg.ApprovalValid || msg.Approval != nil || msg.ApprovalError == "" {
		t.Fatalf("malformed approval was discarded/trusted: %+v", msg)
	}
	if msg.Context == nil {
		t.Fatal("raw context was discarded")
	}
}

func TestTypedAuthorizationRequestIsStrictAndBoundToMessageThread(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "user", "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	validContext := `{"schema_version":1,"taxonomy_version":1,"gate":"gate/merge-414","thread":"gate/merge-414","namespace":{"project_dir":"/repo","profile":"default","session":"stage-a","namespace_id":"ns-1","generation":"gen-1"},"gate_kind":"merge","action":"protected_branch_push","target":"PR #414 head abcdef0 into main","note":"reviewed"}`
	write := func(name, thread, context string) {
		t.Helper()
		raw := `---json
{"schema":1,"id":"` + name + `","from":"cto","to":["user"],"thread":"` + thread + `","subject":"APPROVAL: merge","created":"2026-07-11T00:00:00Z","kind":"question","context":{"authorization_request":` + context + `}}
---
Approve merge.
`
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("valid", "gate/merge-414", validContext)
	write("unknown", "gate/merge-414", strings.TrimSuffix(validContext, "}")+`,"unknown":true}`)
	write("wrong-thread", "gate/other", validContext)

	msgs, warnings := ScanSessionMessages(root, time.Now)
	if len(warnings) != 0 || len(msgs) != 3 {
		t.Fatalf("msgs=%d warnings=%v", len(msgs), warnings)
	}
	byID := make(map[string]Message, len(msgs))
	for _, msg := range msgs {
		byID[msg.ID] = msg
	}
	valid := byID["valid"]
	if !valid.AuthorizationRequestPresent || !valid.AuthorizationRequestValid || valid.AuthorizationRequest == nil || valid.AuthorizationRequestError != "" {
		t.Fatalf("valid request was not retained: %+v", valid)
	}
	for _, id := range []string{"unknown", "wrong-thread"} {
		msg := byID[id]
		if !msg.AuthorizationRequestPresent || msg.AuthorizationRequestValid || msg.AuthorizationRequest != nil || msg.AuthorizationRequestError == "" || msg.Context == nil {
			t.Fatalf("malformed request %q was discarded or trusted: %+v", id, msg)
		}
	}
}

func TestParsedMessagePreservesAuthorityExactSubjectAndBody(t *testing.T) {
	for _, tc := range []struct {
		name, subject, body, wantRawBody string
	}{
		{name: "leading spaces", subject: " APPROVED: x", body: " Gate-Kind: tag\nAction: tag", wantRawBody: " Gate-Kind: tag\nAction: tag"},
		{name: "trailing spaces", subject: "APPROVED: x ", body: "Gate-Kind: tag\nAction: tag ", wantRawBody: "Gate-Kind: tag\nAction: tag "},
		{name: "extra blank line", subject: "APPROVED: x", body: "Gate-Kind: tag\nAction: tag\n", wantRawBody: "Gate-Kind: tag\nAction: tag\n"},
		{name: "crlf framing", subject: "APPROVED: x", body: "Gate-Kind: tag\nAction: tag", wantRawBody: "Gate-Kind: tag\nAction: tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "m.md")
			raw := "---json\n{\"schema\":1,\"id\":\"a\",\"from\":\"user\",\"to\":[\"cto\"],\"thread\":\"gate/x\",\"subject\":" + strconv.Quote(tc.subject) + ",\"created\":\"2026-07-11T00:00:00Z\",\"kind\":\"answer\"}\n---\n" + tc.body + "\n"
			if tc.name == "crlf framing" {
				raw = strings.ReplaceAll(raw, "\n", "\r\n")
			}
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			msg, ok, err := parseMessageFile(path, "cto", MailboxNew, time.Now)
			if err != nil || !ok {
				t.Fatalf("parse ok=%t err=%v", ok, err)
			}
			if !msg.AuthorityRaw || msg.RawSubject != tc.subject || msg.Subject != strings.TrimSpace(tc.subject) || msg.RawBody != tc.wantRawBody || msg.Body != strings.TrimSpace(tc.wantRawBody) {
				t.Fatalf("authority/display fields=%+v", msg)
			}
		})
	}
}

func TestRawFrontmatterRejectsAmbiguousAuthorityBeforeMapDecode(t *testing.T) {
	base := `{"schema":1,"id":"m","from":"cto","to":["user"],"thread":"gate/x","subject":"APPROVAL: x","created":"2026-07-11T00:00:00Z","kind":"question","context":%s}`
	cases := map[string][]byte{
		"authorization nested duplicate": []byte(fmt.Sprintf(base, `{"authorization_request":{"namespace":{"generation":"one","generation":"two"}}}`)),
		"release marker duplicate":       []byte(fmt.Sprintf(base, `{"release_child":{"role":"tag","role":"github_release"}}`)),
		"receipt duplicate":              []byte(fmt.Sprintf(base, `{"release_child":{"receipt":{"attempt_id":"one","attempt_id":"two"}}}`)),
		"invalid utf8":                   append([]byte(fmt.Sprintf(base, `{"release_child":{"role":"`)), append([]byte{0xff}, []byte(`"}}`)...)...),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "m.md")
			raw := append([]byte("---json\n"), header...)
			raw = append(raw, []byte("\n---\nbody\n")...)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := parseMessageFile(path, "user", MailboxNew, time.Now); err == nil || ok || !strings.Contains(err.Error(), "ambiguous frontmatter json") {
				t.Fatalf("parse ok=%t err=%v", ok, err)
			}
		})
	}
}

func TestRawFrontmatterKeepsLenientKnownHeaderCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.md")
	raw := `---json
{"schema":1,"id":"m","from":"cto","to":"user","thread":"gate/x","subject":"APPROVAL: x","created":"2026-07-11T00:00:00Z","kind":"question","future_field":{"kept":"compatible"}}
---
body
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, ok, err := parseMessageFile(path, "user", MailboxNew, time.Now)
	if err != nil || !ok || len(msg.To) != 1 || msg.To[0] != "user" {
		t.Fatalf("lenient parse=(%+v,%t,%v)", msg, ok, err)
	}
}
