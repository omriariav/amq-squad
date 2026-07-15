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
	Base       string                 `json:"base,omitempty"`
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
  amq-squad verify action --project DIR --session S --gate TOPIC --action KIND --target TARGET [--json]
  amq-squad verify authorization --file FILE --action KIND --target TARGET --trust-store FILE [--json]
  amq-squad verify merge --evidence <file|-> [--json]
  amq-squad verify release --evidence <file|-> [--json]

The action guard validates a resolved operator gate for high-risk actions
(default/protected branch push, tags, GitHub releases, external sends). The
merge preflight validates normalized per-PR evidence (CI + review at a head
SHA). The release preflight validates a final-release-commit co-sign gate: an
exact-SHA developer co-sign AND an operator release approval before publish.
None of these commands queries providers, infers state, merges, pushes, tags,
releases, or mutates remote state. 'APPROVED to release' alone never authorizes
publish: push/tag/release require bound authorization evidence, and remain
operator-performed. Failed evidence prints the failed conditions and exits
non-zero.
`)
		if len(args) == 0 {
			return usageErrorf("verify requires a subcommand (action, authorization, merge, or release)")
		}
		return nil
	}
	switch args[0] {
	case "action":
		return runVerifyAction(args[1:])
	case "authorization":
		return runVerifyAuthorization(args[1:])
	case "merge":
		return runVerifyMerge(args[1:])
	case "release":
		return runVerifyRelease(args[1:])
	default:
		return usageErrorf("unknown 'verify' subcommand: %q. Try action, authorization, merge, or release.", args[0])
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

const verifyReleaseStateApproved = "approved"

// verifyReleaseEvidence is the normalized evidence for the #285 final-release
// co-sign gate. It is a DIFFERENT artifact from merge evidence: it binds release
// CI, a developer co-sign, and an operator release approval to the exact final
// assembled release commit SHA before publish.
type verifyReleaseEvidence struct {
	Subject          string                        `json:"subject"`
	Version          string                        `json:"version"`
	ReleaseCommitSHA string                        `json:"release_commit_sha"`
	CI               verifyMergeCheck              `json:"ci"`
	Cosign           verifyReleaseCosign           `json:"cosign"`
	OperatorApproval verifyReleaseOperatorApproval `json:"operator_release_approval"`
}

type verifyReleaseCosign struct {
	State string `json:"state"`
	SHA   string `json:"sha"`
	// Reviewer is the developer co-signing the final release commit; must be a
	// SECOND actor, not the release-lead.
	Reviewer string `json:"reviewer"`
	// DistinctFromReleaseLead asserts the co-signer is a different actor than the
	// release-lead. amq-squad cannot infer this from git alone, so the evidence
	// must assert it and the gate validates the assertion.
	DistinctFromReleaseLead bool   `json:"distinct_from_release_lead"`
	Source                  string `json:"source"`
	CheckedAt               string `json:"checked_at"`
	URL                     string `json:"url,omitempty"`
}

type verifyReleaseOperatorApproval struct {
	Approved  bool   `json:"approved"`
	Gate      string `json:"gate"`
	Source    string `json:"source"`
	CheckedAt string `json:"checked_at"`
	Reason    string `json:"reason,omitempty"`
}

type verifyReleaseResult struct {
	OK               bool                          `json:"ok"`
	Subject          string                        `json:"subject,omitempty"`
	Version          string                        `json:"version,omitempty"`
	ReleaseCommitSHA string                        `json:"release_commit_sha,omitempty"`
	EvidenceSummary  *verifyReleaseEvidenceSummary `json:"evidence_summary,omitempty"`
	Failures         []verifyMergeFailure          `json:"failures,omitempty"`
}

type verifyReleaseEvidenceSummary struct {
	CI               verifyMergeCheckSummary       `json:"ci"`
	Cosign           verifyReleaseCosign           `json:"cosign"`
	OperatorApproval verifyReleaseOperatorApproval `json:"operator_release_approval"`
}

func runVerifyRelease(args []string) error {
	fs := flag.NewFlagSet("verify release", flag.ContinueOnError)
	evidencePath := fs.String("evidence", "", "path to normalized release evidence JSON, or '-' for stdin (required)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad verify release - validate the final-release-commit co-sign gate (#285)

Usage:
  amq-squad verify release --evidence <file|->
  amq-squad verify release --evidence <file|-> --json

Evidence schema:
  {
    "subject": "release vX.Y.Z",
    "version": "vX.Y.Z",
    "release_commit_sha": "<final assembled release commit SHA>",
    "ci": {"state": "success", "sha": "<release_commit_sha>", "source": "...", "checked_at": "..."},
    "cosign": {"state": "approved", "sha": "<release_commit_sha>", "reviewer": "<dev, not release-lead>", "distinct_from_release_lead": true, "source": "...", "checked_at": "..."},
    "operator_release_approval": {"approved": true, "gate": "gate/<topic>", "source": "...", "checked_at": "..."}
  }

Publish-ready requires BOTH an exact-SHA developer co-sign and an approved
operator release gate; neither substitutes for the other. The command never
pushes, tags, or releases. Failed evidence prints the failed conditions and
exits non-zero.
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
	evidence, err := readVerifyReleaseEvidence(path, os.Stdin)
	if err != nil {
		return err
	}
	result := validateVerifyReleaseEvidence(evidence)
	if *jsonOut {
		if err := printJSONEnvelope("verify_release", result); err != nil {
			return err
		}
		if !result.OK {
			return UsageError("release preflight failed")
		}
		return nil
	}
	subject := strings.TrimSpace(evidence.Subject)
	if subject == "" {
		subject = "release"
	}
	if result.OK {
		fmt.Printf("release preflight passed for %s at %s\n", subject, strings.TrimSpace(evidence.ReleaseCommitSHA))
		return nil
	}
	fmt.Printf("release preflight failed for %s at %s\n", subject, strings.TrimSpace(evidence.ReleaseCommitSHA))
	for _, f := range result.Failures {
		fmt.Printf("- %s: %s\n", f.Code, f.Detail)
	}
	return UsageError("release preflight failed")
}

func readVerifyReleaseEvidence(path string, stdin io.Reader) (verifyReleaseEvidence, error) {
	var r io.Reader
	if path == "-" {
		r = stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return verifyReleaseEvidence{}, fmt.Errorf("read evidence: %w", err)
		}
		defer f.Close()
		r = f
	}
	var evidence verifyReleaseEvidence
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&evidence); err != nil {
		return verifyReleaseEvidence{}, fmt.Errorf("decode evidence: %w", err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return verifyReleaseEvidence{}, fmt.Errorf("decode evidence: multiple JSON values")
	}
	return evidence, nil
}

func validateVerifyReleaseEvidence(e verifyReleaseEvidence) verifyReleaseResult {
	result := verifyReleaseResult{
		Subject:          strings.TrimSpace(e.Subject),
		Version:          strings.TrimSpace(e.Version),
		ReleaseCommitSHA: strings.TrimSpace(e.ReleaseCommitSHA),
	}
	sha := result.ReleaseCommitSHA
	if sha == "" {
		result.Failures = append(result.Failures, verifyMergeFailure{
			Code:   "missing_release_commit_sha",
			Detail: "release_commit_sha is required",
		})
	}
	// Release CI must be green and bound to the exact final release commit.
	result.Failures = append(result.Failures, validateVerifyMergeCheck("ci", e.CI, sha, verifyMergeStateSuccess)...)

	// Developer co-sign on the exact final release commit (a SECOND actor).
	cs := e.Cosign
	state := strings.TrimSpace(cs.State)
	switch {
	case state == "":
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "cosign_missing_state", Detail: "cosign.state is required"})
	case state != verifyReleaseStateApproved:
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "cosign_state_not_approved", Detail: fmt.Sprintf("cosign.state is %q, want %q", state, verifyReleaseStateApproved)})
	}
	csSHA := strings.TrimSpace(cs.SHA)
	if csSHA == "" {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "cosign_missing_sha", Detail: "cosign.sha is required"})
	} else if sha != "" && csSHA != sha {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "cosign_stale_sha", Detail: fmt.Sprintf("cosign.sha is %q, want release_commit_sha %q (the co-sign must be on the exact final release commit)", csSHA, sha)})
	}
	if strings.TrimSpace(cs.Reviewer) == "" {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "cosign_missing_reviewer", Detail: "cosign.reviewer is required (the developer co-signing the final release commit)"})
	}
	if !cs.DistinctFromReleaseLead {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "cosign_not_distinct_actor", Detail: "cosign.distinct_from_release_lead must be true: the co-signer must be a second actor, not the release-lead"})
	}

	// Operator release approval is a SEPARATE required gate; it never substitutes
	// for the co-sign, and the co-sign never substitutes for it.
	op := e.OperatorApproval
	if !op.Approved {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "operator_approval_not_approved", Detail: "operator_release_approval.approved must be true"})
	}
	if strings.TrimSpace(op.Gate) == "" && strings.TrimSpace(op.Source) == "" {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "operator_approval_missing_reference", Detail: "operator_release_approval requires a gate or source reference"})
	}

	result.OK = len(result.Failures) == 0
	if result.OK {
		result.EvidenceSummary = &verifyReleaseEvidenceSummary{
			CI:               summarizeVerifyMergeCheck(e.CI),
			Cosign:           cs,
			OperatorApproval: op,
		}
	}
	return result
}
