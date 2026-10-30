package registry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleRegistry() Registry {
	return Registry{
		Schema: Schema,
		Host:   Host{RAMGB: 64, CPU: "Apple M5 Max", GPU: "Apple M5 Max (unified memory)"},
		Tools: []Tool{
			{Name: "claude", Found: true, Path: "/opt/bin/claude", Version: "2.1.307", VerifiedVersion: "2.1.233", VersionCheck: "newer", Login: "logged_in", LoginSource: "keychain"},
			{Name: "ollama", Found: false},
		},
		Entries: []Entry{
			{Runtime: "claude", Provider: "anthropic", Model: "sonnet", Effort: "medium", Verified: true, State: StateReady,
				CostTier: "medium", PrivacyClass: "vendor", QuotaPool: "claude-subscription", ConcurrencyPool: "claude-subscription",
				ConcurrencyCap: 6, ConcurrencySource: SourceMeasured, CapabilityTier: "balanced", CostIndex: 0.5,
				Strengths: []string{"implementation"}, WorkClasses: []string{"implement", "script"}},
			{Runtime: "pi", Provider: "ollama", Model: "ollama/qwen3.6:27b", Effort: "low", State: StateUnprobed,
				Reason: "no R12 probe yet: run doctor --probe", PrivacyClass: "local", ConcurrencyPool: "ollama-gpu",
				ConcurrencyCap: 1, Probation: true, LatencyMS: 6827, LatencyReasoningMS: 106, ProbeRuns: 5, ProbePasses: 4,
				ProbePredicate: ProbePredicate, ProbeVersion: "pi 1.3.0"},
		},
	}
}

func TestFormatParseRoundTrip(t *testing.T) {
	r := sampleRegistry()
	text := Format(r)
	got, err := Parse(text)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, text)
	}
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("round trip differs:\n got %+v\nwant %+v\n%s", got, r, text)
	}
	if Format(got) != text {
		t.Fatal("format is not stable across a round trip")
	}
}

func TestParseRejectsUnknownKeyAndSchema(t *testing.T) {
	text := Format(sampleRegistry())
	bad := strings.Replace(text, "    probation:", "    probaton:", 1)
	if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "probaton") {
		t.Fatalf("misspelt key: err = %v", err)
	}
	if _, err := Parse(strings.Replace(text, "schema: 1", "schema: 2", 1)); err == nil {
		t.Fatal("schema 2 accepted")
	}
	if _, err := Parse("tools: []\n"); err == nil {
		t.Fatal("file without schema accepted")
	}
}

func TestWriteIsAtomicPrivateAndIdempotent(t *testing.T) {
	home := t.TempDir()
	path := Path(home)
	if _, err := Read(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: err = %v", err)
	}
	r := sampleRegistry()
	if err := Write(path, r); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := Write(path, r); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("two writes of one registry differ")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 && filepath.Separator == '/' {
		t.Fatalf("mode %o, want 0600", st.Mode().Perm())
	}
	got, err := Read(path)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("read back: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".tmp-registry.yaml-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestProposableIsVerifiedOnly(t *testing.T) {
	r := sampleRegistry()
	p := r.Proposable()
	if len(p) != 1 || p[0].ID() != "claude/sonnet/medium" {
		t.Fatalf("proposable = %+v", p)
	}
	if _, ok := r.Lookup("pi", "ollama/qwen3.6:27b", "low"); !ok {
		t.Fatal("lookup failed")
	}
}

func TestSortOrder(t *testing.T) {
	r := Registry{Entries: []Entry{
		{Runtime: "pi", Model: "a", Effort: "low"},
		{Runtime: "claude", Model: "opus", Effort: "max"},
		{Runtime: "claude", Model: "opus", Effort: "low"},
	}, Tools: []Tool{{Name: "gh"}, {Name: "claude"}}}
	r.Sort([]string{"claude", "gh"})
	var ids []string
	for _, e := range r.Entries {
		ids = append(ids, e.ID())
	}
	if strings.Join(ids, " ") != "claude/opus/low claude/opus/max pi/a/low" || r.Tools[0].Name != "claude" {
		t.Fatalf("order %v %v", ids, r.Tools)
	}
}
