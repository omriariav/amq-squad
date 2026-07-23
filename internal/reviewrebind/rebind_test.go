package reviewrebind

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTreeProofRecordsImmutableArtifactAndReproves(t *testing.T) {
	repo := initTestRepository(t)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "content\n")
	testGit(t, repo, "add", "tracked.txt")
	testGit(t, repo, "commit", "-m", "reviewed")
	oldHead := testGit(t, repo, "rev-parse", "HEAD")
	testGit(t, repo, "commit", "--allow-empty", "-m", "rebuilt")
	newHead := testGit(t, repo, "rev-parse", "HEAD")

	proof, err := Prove(context.Background(), Request{
		ProjectDir: repo,
		OldHead:    oldHead,
		NewHead:    newHead,
		ProofType:  ProofAuto,
	})
	if err != nil {
		t.Fatalf("prove tree identity: %v", err)
	}
	if proof.ProofType != ProofTree || proof.OldTree != proof.NewTree {
		t.Fatalf("unexpected proof: %#v", proof)
	}
	created := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	artifact, err := NewArtifact(proof, created)
	if err != nil {
		t.Fatalf("new artifact: %v", err)
	}
	store, err := OpenStore(repo, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	existing, err := store.Write(artifact)
	if err != nil || existing {
		t.Fatalf("first write existing=%v err=%v", existing, err)
	}
	existing, err = store.Write(artifact)
	if err != nil || !existing {
		t.Fatalf("idempotent write existing=%v err=%v", existing, err)
	}
	got, err := store.Read(ID(artifact))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if err := Verify(context.Background(), repo, got); err != nil {
		t.Fatalf("re-prove artifact: %v", err)
	}

	conflict, err := NewArtifact(proof, created.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(conflict); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("want immutable conflict, got %v", err)
	}
}

func TestPatchIDProofRequiresWhitespaceSensitiveAndMetadataIdentity(t *testing.T) {
	repo := initTestRepository(t)
	writeTestFile(t, filepath.Join(repo, "feature.txt"), "base\n")
	testGit(t, repo, "add", "feature.txt")
	testGit(t, repo, "commit", "-m", "old base")
	oldBase := testGit(t, repo, "rev-parse", "HEAD")

	writeTestFile(t, filepath.Join(repo, "feature.txt"), "base\nfeature\n")
	testGit(t, repo, "commit", "-am", "reviewed feature")
	oldHead := testGit(t, repo, "rev-parse", "HEAD")

	testGit(t, repo, "checkout", "--detach", oldBase)
	writeTestFile(t, filepath.Join(repo, "upstream.txt"), "unrelated upstream\n")
	testGit(t, repo, "add", "upstream.txt")
	testGit(t, repo, "commit", "-m", "new base")
	newBase := testGit(t, repo, "rev-parse", "HEAD")
	writeTestFile(t, filepath.Join(repo, "feature.txt"), "base\nfeature\n")
	testGit(t, repo, "commit", "-am", "rebuilt feature")
	newHead := testGit(t, repo, "rev-parse", "HEAD")

	proof, err := Prove(context.Background(), Request{
		ProjectDir: repo,
		OldHead:    oldHead,
		NewHead:    newHead,
		OldBase:    oldBase,
		NewBase:    newBase,
		ProofType:  ProofPatchID,
	})
	if err != nil {
		t.Fatalf("prove patch identity: %v", err)
	}
	if proof.ProofType != ProofPatchID || proof.OldTree == proof.NewTree {
		t.Fatalf("expected patch-id proof across different trees: %#v", proof)
	}
	if proof.OldPatch.StablePatchID != proof.NewPatch.StablePatchID ||
		proof.OldPatch.VerbatimID != proof.NewPatch.VerbatimID ||
		proof.OldPatch.MetadataSHA256 != proof.NewPatch.MetadataSHA256 {
		t.Fatalf("patch fingerprints differ: %#v", proof)
	}

	testGit(t, repo, "checkout", "--detach", newBase)
	writeTestFile(t, filepath.Join(repo, "feature.txt"), "base\nfeature \n")
	testGit(t, repo, "commit", "-am", "whitespace semantic delta")
	whitespaceHead := testGit(t, repo, "rev-parse", "HEAD")
	if _, err := Prove(context.Background(), Request{
		ProjectDir: repo,
		OldHead:    oldHead,
		NewHead:    whitespaceHead,
		OldBase:    oldBase,
		NewBase:    newBase,
		ProofType:  ProofPatchID,
	}); err == nil || !strings.Contains(err.Error(), "patch-id proof refused") {
		t.Fatalf("want whitespace-sensitive refusal, got %v", err)
	}
}

func TestArtifactReadRefusesTamperingAndHardlinks(t *testing.T) {
	repo := initTestRepository(t)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "content\n")
	testGit(t, repo, "add", "tracked.txt")
	testGit(t, repo, "commit", "-m", "reviewed")
	oldHead := testGit(t, repo, "rev-parse", "HEAD")
	testGit(t, repo, "commit", "--allow-empty", "-m", "rebuilt")
	newHead := testGit(t, repo, "rev-parse", "HEAD")
	proof, err := Prove(context.Background(), Request{ProjectDir: repo, OldHead: oldHead, NewHead: newHead})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := NewArtifact(proof, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Write(artifact); err != nil {
		t.Fatal(err)
	}
	path := Path(repo, ID(artifact))
	link := filepath.Join(repo, "artifact-hardlink.json")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ID(artifact)); err == nil || !strings.Contains(err.Error(), "link count") {
		t.Fatalf("want hardlink refusal, got %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), artifact.ArtifactSHA256, "sha256:"+strings.Repeat("0", 64), 1))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ID(artifact)); err == nil || !strings.Contains(err.Error(), "artifact_sha256 mismatch") {
		t.Fatalf("want tamper refusal, got %v", err)
	}
}

func TestStoreRefusesSymlinkAncestor(t *testing.T) {
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(project, ".amq-squad", "reviews")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(project, true); err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("want symlink ancestor refusal, got %v", err)
	}
}

func TestProofIgnoresHostileGitEnvironmentAndReplaceRefs(t *testing.T) {
	repo := initTestRepository(t)
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "reviewed\n")
	testGit(t, repo, "add", "tracked.txt")
	testGit(t, repo, "commit", "-m", "reviewed")
	oldHead := testGit(t, repo, "rev-parse", "HEAD")
	writeTestFile(t, filepath.Join(repo, "tracked.txt"), "semantic change\n")
	testGit(t, repo, "commit", "-am", "changed")
	newHead := testGit(t, repo, "rev-parse", "HEAD")
	testGit(t, repo, "replace", newHead, oldHead)

	hostile := initTestRepository(t)
	t.Setenv("GIT_DIR", filepath.Join(hostile, ".git"))
	t.Setenv("GIT_WORK_TREE", hostile)
	t.Setenv("GIT_NAMESPACE", "hostile")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "diff.external")
	t.Setenv("GIT_CONFIG_VALUE_0", "false-positive-helper")
	if _, err := Prove(context.Background(), Request{
		ProjectDir: repo,
		OldHead:    oldHead,
		NewHead:    newHead,
		ProofType:  ProofTree,
	}); err == nil || !strings.Contains(err.Error(), "tree proof refused") {
		t.Fatalf("replace ref or hostile Git environment influenced proof: %v", err)
	}
}

func initTestRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	testGit(t, repo, "init")
	testGit(t, repo, "config", "user.email", "test@example.com")
	testGit(t, repo, "config", "user.name", "Test User")
	return repo
}

func testGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
