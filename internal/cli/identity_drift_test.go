package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #540 acceptance criterion 3: any identity-drift message names each differing
// field with both values, and the message can NEVER show equal operands.
//
// The guarantee is structural rather than incidental: identityDrift.add is the
// only way a field enters the report and it drops equal pairs, so these tests
// assert a property of the constructor, not the spelling of one call site.
func TestIdentityDriftCannotRenderEqualOperands(t *testing.T) {
	cases := []struct {
		name             string
		accepted, curren string
	}{
		{"identical non-empty", "squad/v2-25-0", "squad/v2-25-0"},
		{"both empty", "", ""},
		{"both whitespace", "  ", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d identityDrift
			// Every comparison entry point must refuse an equal pair, including
			// comparePath, whose predicate (sameFilesystemPath) returns false for
			// two EMPTY paths and would otherwise report empty-vs-empty as drift.
			d.compare("namespace", tc.accepted, tc.curren)
			d.comparePath("project", tc.accepted, tc.curren)
			if d.drifted() {
				t.Fatalf("equal operands (%q, %q) were recorded as drift: %v", tc.accepted, tc.curren, d.fields)
			}
			if err := d.err("subject"); err != nil {
				t.Fatalf("equal operands produced an error: %v", err)
			}
		})
	}
}

// Every differing field must appear, with both of its values. The #540 report
// showed one field while a different one was what actually differed.
func TestIdentityDriftNamesEveryDifferingFieldWithBothValues(t *testing.T) {
	var d identityDrift
	d.compare("profile", "squad", "other")
	d.compare("session", "v2-25-0", "v2-26-0")
	err := d.err("prepared launch record identity drift")
	if err == nil {
		t.Fatal("differing fields produced no error")
	}
	msg := err.Error()
	for _, want := range []string{"profile", `"squad"`, `"other"`, "session", `"v2-25-0"`, `"v2-26-0"`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("drift message %q is missing %q", msg, want)
		}
	}
}

// The regression that produced the contradictory report: the Project component
// differed, but the message rendered only the namespace, which was equal. The
// message must now name `project` and must not claim a namespace mismatch.
func TestPreparedLaunchIdentityDriftNamesProjectNotNamespace(t *testing.T) {
	manifest := preparedRunManifest{
		Project:   filepath.Join(t.TempDir(), "accepted-project"),
		Profile:   "squad",
		Session:   "v2-25-0",
		Namespace: "squad/v2-25-0",
	}
	current := filepath.Join(t.TempDir(), "different-project")

	err := preparedLaunchIdentityDrift(manifest, current, "squad", "v2-25-0")
	if err == nil {
		t.Fatal("a differing project must be reported as drift")
	}
	msg := err.Error()
	if !strings.Contains(msg, "project") {
		t.Fatalf("drift message %q does not name the project field that actually differs", msg)
	}
	if strings.Contains(msg, "namespace") {
		t.Fatalf("drift message %q blames the namespace, which is identical; this is the #540 contradiction", msg)
	}
	if !strings.Contains(msg, manifest.Project) || !strings.Contains(msg, current) {
		t.Fatalf("drift message %q must carry both project values", msg)
	}
}

// A project recorded in one representation and resolved in another is the SAME
// project and must not be drift. This is the launch that #540 killed: prepared
// with `--project .`, bootstrapped from the resolved absolute path.
func TestPreparedLaunchIdentityAcceptsEquivalentProjectRepresentations(t *testing.T) {
	project := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	resolved := canonicalFilesystemPath(project)

	// A manifest written by a release that recorded "." verbatim must still be
	// accepted by an agent that resolves the project absolutely.
	for _, recorded := range []string{".", "./", project, resolved} {
		t.Run(recorded, func(t *testing.T) {
			manifest := preparedRunManifest{
				Project:   recorded,
				Profile:   "squad",
				Session:   "v2-25-0",
				Namespace: "squad/v2-25-0",
			}
			if err := preparedLaunchIdentityDrift(manifest, resolved, "squad", "v2-25-0"); err != nil {
				t.Fatalf("project recorded as %q vs current %q reported drift: %v", recorded, resolved, err)
			}
		})
	}
}

func TestPreparedLaunchIdentityAcceptsDifferentlyCasedProjectOnCaseInsensitiveFilesystem(t *testing.T) {
	project := filepath.Join(t.TempDir(), "PreparedProject")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	variant, ok := differentlyCasedExistingPath(project)
	if !ok {
		t.Skip("test filesystem is case-sensitive")
	}

	manifest := preparedRunManifest{
		Project:   variant,
		Profile:   "squad-v2-27-0",
		Session:   "v2-27-0",
		Namespace: "squad-v2-27-0/v2-27-0",
	}
	if err := preparedLaunchIdentityDrift(manifest, project, manifest.Profile, manifest.Session); err != nil {
		t.Fatalf("differently-cased aliases of one prepared project reported drift: %v", err)
	}
}

// A genuine profile or session mismatch must still fail closed. The fix makes
// the message honest; it must not make the check permissive.
func TestPreparedLaunchIdentityStillRejectsRealNamespaceDrift(t *testing.T) {
	project := t.TempDir()
	base := preparedRunManifest{Project: project, Profile: "squad", Session: "v2-25-0", Namespace: "squad/v2-25-0"}

	if err := preparedLaunchIdentityDrift(base, project, "other", "v2-25-0"); err == nil {
		t.Fatal("a differing profile must be reported as drift")
	}
	if err := preparedLaunchIdentityDrift(base, project, "squad", "v2-26-0"); err == nil {
		t.Fatal("a differing session must be reported as drift")
	}
	// A manifest whose recorded namespace disagrees with its own profile/session
	// is corrupt and must be rejected.
	corrupt := base
	corrupt.Namespace = "squad/v9-9-9"
	if err := preparedLaunchIdentityDrift(corrupt, project, "squad", "v2-25-0"); err == nil {
		t.Fatal("a manifest namespace inconsistent with profile/session must be reported as drift")
	}
}
