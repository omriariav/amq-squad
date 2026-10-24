package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The two v2 redaction rules, moved from commandevidence/evidence.go: a
// secret-looking key and a secret-looking argument; token counts stay.
func TestRedactKeysAndArguments(t *testing.T) {
	got := string(Redact([]byte(`{"payload":{"api_key":"k1","input_tokens":42,"tool_input":{"command":"curl --token abc https://x && login password=hunter2 ok"}},"GH_TOKEN":"t"}`)))
	for _, leak := range []string{"k1", "abc", "hunter2", `"t"`} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted line keeps %s: %s", leak, got)
		}
	}
	if !strings.Contains(got, `"input_tokens":42`) || !strings.Contains(got, "curl --token [redacted] https://x && login password=[redacted] ok") {
		t.Fatalf("redaction: %s", got)
	}
}

// P1-7: argv arrays, quoted values, flag=value, bearer credentials and
// numeric secrets; only numeric count fields stay.
func TestRedactArgvQuotesAndNumbers(t *testing.T) {
	for _, tc := range []struct{ in, leak, keep string }{
		{`{"argv":["gh","--token","abc123","pr","list"]}`, "abc123", `"pr"`},
		{`{"argv":["curl","--api-key=k9","x"]}`, "k9", `"--api-key=[redacted]"`},
		{`{"command":"deploy --password \"two words\" --verbose"}`, "words", "--verbose"},
		{`{"command":"deploy --access-token 'tail part' next"}`, "tail", "next"},
		{`{"command":"curl -H \"Authorization: Bearer sekret\" https://x"}`, "sekret", "https://x"},
		{`{"command":"curl -H Authorization: Bearer sekret2 https://x"}`, "sekret2", "https://x"},
		{`{"pin_secret":4242,"password":1234,"output_tokens":17}`, "4242", `"output_tokens":17`},
		{`{"password":1234}`, "1234", `"password":"[redacted]"`},
	} {
		got := string(Redact([]byte(tc.in)))
		if strings.Contains(got, tc.leak) || !strings.Contains(got, tc.keep) {
			t.Errorf("%s -> %s", tc.in, got)
		}
	}
}

// fix 2, P1-7 and N1: URL userinfo, nested sh -c strings, the whitespace
// fallback for a line that does not parse, and the explicit count list.
func TestRedactURLsNestedShellAndCountList(t *testing.T) {
	for _, tc := range []struct{ in, leak, keep string }{
		{`{"command":"git clone https://omri:ghp_secret1@github.com/o/r.git"}`, "ghp_secret1", "github.com/o/r.git"},
		{`{"command":"curl https://user@example.com/x"}`, "user@", "example.com/x"},
		{`{"command":"sh -c 'curl --token abc9 https://x'"}`, "abc9", "https://x"},
		{`{"command":"bash -c \"export API_KEY=k77 && run\""}`, "k77", "run"},
		{`{"command":"zsh -c \"sh -c 'deploy --password p55 now'\""}`, "p55", "now"},
		{`{"command":"deploy --token t88 'unterminated"}`, "t88", "unterminated"},
		{`{"reset_tokens":999,"cache_read_input_tokens":12,"input_tokens":3}`, "999", `"cache_read_input_tokens":12`},
	} {
		got := string(Redact([]byte(tc.in)))
		if strings.Contains(got, tc.leak) || !strings.Contains(got, tc.keep) {
			t.Errorf("%s -> %s", tc.in, got)
		}
	}
}

// The task link (v2 evidenceTaskLink): only the holders' lines inside each
// lease window belong to the task.
func TestForTaskKeepsLeaseWindowsOnly(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	at := func(m int) string { return t0.Add(time.Duration(m) * time.Minute).Format(time.RFC3339) }
	write(t, Path(root, "seat-a"), `{"seq":1,"at":"`+at(-5)+`","handle":"seat-a"}`, `{"seq":2,"at":"`+at(5)+`","handle":"seat-a","payload":{"token":"x"}}`, `{"seq":3,"at":"`+at(25)+`","handle":"seat-a"}`)
	write(t, Path(root, "seat-b"), `{"seq":1,"at":"`+at(15)+`","handle":"seat-b"}`, `{"seq":2,"at":"`+at(40)+`","handle":"seat-b"}`)
	tk := task.Task{ID: "t-1", Attempt: 2,
		Attempts: []task.Attempt{{N: 1, Holder: "seat-a", GrantedAt: t0, EndedAt: t0.Add(10 * time.Minute)}},
		Lease:    &task.Lease{Holder: "seat-b", GrantedAt: t0.Add(12 * time.Minute)}}
	lines, err := ForTask(root, tk, t0.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Holder != "seat-a" || lines[0].Attempt != 1 || lines[1].Holder != "seat-b" || lines[1].Attempt != 2 {
		t.Fatalf("lines %+v", lines)
	}
	if strings.Contains(string(lines[0].Line), `"x"`) {
		t.Fatalf("task line not redacted: %s", lines[0].Line)
	}
}

func TestOutcomesNameTheTask(t *testing.T) {
	root := t.TempDir()
	if rows, err := Outcomes(root, "t-1"); err != nil || rows != nil {
		t.Fatalf("absent log: %v %v", rows, err)
	}
	write(t, OutcomePath(root), `{"task":"t-1","attempt":1,"outcome":"done","secret":"s"}`, `{"task":"t-2"}`, `torn`)
	rows, err := Outcomes(root, "t-1")
	if err != nil || len(rows) != 1 || strings.Contains(string(rows[0]), `"s"`) {
		t.Fatalf("rows %s %v", rows, err)
	}
}
