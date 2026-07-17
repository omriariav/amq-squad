package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validReleasePlanInput(t *testing.T) releasePlanInput {
	t.Helper()
	project := t.TempDir()
	notesFile := filepath.Join(project, "release-notes.md")
	if err := os.WriteFile(notesFile, []byte("release notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return releasePlanInput{
		Project: project, Repository: "omriariav/amq-squad", Remote: "origin", RemoteURL: "https://github.com/omriariav/amq-squad.git", Branch: "main", ProtectedBranch: true,
		Head: strings.Repeat("a", 40), Version: "v2.22.0", Tag: "v2.22.0", Annotation: "amq-squad v2.22.0",
		TagPolicy: "signed", WorktreeState: "clean", PreflightState: "passed", PreflightSHA256: strings.Repeat("b", 64),
		ReleaseTitle: "amq-squad v2.22.0", NotesPolicy: "file", NotesFile: notesFile, NotesSHA256: strings.Repeat("c", 64),
		Draft: true, Prerelease: false, LatestPolicy: "false",
	}
}

func TestReleasePlanFreezesBranchTagReleaseAndVerification(t *testing.T) {
	in := validReleasePlanInput(t)
	plan, err := buildReleasePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := buildReleasePlan(in)
	if err != nil || again.PlanID != plan.PlanID {
		t.Fatalf("plan id must be deterministic: %s %s %v", plan.PlanID, again.PlanID, err)
	}
	if !plan.Ready || plan.BranchPushRefspec != in.Head+":refs/heads/main" || plan.TagPushRefspec != "refs/tags/v2.22.0:refs/tags/v2.22.0" {
		t.Fatalf("refspec plan = %+v", plan)
	}
	if len(plan.Actions) != 4 || plan.Actions[0].Action != "protected_branch_push" || plan.Actions[0].GateKind != "merge" {
		t.Fatalf("branch action = %+v", plan.Actions)
	}
	for _, action := range plan.Actions {
		want := "Action: " + action.Action + "\nTarget: " + action.Target
		if action.RequestBody != want || strings.Contains(action.RequestBody, `\n`) {
			t.Fatalf("literal-newline body got=%q want=%q", action.RequestBody, want)
		}
		if strings.HasPrefix(action.Command, "git ") && !strings.HasPrefix(action.Command, "git -C "+shellQuote(plan.Project)+" ") {
			t.Fatalf("git action is not worktree-bound: %s", action.Command)
		}
	}
	release := plan.Actions[3]
	for _, want := range []string{"--repo omriariav/amq-squad", "--title 'amq-squad v2.22.0'", "--notes-file " + shellQuote(plan.NotesFile), "--latest=false", "--draft"} {
		if !strings.Contains(release.Command, want) {
			t.Fatalf("noninteractive release command missing %q: %s", want, release.Command)
		}
	}
	if len(plan.Verification.Preflight) != 2 || plan.Verification.Preflight[0].ExpectedURL != in.RemoteURL || plan.Verification.Preflight[1].ExpectedSHA != in.NotesSHA256 {
		t.Fatalf("preflight verification = %+v", plan.Verification.Preflight)
	}
	if len(plan.Verification.Local) != 3 || plan.Verification.Local[0].ExpectedType != "tag" || plan.Verification.Local[1].ExpectedSHA != in.Head || plan.Verification.Local[2].ExpectedSignature != "valid" {
		t.Fatalf("local verification = %+v", plan.Verification.Local)
	}
	steps := append([]releaseVerificationStep(nil), plan.Verification.Preflight...)
	steps = append(steps, plan.Verification.Local...)
	steps = append(steps, plan.Verification.Remote...)
	for _, step := range steps {
		if strings.HasPrefix(step.Command, "git ") && !strings.HasPrefix(step.Command, "git -C "+shellQuote(plan.Project)+" ") {
			t.Fatalf("git verification is not worktree-bound: %s", step.Command)
		}
	}
	if len(plan.Verification.Remote) != 3 || plan.Verification.Remote[0].ExpectedSHA != in.Head || plan.Verification.Remote[1].MustDifferFromSHA != in.Head || plan.Verification.Remote[2].ExpectedSHA != in.Head {
		t.Fatalf("remote verification = %+v", plan.Verification.Remote)
	}
}

func TestReleasePlanProtectedAndDefaultTaxonomy(t *testing.T) {
	protected := validReleasePlanInput(t)
	p, err := buildReleasePlan(protected)
	if err != nil {
		t.Fatal(err)
	}
	plain := protected
	plain.ProtectedBranch = false
	d, err := buildReleasePlan(plain)
	if err != nil {
		t.Fatal(err)
	}
	if p.Actions[0].Action != "protected_branch_push" || d.Actions[0].Action != "default_branch_push" || p.PlanID == d.PlanID {
		t.Fatalf("taxonomy not frozen: protected=%+v default=%+v", p.Actions[0], d.Actions[0])
	}
}

func TestReleasePlanRejectsIdentityAndRefMismatches(t *testing.T) {
	tests := []struct {
		name string
		edit func(*releasePlanInput)
		want string
	}{
		{"tag version", func(in *releasePlanInput) { in.Tag = "v2.22.1" }, "must exactly match version"},
		{"missing project", func(in *releasePlanInput) { in.Project = "" }, "project is required"},
		{"symbolic head", func(in *releasePlanInput) { in.Head = "HEAD" }, "exact lowercase"},
		{"unsafe branch", func(in *releasePlanInput) { in.Branch = "refs/../main" }, "canonical git ref"},
		{"dot owner", func(in *releasePlanInput) { in.Repository = "./amq-squad" }, "canonical OWNER/REPO"},
		{"dots repository", func(in *releasePlanInput) { in.Repository = "omriariav/..." }, "canonical OWNER/REPO"},
		{"remote identity", func(in *releasePlanInput) { in.RemoteURL = "https://github.com/other/amq-squad.git" }, "exact repository"},
		{"noncanonical remote URL", func(in *releasePlanInput) { in.RemoteURL = "https://github.com/omriariav/amq-squad" }, "canonical GitHub URL"},
		{"lightweight policy", func(in *releasePlanInput) { in.TagPolicy = "lightweight" }, "signed or annotated"},
		{"missing signed evidence", func(in *releasePlanInput) { in.PreflightSHA256 = "unsigned" }, "preflight-sha256"},
		{"notes mutation", func(in *releasePlanInput) { in.NotesSHA256 = strings.Repeat("D", 64) }, "notes-policy=file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validReleasePlanInput(t)
			tc.edit(&in)
			if _, err := buildReleasePlan(in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %q", err, tc.want)
			}
		})
	}
}

func TestReleasePlanIDBindsCanonicalWorktreeAndRemote(t *testing.T) {
	first := validReleasePlanInput(t)
	plan, err := buildReleasePlan(first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Project = t.TempDir()
	second.NotesFile = filepath.Join(second.Project, "release-notes.md")
	if err := os.WriteFile(second.NotesFile, []byte("release notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherWorktree, err := buildReleasePlan(second)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanID == otherWorktree.PlanID || plan.Project == otherWorktree.Project {
		t.Fatalf("plan id did not bind canonical worktree: first=%s second=%s", plan.PlanID, otherWorktree.PlanID)
	}
	ssh := first
	ssh.RemoteURL = "git@github.com:omriariav/amq-squad.git"
	otherRemote, err := buildReleasePlan(ssh)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanID == otherRemote.PlanID {
		t.Fatal("plan id did not bind exact remote URL")
	}
}

func TestReleasePlanGeneratedNotesRemainExplicit(t *testing.T) {
	in := validReleasePlanInput(t)
	in.NotesPolicy, in.NotesFile, in.NotesSHA256 = "generated", "", ""
	plan, err := buildReleasePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Actions[3].Command, "--generate-notes") || len(plan.Verification.Preflight) != 1 || plan.Verification.Preflight[0].Kind != "remote_identity" {
		t.Fatalf("generated notes plan is ambiguous: %+v", plan)
	}
}

func TestReleasePlanResolvesRelativeNotesInsideFrozenWorktree(t *testing.T) {
	in := validReleasePlanInput(t)
	in.NotesFile = filepath.Base(in.NotesFile)
	plan, err := buildReleasePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(plan.Project, filepath.Base(in.NotesFile))
	if plan.NotesFile != want || plan.Verification.Preflight[1].Ref != want || !strings.Contains(plan.Actions[3].Command, shellQuote(want)) {
		t.Fatalf("relative notes were not worktree-bound: %+v", plan)
	}
}

func TestReleasePlanExplicitlyRejectsLightweightAndUnsignedExpectations(t *testing.T) {
	in := validReleasePlanInput(t)
	plan, err := buildReleasePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Verification.Local[0].ExpectedType != "tag" {
		t.Fatal("local object-type expectation must reject lightweight tags")
	}
	if plan.Verification.Local[2].ExpectedSignature != "valid" {
		t.Fatal("signed policy must require signature verification")
	}
	if plan.Verification.Remote[1].MustDifferFromSHA != in.Head || plan.Verification.Remote[2].ExpectedSHA != in.Head {
		t.Fatal("remote object and peeled expectations must be distinct")
	}
}

func TestReleasePlanVerificationRejectsObservedTampering(t *testing.T) {
	in := validReleasePlanInput(t)
	plan, err := buildReleasePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	valid := releaseVerificationObservation{
		RemoteURL: in.RemoteURL, NotesSHA256: in.NotesSHA256,
		RemoteBranchSHA: in.Head, LocalTagType: "tag", LocalPeeledSHA: in.Head, LocalSignature: "valid",
		RemoteTagObjectSHA: strings.Repeat("d", 40), RemotePeeledSHA: in.Head,
	}
	if failures := validateReleaseVerificationObservation(plan, valid); len(failures) != 0 {
		t.Fatalf("valid observation failed: %v", failures)
	}
	tests := []struct {
		name string
		edit func(*releaseVerificationObservation)
		want string
	}{
		{"branch SHA", func(o *releaseVerificationObservation) { o.RemoteBranchSHA = strings.Repeat("e", 40) }, "remote branch SHA"},
		{"remote URL", func(o *releaseVerificationObservation) { o.RemoteURL = "https://github.com/other/repo.git" }, "named remote URL"},
		{"notes SHA", func(o *releaseVerificationObservation) { o.NotesSHA256 = strings.Repeat("e", 64) }, "release notes SHA-256"},
		{"lightweight tag", func(o *releaseVerificationObservation) { o.LocalTagType = "commit" }, "annotated tag object"},
		{"unsigned signed policy", func(o *releaseVerificationObservation) { o.LocalSignature = "invalid" }, "valid local signature"},
		{"remote lightweight", func(o *releaseVerificationObservation) { o.RemoteTagObjectSHA = in.Head }, "absent or lightweight"},
		{"remote peeled mismatch", func(o *releaseVerificationObservation) { o.RemotePeeledSHA = strings.Repeat("f", 40) }, "remote peeled tag SHA"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observed := valid
			tc.edit(&observed)
			failures := validateReleaseVerificationObservation(plan, observed)
			if len(failures) != 1 || !strings.Contains(failures[0], tc.want) {
				t.Fatalf("failures=%v want=%q", failures, tc.want)
			}
		})
	}
}

func TestReleasePlanFailedStateRemainsNonReady(t *testing.T) {
	in := validReleasePlanInput(t)
	in.WorktreeState = "dirty"
	in.PreflightState = "failed"
	plan, err := buildReleasePlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || len(plan.Failures) != 2 {
		t.Fatalf("failed state=%+v", plan)
	}
}
