package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const releasePlanSchemaVersion = 1

type releaseActionPlan struct {
	Step        string `json:"step"`
	GateKind    string `json:"gate_kind"`
	Action      string `json:"action"`
	Target      string `json:"target"`
	RequestBody string `json:"request_body"`
	Command     string `json:"command"`
}

type releaseVerificationStep struct {
	Kind              string `json:"kind"`
	Command           string `json:"command"`
	Ref               string `json:"ref,omitempty"`
	ExpectedType      string `json:"expected_type,omitempty"`
	ExpectedSHA       string `json:"expected_sha,omitempty"`
	ExpectedURL       string `json:"expected_url,omitempty"`
	MustDifferFromSHA string `json:"must_differ_from_sha,omitempty"`
	ExpectedSignature string `json:"expected_signature,omitempty"`
}

type releaseVerificationPlan struct {
	Preflight []releaseVerificationStep `json:"preflight"`
	Local     []releaseVerificationStep `json:"local"`
	Remote    []releaseVerificationStep `json:"remote"`
}

type releaseVerificationObservation struct {
	RemoteURL          string
	RemoteBranchSHA    string
	NotesSHA256        string
	LocalTagType       string
	LocalPeeledSHA     string
	LocalSignature     string
	RemoteTagObjectSHA string
	RemotePeeledSHA    string
}

type releasePlan struct {
	SchemaVersion     int                     `json:"schema_version"`
	PlanID            string                  `json:"plan_id"`
	Project           string                  `json:"project"`
	Repository        string                  `json:"repository"`
	Remote            string                  `json:"remote"`
	RemoteURL         string                  `json:"remote_url"`
	DefaultBranch     string                  `json:"default_branch"`
	ProtectedBranch   bool                    `json:"protected_branch"`
	CandidateHeadSHA  string                  `json:"candidate_head_sha"`
	BranchPushRefspec string                  `json:"branch_push_refspec"`
	Version           string                  `json:"version"`
	Tag               string                  `json:"tag"`
	TagRef            string                  `json:"tag_ref"`
	TagPushRefspec    string                  `json:"tag_push_refspec"`
	Annotation        string                  `json:"annotation"`
	TagPolicy         string                  `json:"tag_policy"`
	SigningPolicy     string                  `json:"signing_policy"`
	WorktreeState     string                  `json:"worktree_state"`
	PreflightState    string                  `json:"preflight_state"`
	PreflightSHA256   string                  `json:"preflight_sha256"`
	ReleaseTitle      string                  `json:"release_title"`
	NotesPolicy       string                  `json:"notes_policy"`
	NotesFile         string                  `json:"notes_file,omitempty"`
	NotesSHA256       string                  `json:"notes_sha256,omitempty"`
	Draft             bool                    `json:"draft"`
	Prerelease        bool                    `json:"prerelease"`
	LatestPolicy      string                  `json:"latest_policy"`
	Ready             bool                    `json:"ready"`
	Failures          []string                `json:"failures,omitempty"`
	Actions           []releaseActionPlan     `json:"actions"`
	Verification      releaseVerificationPlan `json:"verification"`
}

type releasePlanInput struct {
	Project, Repository, Remote, RemoteURL            string
	Branch, Head, Version, Tag                        string
	Annotation, TagPolicy, WorktreeState              string
	PreflightState, PreflightSHA256                   string
	ReleaseTitle, NotesPolicy, NotesFile, NotesSHA256 string
	LatestPolicy                                      string
	ProtectedBranch, Draft, Prerelease                bool
}

func runVerifyReleasePlan(args []string) error {
	fs := flag.NewFlagSet("verify release-plan", flag.ContinueOnError)
	repository := fs.String("repository", "", "canonical OWNER/REPO identity (required)")
	project := fs.String("project", "", "local project/worktree path (required)")
	remote := fs.String("remote", "origin", "exact local git remote name")
	remoteURL := fs.String("remote-url", "", "exact GitHub remote URL for repository identity (required)")
	branch := fs.String("branch", "", "exact default branch (required)")
	protected := fs.Bool("protected-branch", true, "freeze protected-branch taxonomy")
	head := fs.String("head", "", "exact candidate commit SHA (required)")
	version := fs.String("version", "", "canonical release version (required)")
	tag := fs.String("tag", "", "exact tag; defaults to and must equal version")
	annotation := fs.String("annotation", "", "single-line annotated tag message (required)")
	tagPolicy := fs.String("tag-policy", "signed", "signed or annotated")
	worktreeState := fs.String("worktree-state", "", "clean or dirty (required)")
	preflightState := fs.String("preflight-state", "", "passed or failed (required)")
	preflightSHA := fs.String("preflight-sha256", "", "immutable preflight evidence SHA-256 (required)")
	releaseTitle := fs.String("release-title", "", "exact noninteractive release title (required)")
	notesPolicy := fs.String("notes-policy", "", "file or generated (required)")
	notesFile := fs.String("notes-file", "", "notes file when notes-policy=file")
	notesSHA := fs.String("notes-sha256", "", "notes file SHA-256 when notes-policy=file")
	draft := fs.Bool("draft", true, "freeze draft policy")
	prerelease := fs.Bool("prerelease", false, "freeze prerelease policy")
	latestPolicy := fs.String("latest-policy", "false", "true or false")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad verify release-plan - deterministic read-only tag/release plan

All repository, branch, candidate, tag/signing, evidence, title/notes, and
draft/prerelease/latest inputs are explicit. Output contains exact literal-
newline gate bodies, non-force refspecs, and local/remote verification steps.
The command performs no git, gh, network, authorization, or filesystem mutation.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("verify release-plan takes no positional arguments")
	}
	plan, err := buildReleasePlan(releasePlanInput{
		Project: *project, Repository: *repository, Remote: *remote, RemoteURL: *remoteURL, Branch: *branch, ProtectedBranch: *protected,
		Head: *head, Version: *version, Tag: *tag, Annotation: *annotation, TagPolicy: *tagPolicy,
		WorktreeState: *worktreeState, PreflightState: *preflightState, PreflightSHA256: *preflightSHA,
		ReleaseTitle: *releaseTitle, NotesPolicy: *notesPolicy, NotesFile: *notesFile, NotesSHA256: *notesSHA,
		Draft: *draft, Prerelease: *prerelease, LatestPolicy: *latestPolicy,
	})
	if err != nil {
		return usageErrorf("verify release-plan: %v", err)
	}
	if *jsonOut {
		return printJSONEnvelope("verify_release_plan", plan)
	}
	fmt.Printf("release plan %s ready=%t\n", plan.PlanID, plan.Ready)
	for _, failure := range plan.Failures {
		fmt.Printf("- %s\n", failure)
	}
	for _, step := range plan.Verification.Preflight {
		fmt.Printf("\nverify preflight %s: %s\n", step.Kind, step.Command)
	}
	for _, action := range plan.Actions {
		fmt.Printf("\n%s\n%s\ncommand: %s\n", action.Step, action.RequestBody, action.Command)
	}
	for _, step := range plan.Verification.Local {
		fmt.Printf("\nverify local %s: %s\n", step.Kind, step.Command)
	}
	for _, step := range plan.Verification.Remote {
		fmt.Printf("\nverify remote %s: %s\n", step.Kind, step.Command)
	}
	return nil
}

func buildReleasePlan(in releasePlanInput) (releasePlan, error) {
	fields := map[string]*string{
		"project": &in.Project, "repository": &in.Repository, "remote": &in.Remote, "remote_url": &in.RemoteURL, "branch": &in.Branch, "head": &in.Head,
		"version": &in.Version, "tag": &in.Tag, "annotation": &in.Annotation, "tag_policy": &in.TagPolicy,
		"worktree_state": &in.WorktreeState, "preflight_state": &in.PreflightState, "preflight_sha256": &in.PreflightSHA256,
		"release_title": &in.ReleaseTitle, "notes_policy": &in.NotesPolicy, "notes_file": &in.NotesFile,
		"notes_sha256": &in.NotesSHA256, "latest_policy": &in.LatestPolicy,
	}
	for name, value := range fields {
		if *value != strings.TrimSpace(*value) || strings.ContainsAny(*value, "\r\n\x00") {
			return releasePlan{}, fmt.Errorf("%s must be trim-canonical and single-line", name)
		}
	}
	if in.Tag == "" {
		in.Tag = in.Version
	}
	if in.Project == "" {
		return releasePlan{}, fmt.Errorf("project is required")
	}
	canonicalProject, err := canonicalDir(in.Project)
	if err != nil {
		return releasePlan{}, fmt.Errorf("project must resolve to a canonical existing directory: %w", err)
	}
	in.Project = canonicalProject
	if !validGitHubRepository(in.Repository) {
		return releasePlan{}, fmt.Errorf("repository must be canonical OWNER/REPO without .git")
	}
	remoteRepository, ok := gitHubRemoteRepository(in.RemoteURL)
	if !ok || remoteRepository != in.Repository {
		return releasePlan{}, fmt.Errorf("remote-url must be a canonical GitHub URL for exact repository %s", in.Repository)
	}
	if !validReleaseRefLeaf(in.Remote) || !validReleaseBranch(in.Branch) {
		return releasePlan{}, fmt.Errorf("remote and branch must be canonical git ref components")
	}
	if !isExactGitSHA(in.Head) {
		return releasePlan{}, fmt.Errorf("head must be one exact lowercase 40- or 64-hex commit SHA")
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`).MatchString(in.Version) {
		return releasePlan{}, fmt.Errorf("version must be canonical vMAJOR.MINOR.PATCH")
	}
	if in.Tag != in.Version {
		return releasePlan{}, fmt.Errorf("tag %q must exactly match version %q", in.Tag, in.Version)
	}
	if in.Annotation == "" {
		return releasePlan{}, fmt.Errorf("annotation is required")
	}
	tagFlag, signingPolicy := "-a", "forbidden"
	switch in.TagPolicy {
	case "signed":
		tagFlag, signingPolicy = "-s", "required"
	case "annotated":
	default:
		return releasePlan{}, fmt.Errorf("tag-policy must be signed or annotated")
	}
	if in.WorktreeState != "clean" && in.WorktreeState != "dirty" {
		return releasePlan{}, fmt.Errorf("worktree-state must be clean or dirty")
	}
	if in.PreflightState != "passed" && in.PreflightState != "failed" {
		return releasePlan{}, fmt.Errorf("preflight-state must be passed or failed")
	}
	if !isLowerSHA256(in.PreflightSHA256) {
		return releasePlan{}, fmt.Errorf("preflight-sha256 must be 64 lowercase hex characters")
	}
	if in.ReleaseTitle == "" {
		return releasePlan{}, fmt.Errorf("release-title is required")
	}
	if in.LatestPolicy != "true" && in.LatestPolicy != "false" {
		return releasePlan{}, fmt.Errorf("latest-policy must be true or false")
	}
	notesArgs := ""
	switch in.NotesPolicy {
	case "file":
		if in.NotesFile == "" || !isLowerSHA256(in.NotesSHA256) {
			return releasePlan{}, fmt.Errorf("notes-policy=file requires notes-file and 64 lowercase hex notes-sha256")
		}
		notesPath := in.NotesFile
		if !filepath.IsAbs(notesPath) {
			notesPath = filepath.Join(in.Project, notesPath)
		}
		canonicalNotes, err := canonicalReleaseFile(notesPath)
		if err != nil {
			return releasePlan{}, fmt.Errorf("notes-file must resolve to a canonical regular file: %w", err)
		}
		in.NotesFile = canonicalNotes
		notesArgs = " --notes-file " + shellQuote(in.NotesFile)
	case "generated":
		if in.NotesFile != "" || in.NotesSHA256 != "" {
			return releasePlan{}, fmt.Errorf("notes-policy=generated forbids notes-file and notes-sha256")
		}
		notesArgs = " --generate-notes"
	default:
		return releasePlan{}, fmt.Errorf("notes-policy must be file or generated")
	}
	branchRef := "refs/heads/" + in.Branch
	branchRefspec := in.Head + ":" + branchRef
	branchAction := "default_branch_push"
	if in.ProtectedBranch {
		branchAction = "protected_branch_push"
	}
	gitCommand := "git -C " + shellQuote(in.Project)
	branchTarget := fmt.Sprintf("push candidate %s to %s on %s=%s for %s from %s", in.Head, branchRef, in.Remote, in.RemoteURL, in.Repository, in.Project)
	tagRef := "refs/tags/" + in.Tag
	tagRefspec := tagRef + ":" + tagRef
	createTarget := fmt.Sprintf("create %s tag %s at %s in %s worktree %s", in.TagPolicy, in.Tag, in.Head, in.Repository, in.Project)
	pushTarget := fmt.Sprintf("push %s to %s=%s for %s from %s", tagRefspec, in.Remote, in.RemoteURL, in.Repository, in.Project)
	releaseTarget := fmt.Sprintf("create GitHub release %s in %s title=%q notes=%s draft=%t prerelease=%t latest=%s from tag %s at %s", in.Version, in.Repository, in.ReleaseTitle, in.NotesPolicy, in.Draft, in.Prerelease, in.LatestPolicy, in.Tag, in.Head)
	releaseCommand := "gh release create " + shellQuote(in.Tag) + " --repo " + shellQuote(in.Repository) + " --verify-tag --title " + shellQuote(in.ReleaseTitle) + notesArgs + " --latest=" + in.LatestPolicy
	if in.Draft {
		releaseCommand += " --draft"
	}
	if in.Prerelease {
		releaseCommand += " --prerelease"
	}
	plan := releasePlan{
		SchemaVersion: releasePlanSchemaVersion, Project: in.Project, Repository: in.Repository, Remote: in.Remote, RemoteURL: in.RemoteURL, DefaultBranch: in.Branch,
		ProtectedBranch: in.ProtectedBranch, CandidateHeadSHA: in.Head, BranchPushRefspec: branchRefspec,
		Version: in.Version, Tag: in.Tag, TagRef: tagRef, TagPushRefspec: tagRefspec, Annotation: in.Annotation,
		TagPolicy: in.TagPolicy, SigningPolicy: signingPolicy, WorktreeState: in.WorktreeState,
		PreflightState: in.PreflightState, PreflightSHA256: in.PreflightSHA256, ReleaseTitle: in.ReleaseTitle,
		NotesPolicy: in.NotesPolicy, NotesFile: in.NotesFile, NotesSHA256: in.NotesSHA256,
		Draft: in.Draft, Prerelease: in.Prerelease, LatestPolicy: in.LatestPolicy,
		Actions: []releaseActionPlan{
			{Step: "push_candidate_branch", GateKind: "merge", Action: branchAction, Target: branchTarget, RequestBody: "Action: " + branchAction + "\nTarget: " + branchTarget, Command: gitCommand + " push " + shellQuote(in.Remote) + " " + shellQuote(branchRefspec)},
			{Step: "create_tag", GateKind: "tag", Action: "tag", Target: createTarget, RequestBody: "Action: tag\nTarget: " + createTarget, Command: gitCommand + " tag " + tagFlag + " " + shellQuote(in.Tag) + " " + shellQuote(in.Head) + " -m " + shellQuote(in.Annotation)},
			{Step: "push_tag", GateKind: "tag", Action: "tag", Target: pushTarget, RequestBody: "Action: tag\nTarget: " + pushTarget, Command: gitCommand + " push " + shellQuote(in.Remote) + " " + shellQuote(tagRefspec)},
			{Step: "github_release", GateKind: "release", Action: "github_release", Target: releaseTarget, RequestBody: "Action: github_release\nTarget: " + releaseTarget, Command: releaseCommand},
		},
	}
	plan.Verification.Preflight = []releaseVerificationStep{
		{Kind: "remote_identity", Command: gitCommand + " remote get-url " + shellQuote(in.Remote), ExpectedURL: in.RemoteURL},
	}
	if in.NotesPolicy == "file" {
		plan.Verification.Preflight = append(plan.Verification.Preflight, releaseVerificationStep{Kind: "release_notes_sha256", Command: "shasum -a 256 -- " + shellQuote(in.NotesFile), Ref: in.NotesFile, ExpectedSHA: in.NotesSHA256})
	}
	plan.Verification.Local = []releaseVerificationStep{
		{Kind: "local_tag_object", Command: gitCommand + " cat-file -t " + shellQuote(tagRef), Ref: tagRef, ExpectedType: "tag"},
		{Kind: "local_tag_peeled", Command: gitCommand + " rev-parse " + shellQuote(tagRef+"^{}"), Ref: tagRef + "^{}", ExpectedSHA: in.Head},
	}
	if in.TagPolicy == "signed" {
		plan.Verification.Local = append(plan.Verification.Local, releaseVerificationStep{Kind: "local_tag_signature", Command: gitCommand + " tag -v " + shellQuote(in.Tag), Ref: tagRef, ExpectedSignature: "valid"})
	}
	plan.Verification.Remote = []releaseVerificationStep{
		{Kind: "remote_branch", Command: gitCommand + " ls-remote " + shellQuote(in.Remote) + " " + shellQuote(branchRef), Ref: branchRef, ExpectedSHA: in.Head},
		{Kind: "remote_tag_object", Command: gitCommand + " ls-remote --tags " + shellQuote(in.Remote) + " " + shellQuote(tagRef), Ref: tagRef, ExpectedType: "tag_object", MustDifferFromSHA: in.Head},
		{Kind: "remote_tag_peeled", Command: gitCommand + " ls-remote --tags " + shellQuote(in.Remote) + " " + shellQuote(tagRef+"^{}"), Ref: tagRef + "^{}", ExpectedSHA: in.Head},
	}
	if in.WorktreeState != "clean" {
		plan.Failures = append(plan.Failures, "worktree is not clean")
	}
	if in.PreflightState != "passed" {
		plan.Failures = append(plan.Failures, "immutable preflight evidence did not pass")
	}
	plan.Ready = len(plan.Failures) == 0
	identity := plan
	identity.PlanID = ""
	b, _ := json.Marshal(identity)
	sum := sha256.Sum256(b)
	plan.PlanID = "release-plan-" + hex.EncodeToString(sum[:])
	return plan, nil
}

func validReleaseRefLeaf(value string) bool {
	return value != "" && !strings.HasPrefix(value, "-") && !strings.ContainsAny(value, " ~^:?*[\\") && !strings.Contains(value, "..") && !strings.Contains(value, "@{") && !strings.Contains(value, "/")
}

func validGitHubRepository(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.HasSuffix(value, ".git") {
		return false
	}
	validSegment := regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	for _, part := range parts {
		if !validSegment.MatchString(part) || strings.Trim(part, ".") == "" {
			return false
		}
	}
	return true
}

func gitHubRemoteRepository(value string) (string, bool) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)\.git$`),
		regexp.MustCompile(`^git@github\.com:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)\.git$`),
		regexp.MustCompile(`^ssh://git@github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)\.git$`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindStringSubmatch(value); len(match) == 3 {
			repository := match[1] + "/" + match[2]
			return repository, validGitHubRepository(repository)
		}
	}
	return "", false
}

func canonicalReleaseFile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", resolved)
	}
	return filepath.Clean(resolved), nil
}

func validReleaseBranch(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.Contains(value, "//") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, " ~^:?*[\\") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
func isExactGitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func isLowerSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateReleaseVerificationObservation(plan releasePlan, observed releaseVerificationObservation) []string {
	var failures []string
	if observed.RemoteURL != plan.RemoteURL {
		failures = append(failures, "named remote URL does not match frozen repository identity")
	}
	if observed.RemoteBranchSHA != plan.CandidateHeadSHA {
		failures = append(failures, "remote branch SHA does not match candidate")
	}
	if observed.LocalTagType != "tag" {
		failures = append(failures, "local tag is not an annotated tag object")
	}
	if observed.LocalPeeledSHA != plan.CandidateHeadSHA {
		failures = append(failures, "local peeled tag SHA does not match candidate")
	}
	if plan.SigningPolicy == "required" && observed.LocalSignature != "valid" {
		failures = append(failures, "signed tag policy lacks a valid local signature")
	}
	if observed.RemoteTagObjectSHA == "" || observed.RemoteTagObjectSHA == plan.CandidateHeadSHA {
		failures = append(failures, "remote tag object is absent or lightweight")
	}
	if observed.RemotePeeledSHA != plan.CandidateHeadSHA {
		failures = append(failures, "remote peeled tag SHA does not match candidate")
	}
	if plan.NotesPolicy == "file" && observed.NotesSHA256 != plan.NotesSHA256 {
		failures = append(failures, "release notes SHA-256 does not match frozen file evidence")
	}
	return failures
}
