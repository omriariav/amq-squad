// Package registry is the 3.0 runtime registry of plan section 5 (R9 to
// R12): ~/.amq-squad/registry.yaml holds one entry per runtime x model x
// effort, written from a probe of the installed tools and models. The lead
// proposes seats from verified entries only (R11); routing (2b-4), the pi
// package (4-1) and the operator session (4-3) read the file through Read.
//
// The entry type is the shared contract of brief 2b-3. Fields:
//
//	runtime            claude | codex | pi | grok | cursor-agent
//	provider           anthropic | openai | ollama | xai | cursor
//	model, effort      the provider arguments (pi: "ollama/qwen3.6:27b")
//	verified           the lead may propose a seat on this entry
//	state, reason      why an entry is or is not verified (State* below)
//	cost_tier          free | low | medium | high
//	privacy_class      local | vendor
//	quota_pool         the usage window the entry draws on (API and
//	                   subscription are separate pools; Opus has its own API bucket)
//	concurrency_pool   entries that share one cap (all Ollama entries share the GPU)
//	concurrency_cap    seats at once in that pool; concurrency_source says
//	                   whether the number is measured, extrapolated or unmeasured
//	untrusted_input    the entry is a cheap reader: untrusted input forces
//	                   read-only tools and schema-only output (plan 14)
//	probation          never first in a chain (plan 6.2)
//	latency_ms         direct text TTFT (amendment 10); latency_reasoning_ms
//	                   is the first reasoning token for thinking models
//	capability_tier, cost_index, latency_index, strengths, work_classes
//	                   the routing metadata cut from catalog.json (2a-2 ruling 13)
//	probe_*            the R12 probe record: runs, passes, false successes,
//	                   tool errors, predicate, and the version probed against
package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Schema is the registry.yaml schema version.
const Schema = 1

// Entry states. Only StateReady entries are verified.
const (
	StateReady       = "ready"         // usable; verified
	StateMissingTool = "missing_tool"  // the runtime binary is not on PATH
	StateLoggedOut   = "logged_out"    // the runtime has no login and no API key
	StateUnprobed    = "unprobed"      // a local entry with no passing R12 probe yet
	StateProbeFailed = "probe_failed"  // the R12 probe did not pass N of N
	StateNoToolCalls = "no_tool_calls" // the model refuses tools; never implement or script work
	StateRefused     = "refused"       // the pinned runtime refuses the model id (amendment 17)
	StateNeedsProbe  = "needs_probe"   // a detected runtime with no entry probe (Grok Build, Cursor)
)

// Concurrency sources (plan 8: caps come from the load test, not guesses).
const (
	SourceMeasured     = "measured"
	SourceExtrapolated = "extrapolated"
	SourceUnmeasured   = "unmeasured"
)

// Entry is one runtime x model x effort.
type Entry struct {
	Runtime            string
	Provider           string
	Model              string
	Effort             string
	Verified           bool
	State              string
	Reason             string
	CostTier           string
	PrivacyClass       string
	QuotaPool          string
	ConcurrencyPool    string
	ConcurrencyCap     int
	ConcurrencySource  string
	UntrustedInput     bool
	Probation          bool
	LatencyMS          int
	LatencyReasoningMS int
	CapabilityTier     string
	CostIndex          float64
	LatencyIndex       float64
	Strengths          []string
	WorkClasses        []string
	ProbeRuns          int
	ProbePasses        int
	ProbeFalseSuccess  int
	ProbeToolErrors    int
	ProbePredicate     string
	ProbeVersion       string
}

// ID is runtime/model/effort, the key routing chains name.
func (e Entry) ID() string { return e.Runtime + "/" + e.Model + "/" + e.Effort }

// Tool is one detected executable (plan 5: a missing tool is one status line).
type Tool struct {
	Name            string
	Found           bool
	Path            string
	Version         string
	VerifiedVersion string // the version the AMQ launch API verified, "" when it verifies none
	VersionCheck    string // match | newer | older | unverified | unknown
	Login           string // logged_in | logged_out | unknown | n/a
	LoginSource     string // keychain | file | env | ""
}

// Host is the RAM and GPU line of the probe.
type Host struct {
	RAMGB int
	CPU   string
	GPU   string
}

// Registry is the whole file.
type Registry struct {
	Schema  int
	Host    Host
	Tools   []Tool
	Entries []Entry
}

// Path is ~/.amq-squad/registry.yaml under home ("" home: the user's home).
func Path(home string) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "registry.yaml")
}

// Lookup returns the entry for runtime, model and effort.
func (r Registry) Lookup(runtime, model, effort string) (Entry, bool) {
	for _, e := range r.Entries {
		if e.Runtime == runtime && e.Model == model && e.Effort == effort {
			return e, true
		}
	}
	return Entry{}, false
}

// Proposable is the verified entries, in file order (R11).
func (r Registry) Proposable() []Entry {
	var out []Entry
	for _, e := range r.Entries {
		if e.Verified {
			out = append(out, e)
		}
	}
	return out
}

// Tool returns the detected tool by name.
func (r Registry) Tool(name string) (Tool, bool) {
	for _, t := range r.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// Sort puts tools and entries in the stable file order: tools by the
// detection order given, entries by runtime, model, effort rank.
func (r *Registry) Sort(toolOrder []string) {
	rank := map[string]int{}
	for i, n := range toolOrder {
		rank[n] = i
	}
	sort.SliceStable(r.Tools, func(i, j int) bool { return rank[r.Tools[i].Name] < rank[r.Tools[j].Name] })
	sort.SliceStable(r.Entries, func(i, j int) bool {
		a, b := r.Entries[i], r.Entries[j]
		if a.Runtime != b.Runtime {
			return runtimeRank(a.Runtime) < runtimeRank(b.Runtime)
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return effortRank(a.Effort) < effortRank(b.Effort)
	})
}

var runtimeOrder = []string{"claude", "codex", "pi", "grok", "cursor-agent"}

func runtimeRank(r string) int {
	for i, n := range runtimeOrder {
		if n == r {
			return i
		}
	}
	return len(runtimeOrder)
}

var effortOrder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func effortRank(e string) int {
	for i, n := range effortOrder {
		if n == e {
			return i
		}
	}
	return len(effortOrder)
}

// Read loads a registry file. A missing file is an error the caller can test
// with errors.Is(err, fs.ErrNotExist).
func Read(path string) (Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Registry{}, err
	}
	r, err := Parse(string(raw))
	if err != nil {
		return Registry{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// Write writes the registry atomically (temp file and rename, mode 0600).
// Two writes of the same Registry give the same bytes.
func Write(path string, r Registry) error {
	if path == "" {
		return errors.New("registry: no path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".registry-*.yaml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.WriteString(Format(r)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func normalizeList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
