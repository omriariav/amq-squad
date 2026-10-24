package registry

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/agentcatalog"
)

// ShipN is the R12 pass count before a local entry ships (amendment 10).
const ShipN = 5

// ProbeRecord is the R12 probe tally for one local model.
type ProbeRecord struct {
	Runs, Passes, FalseSuccesses, ToolErrors int
	Predicate, Version                       string
}

// Latency is one direct TTFT measurement in milliseconds.
type Latency struct{ TextMS, ReasoningMS int }

// BuildOptions carries what a probe run adds to detection. Fields left nil
// keep the previous registry's values, so a plain detection run rewrites the
// file with the same probe results and latencies (acceptance 2).
type BuildOptions struct {
	Catalog     agentcatalog.Catalog
	CodexProbes map[string]CodexVerdict // by model id, from ProbeCodexModel
	Probes      map[string]ProbeRecord  // by pi model id ("ollama/qwen3.6:27b")
	Latency     map[string]Latency      // by pi model id
}

// Concurrency caps (plan 8: from the load test, not from guesses). S6 ran six
// Claude Sonnet-medium seats with zero usage-limit events; the other Claude
// models share that pool, so their cap is the same number, extrapolated. The
// Codex column of S6 was not run. Ollama runs one llama-server slot (S5).
const (
	claudeSubscriptionCap = 6
	vendorUnmeasuredCap   = 4
	ollamaGPUCap          = 1
)

var claudeCost = map[string]string{"haiku": "low", "sonnet": "medium", "opus": "high", "fable": "high"}
var codexCost = map[string]string{"gpt-5.6-sol": "high", "gpt-5.6-terra": "medium"}

// workClasses maps a capability tier to the plan 6.1 classes an entry may take.
func workClasses(tier string) []string {
	switch tier {
	case agentcatalog.TierFast:
		return []string{"extract", "summarize", "script"}
	case agentcatalog.TierBalanced:
		return []string{"extract", "script", "implement"}
	case agentcatalog.TierFrontier:
		return []string{"implement", "plan", "review"}
	}
	return nil
}

// Build turns one detection into a registry. prev supplies probe results and
// latencies the options do not replace.
func Build(det Detection, prev Registry, opts BuildOptions) Registry {
	r := Registry{Schema: Schema, Host: det.Host, Tools: append([]Tool(nil), det.Tools...)}
	cat := opts.Catalog.Effective()
	claude := det.Tool("claude")
	for _, m := range cat.Entries("claude", agentcatalog.Models) {
		for _, eff := range cat.Entries("claude", agentcatalog.Efforts) {
			e := vendorEntry("claude", "anthropic", m, eff.Value)
			e.CostTier = claudeCost[m.Value]
			if claude.LoginSource == "env" {
				e.QuotaPool = "anthropic-api"
				if m.Value == "opus" {
					e.QuotaPool = "anthropic-api-opus" // Opus 5.5 has its own API bucket (plan 5)
				}
				e.ConcurrencyCap, e.ConcurrencySource = vendorUnmeasuredCap, SourceUnmeasured
			} else {
				e.QuotaPool = "claude-subscription"
				e.ConcurrencyCap, e.ConcurrencySource = claudeSubscriptionCap, SourceExtrapolated
				if m.Value == "sonnet" && eff.Value == "medium" {
					e.ConcurrencySource = SourceMeasured
				}
			}
			e.ConcurrencyPool = e.QuotaPool
			setVendorState(&e, claude)
			r.Entries = append(r.Entries, e)
		}
	}
	codex := det.Tool("codex")
	for _, m := range cat.Entries("codex", agentcatalog.Models) {
		probes := codexProbes(prev, opts)
		verdict := CheckCodexModel(codex.Version, m.Value, probes)
		probe, probed := probes[m.Value]
		probed = probed && probe.Known && probe.Version == codex.Version
		for _, eff := range cat.Entries("codex", agentcatalog.Efforts) {
			e := vendorEntry("codex", "openai", m, eff.Value)
			e.CostTier = codexCost[m.Value]
			e.QuotaPool = "codex-chatgpt"
			if codex.LoginSource == "env" {
				e.QuotaPool = "openai-api"
			}
			e.ConcurrencyPool = e.QuotaPool
			e.ConcurrencyCap, e.ConcurrencySource = vendorUnmeasuredCap, SourceUnmeasured
			setVendorState(&e, codex)
			if e.State == StateReady && codex.Found {
				e.ProbeVersion = codex.Version
				if probed {
					e.ProbeRuns, e.ProbePredicate = 1, "codex-model-turn"
					if probe.Usable {
						e.ProbePasses = 1
					}
				}
				if !verdict.Usable {
					e.Verified = false
					e.State, e.Reason = StateRefused, verdict.Reason
					if !verdict.Known {
						e.State = StateUnprobed
					}
				}
			}
			r.Entries = append(r.Entries, e)
		}
	}
	r.Entries = append(r.Entries, localEntries(det, prev, opts)...)
	r.Sort(ToolOrder)
	return r
}

func vendorEntry(runtime, provider string, m agentcatalog.Entry, effort string) Entry {
	tier := m.CapabilityTier
	return Entry{
		Runtime: runtime, Provider: provider, Model: m.Value, Effort: effort,
		PrivacyClass: "vendor", CapabilityTier: tier, CostIndex: m.CostIndex, LatencyIndex: m.LatencyIndex,
		Strengths: m.Strengths, WorkClasses: firstNonEmpty(m.WorkClasses, workClasses(tier)),
		UntrustedInput: tier == agentcatalog.TierFast,
	}
}

func setVendorState(e *Entry, t Tool) {
	switch {
	case !t.Found:
		e.State, e.Reason = StateMissingTool, t.Name+" is not on PATH"
	case t.Login == "logged_out":
		e.State, e.Reason = StateLoggedOut, t.Name+" has no login and no API key"
	default:
		e.State, e.Verified = StateReady, true
	}
}

func codexProbes(prev Registry, opts BuildOptions) map[string]CodexVerdict {
	if opts.CodexProbes != nil {
		return opts.CodexProbes
	}
	out := map[string]CodexVerdict{}
	for _, e := range prev.Entries {
		if e.Runtime != "codex" || e.ProbeVersion == "" || e.ProbeRuns == 0 {
			continue
		}
		out[e.Model] = CodexVerdict{Model: e.Model, Version: e.ProbeVersion, Known: true,
			Usable: e.ProbePasses == e.ProbeRuns, Reason: e.Reason}
	}
	return out
}

// localEntries are the pi + Ollama entries: one per local model and effort,
// all in one GPU concurrency pool.
func localEntries(det Detection, prev Registry, opts BuildOptions) []Entry {
	pi := det.Tool("pi")
	var out []Entry
	for _, m := range det.Ollama {
		id := "ollama/" + m.Name
		rec, hasRec := opts.Probes[id]
		lat, hasLat := opts.Latency[id]
		for _, eff := range []string{"low", "medium", "high"} {
			e := Entry{
				Runtime: "pi", Provider: "ollama", Model: id, Effort: eff, PrivacyClass: "local",
				CostTier: "free", QuotaPool: "local", ConcurrencyPool: "ollama-gpu",
				ConcurrencyCap: ollamaGPUCap, ConcurrencySource: SourceMeasured,
				CapabilityTier: agentcatalog.TierFast, UntrustedInput: true,
			}
			if p, ok := prev.Lookup("pi", id, eff); ok {
				e.ProbeRuns, e.ProbePasses, e.ProbeFalseSuccess = p.ProbeRuns, p.ProbePasses, p.ProbeFalseSuccess
				e.ProbeToolErrors, e.ProbePredicate, e.ProbeVersion = p.ProbeToolErrors, p.ProbePredicate, p.ProbeVersion
				e.LatencyMS, e.LatencyReasoningMS = p.LatencyMS, p.LatencyReasoningMS
			}
			if hasRec {
				e.ProbeRuns, e.ProbePasses, e.ProbeFalseSuccess = rec.Runs, rec.Passes, rec.FalseSuccesses
				e.ProbeToolErrors, e.ProbePredicate, e.ProbeVersion = rec.ToolErrors, rec.Predicate, rec.Version
			}
			if hasLat {
				e.LatencyMS, e.LatencyReasoningMS = lat.TextMS, lat.ReasoningMS
			}
			setLocalState(&e, m, pi)
			out = append(out, e)
		}
	}
	return out
}

// setLocalState applies R12: verified only after N of N passes with zero
// false successes, N = ShipN.
func setLocalState(e *Entry, m OllamaModel, pi Tool) {
	e.WorkClasses = []string{"extract", "summarize", "script", "implement"}
	switch {
	case m.CapsKnown && !m.Tools:
		e.State, e.Reason = StateNoToolCalls, "Ollama refuses tools for this model; never implement or script work"
		e.WorkClasses = []string{"extract", "summarize"}
		return
	case !pi.Found:
		e.State, e.Reason = StateMissingTool, "the built-in pi runtime is not installed"
	case e.ProbeRuns == 0:
		e.State, e.Reason = StateUnprobed, "no R12 probe yet; run 'amq-squad doctor --probe'"
	case e.ProbeFalseSuccess > 0:
		e.State = StateProbeFailed
		e.Reason = fmt.Sprintf("R12 probe: %d false success(es) in %d runs", e.ProbeFalseSuccess, e.ProbeRuns)
	case e.ProbePasses < e.ProbeRuns:
		e.State = StateProbeFailed
		e.Reason = fmt.Sprintf("R12 probe: %d of %d passes, unverified", e.ProbePasses, e.ProbeRuns)
	case e.ProbeRuns < ShipN:
		e.State = StateUnprobed
		e.Reason = fmt.Sprintf("R12 probe: %d of %d passes; %d needed before the entry ships", e.ProbePasses, e.ProbeRuns, ShipN)
	default:
		e.State, e.Verified, e.Reason = StateReady, true, ""
	}
	// Probation: an unverified local entry that has a probe record, or a
	// verified one whose runs carried tool errors, is never first in a chain.
	e.Probation = e.ProbeRuns > 0 && (!e.Verified || e.ProbeToolErrors > 0)
}

func firstNonEmpty(a, b []string) []string {
	if len(a) > 0 {
		return a
	}
	return b
}

// CheckLaunch refuses a seat on an entry that is not verified, before the
// launch (R11, amendment 17). codexVersion is the codex-cli that will run.
func CheckLaunch(r Registry, runtime, model, effort, codexVersion string) error {
	e, ok := r.Lookup(runtime, model, effort)
	if !ok {
		return fmt.Errorf("no registry entry %s/%s/%s", runtime, model, effort)
	}
	if !e.Verified {
		return fmt.Errorf("registry entry %s is %s: %s", e.ID(), e.State, strings.TrimSpace(e.Reason))
	}
	if runtime == "codex" && e.ProbeVersion != "" && codexVersion != "" && e.ProbeVersion != codexVersion {
		return fmt.Errorf("registry entry %s was checked against codex-cli %s, but %s will run; re-run 'amq-squad doctor --write-registry'", e.ID(), e.ProbeVersion, codexVersion)
	}
	return nil
}
