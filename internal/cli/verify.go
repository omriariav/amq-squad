package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	verifyMergeStateSuccess = "success"
	verifyMergeStateClean   = "clean"
)

type verifyMergeEvidence struct {
	Subject    string                 `json:"subject"`
	HeadSHA    string                 `json:"head_sha"`
	CI         verifyMergeCheck       `json:"ci"`
	Review     verifyMergeCheck       `json:"review"`
	Exceptions []verifyMergeException `json:"exceptions"`
}

type verifyMergeCheck struct {
	State     string `json:"state"`
	SHA       string `json:"sha"`
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	URL       string `json:"url,omitempty"`
}

type verifyMergeException struct {
	Name     string `json:"name"`
	Approved bool   `json:"approved"`
	Gate     string `json:"gate,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type verifyMergeResult struct {
	OK              bool                        `json:"ok"`
	Subject         string                      `json:"subject,omitempty"`
	HeadSHA         string                      `json:"head_sha,omitempty"`
	EvidenceSummary *verifyMergeEvidenceSummary `json:"evidence_summary,omitempty"`
	Failures        []verifyMergeFailure        `json:"failures,omitempty"`
}

type verifyMergeFailure struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type verifyMergeEvidenceSummary struct {
	CI         verifyMergeCheckSummary       `json:"ci"`
	Review     verifyMergeCheckSummary       `json:"review"`
	Exceptions []verifyMergeExceptionSummary `json:"exceptions,omitempty"`
}

type verifyMergeCheckSummary struct {
	State     string `json:"state"`
	SHA       string `json:"sha"`
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	URL       string `json:"url,omitempty"`
}

type verifyMergeExceptionSummary struct {
	Name     string `json:"name"`
	Approved bool   `json:"approved"`
	Gate     string `json:"gate,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func runVerify(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad verify - deterministic preflight checks

Usage:
  amq-squad verify merge --evidence <file|-> [--json]

The merge preflight validates normalized lead-supplied evidence. It does not
query providers, infer PR state, merge, or mutate remote state. Failed evidence
prints the failed conditions and exits non-zero.
`)
		if len(args) == 0 {
			return usageErrorf("verify requires a subcommand (merge)")
		}
		return nil
	}
	switch args[0] {
	case "merge":
		return runVerifyMerge(args[1:])
	default:
		return usageErrorf("unknown 'verify' subcommand: %q. Try merge.", args[0])
	}
}

func runVerifyMerge(args []string) error {
	fs := flag.NewFlagSet("verify merge", flag.ContinueOnError)
	evidencePath := fs.String("evidence", "", "path to normalized merge evidence JSON, or '-' for stdin (required)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad verify merge - validate merge-readiness evidence

Usage:
  amq-squad verify merge --evidence <file|->
  amq-squad verify merge --evidence <file|-> --json

Evidence schema:
  {
    "subject": "PR or change identifier",
    "head_sha": "current change head SHA",
    "ci": {"state": "success", "sha": "same SHA", "source": "...", "checked_at": "..."},
    "review": {"state": "clean", "sha": "same SHA", "source": "...", "checked_at": "..."},
    "exceptions": [{"name": "...", "approved": true, "gate": "gate/topic"}]
  }

Failed evidence prints machine-readable failures under --json and exits non-zero.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	path := strings.TrimSpace(*evidencePath)
	if path == "" {
		return usageErrorf("--evidence is required")
	}
	evidence, err := readVerifyMergeEvidence(path, os.Stdin)
	if err != nil {
		return err
	}
	result := validateVerifyMergeEvidence(evidence)
	if *jsonOut {
		if err := printJSONEnvelope("verify_merge", result); err != nil {
			return err
		}
		if !result.OK {
			return UsageError("merge preflight failed")
		}
		return nil
	}
	if result.OK {
		fmt.Printf("merge preflight passed for %s at %s\n", displaySubject(evidence), strings.TrimSpace(evidence.HeadSHA))
		return nil
	}
	fmt.Printf("merge preflight failed for %s at %s\n", displaySubject(evidence), strings.TrimSpace(evidence.HeadSHA))
	for _, f := range result.Failures {
		fmt.Printf("- %s: %s\n", f.Code, f.Detail)
	}
	return UsageError("merge preflight failed")
}

func readVerifyMergeEvidence(path string, stdin io.Reader) (verifyMergeEvidence, error) {
	var r io.Reader
	if path == "-" {
		r = stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return verifyMergeEvidence{}, fmt.Errorf("read evidence: %w", err)
		}
		defer f.Close()
		r = f
	}
	var evidence verifyMergeEvidence
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&evidence); err != nil {
		return verifyMergeEvidence{}, fmt.Errorf("decode evidence: %w", err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return verifyMergeEvidence{}, fmt.Errorf("decode evidence: multiple JSON values")
	}
	return evidence, nil
}

func validateVerifyMergeEvidence(e verifyMergeEvidence) verifyMergeResult {
	result := verifyMergeResult{
		Subject: strings.TrimSpace(e.Subject),
		HeadSHA: strings.TrimSpace(e.HeadSHA),
	}
	if result.HeadSHA == "" {
		result.Failures = append(result.Failures, verifyMergeFailure{
			Code:   "missing_head_sha",
			Detail: "head_sha is required",
		})
	}
	result.Failures = append(result.Failures, validateVerifyMergeCheck("ci", e.CI, result.HeadSHA, verifyMergeStateSuccess)...)
	result.Failures = append(result.Failures, validateVerifyMergeCheck("review", e.Review, result.HeadSHA, verifyMergeStateClean)...)
	for i, ex := range e.Exceptions {
		name := strings.TrimSpace(ex.Name)
		switch {
		case name == "":
			result.Failures = append(result.Failures, verifyMergeFailure{
				Code:   "unnamed_exception",
				Detail: fmt.Sprintf("exceptions[%d] has no name", i),
			})
		case !ex.Approved:
			result.Failures = append(result.Failures, verifyMergeFailure{
				Code:   "unapproved_exception",
				Detail: fmt.Sprintf("exception %q is not approved", name),
			})
		}
	}
	result.OK = len(result.Failures) == 0
	if result.OK {
		summary := summarizeVerifyMergeEvidence(e)
		result.EvidenceSummary = &summary
	}
	return result
}

func summarizeVerifyMergeEvidence(e verifyMergeEvidence) verifyMergeEvidenceSummary {
	summary := verifyMergeEvidenceSummary{
		CI:     summarizeVerifyMergeCheck(e.CI),
		Review: summarizeVerifyMergeCheck(e.Review),
	}
	for _, ex := range e.Exceptions {
		summary.Exceptions = append(summary.Exceptions, verifyMergeExceptionSummary{
			Name:     strings.TrimSpace(ex.Name),
			Approved: ex.Approved,
			Gate:     strings.TrimSpace(ex.Gate),
			Reason:   strings.TrimSpace(ex.Reason),
		})
	}
	return summary
}

func summarizeVerifyMergeCheck(check verifyMergeCheck) verifyMergeCheckSummary {
	return verifyMergeCheckSummary{
		State:     strings.TrimSpace(check.State),
		SHA:       strings.TrimSpace(check.SHA),
		Source:    strings.TrimSpace(check.Source),
		CheckedAt: strings.TrimSpace(check.CheckedAt),
		URL:       strings.TrimSpace(check.URL),
	}
}

func validateVerifyMergeCheck(field string, check verifyMergeCheck, headSHA string, wantState string) []verifyMergeFailure {
	var failures []verifyMergeFailure
	state := strings.TrimSpace(check.State)
	sha := strings.TrimSpace(check.SHA)
	if state == "" {
		failures = append(failures, verifyMergeFailure{
			Code:   field + "_missing_state",
			Detail: field + ".state is required",
		})
	} else if state != wantState {
		failures = append(failures, verifyMergeFailure{
			Code:   field + "_state_not_" + wantState,
			Detail: fmt.Sprintf("%s.state is %q, want %q", field, state, wantState),
		})
	}
	if sha == "" {
		failures = append(failures, verifyMergeFailure{
			Code:   field + "_missing_sha",
			Detail: field + ".sha is required",
		})
	} else if headSHA != "" && sha != headSHA {
		failures = append(failures, verifyMergeFailure{
			Code:   field + "_stale_sha",
			Detail: fmt.Sprintf("%s.sha is %q, want head_sha %q", field, sha, headSHA),
		})
	}
	return failures
}

func displaySubject(e verifyMergeEvidence) string {
	if s := strings.TrimSpace(e.Subject); s != "" {
		return s
	}
	return "change"
}
