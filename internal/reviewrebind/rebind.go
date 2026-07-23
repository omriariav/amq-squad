// Package reviewrebind proves and records review-safe Git tree or patch
// equivalence. Artifacts are evidence about two immutable commits; they do not
// grant approval and never relax the requirement for review evidence.
package reviewrebind

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	SchemaVersion = 1
	Kind          = "review_rebinding"
	ProofAuto     = "auto"
	ProofTree     = "tree"
	ProofPatchID  = "patch_id"

	storeMaxBytes = 512 << 10
	gitMaxBytes   = 128 << 20
)

// PatchFingerprint records independent stable and whitespace-sensitive patch
// IDs. MetadataSHA256 additionally binds changed paths, statuses, and mode
// summaries; DiffSHA256 is retained for audit even when rebases change context.
type PatchFingerprint struct {
	StablePatchID  string `json:"stable_patch_id"`
	VerbatimID     string `json:"verbatim_patch_id"`
	MetadataSHA256 string `json:"metadata_sha256"`
	DiffSHA256     string `json:"diff_sha256"`
}

type Proof struct {
	RepositoryID string            `json:"repository_id"`
	ProjectDir   string            `json:"-"`
	OldHead      string            `json:"old_head"`
	NewHead      string            `json:"new_head"`
	ProofType    string            `json:"proof_type"`
	OldTree      string            `json:"old_tree"`
	NewTree      string            `json:"new_tree"`
	OldBase      string            `json:"old_base,omitempty"`
	NewBase      string            `json:"new_base,omitempty"`
	OldPatch     *PatchFingerprint `json:"old_patch,omitempty"`
	NewPatch     *PatchFingerprint `json:"new_patch,omitempty"`
}

type Artifact struct {
	SchemaVersion  int               `json:"schema_version"`
	Kind           string            `json:"kind"`
	RepositoryID   string            `json:"repository_id"`
	OldHead        string            `json:"old_head"`
	NewHead        string            `json:"new_head"`
	ProofType      string            `json:"proof_type"`
	OldTree        string            `json:"old_tree"`
	NewTree        string            `json:"new_tree"`
	OldBase        string            `json:"old_base,omitempty"`
	NewBase        string            `json:"new_base,omitempty"`
	OldPatch       *PatchFingerprint `json:"old_patch,omitempty"`
	NewPatch       *PatchFingerprint `json:"new_patch,omitempty"`
	CreatedAt      string            `json:"created_at"`
	ArtifactSHA256 string            `json:"artifact_sha256"`
}

type Request struct {
	ProjectDir string
	OldHead    string
	NewHead    string
	OldBase    string
	NewBase    string
	ProofType  string
}

func Prove(ctx context.Context, req Request) (Proof, error) {
	project, repositoryID, err := resolveRepository(ctx, req.ProjectDir)
	if err != nil {
		return Proof{}, err
	}
	oldHead, err := resolveCommit(ctx, project, "old head", req.OldHead)
	if err != nil {
		return Proof{}, err
	}
	newHead, err := resolveCommit(ctx, project, "new head", req.NewHead)
	if err != nil {
		return Proof{}, err
	}
	if oldHead == newHead {
		return Proof{}, fmt.Errorf("old and new heads resolve to the same commit %s", oldHead)
	}
	oldTree, err := resolveTree(ctx, project, oldHead)
	if err != nil {
		return Proof{}, err
	}
	newTree, err := resolveTree(ctx, project, newHead)
	if err != nil {
		return Proof{}, err
	}
	proofType := strings.TrimSpace(req.ProofType)
	if proofType == "" {
		proofType = ProofAuto
	}
	if proofType != ProofAuto && proofType != ProofTree && proofType != ProofPatchID {
		return Proof{}, fmt.Errorf("proof type must be %q, %q, or %q", ProofAuto, ProofTree, ProofPatchID)
	}
	proof := Proof{
		RepositoryID: repositoryID,
		ProjectDir:   project,
		OldHead:      oldHead,
		NewHead:      newHead,
		OldTree:      oldTree,
		NewTree:      newTree,
	}
	if proofType != ProofPatchID && oldTree == newTree {
		proof.ProofType = ProofTree
		return proof, nil
	}
	if proofType == ProofTree {
		return Proof{}, fmt.Errorf("tree proof refused: old tree %s differs from new tree %s", oldTree, newTree)
	}
	if strings.TrimSpace(req.OldBase) == "" || strings.TrimSpace(req.NewBase) == "" {
		return Proof{}, fmt.Errorf("patch-id proof requires explicit --old-base and --new-base")
	}
	oldBase, err := resolveCommit(ctx, project, "old base", req.OldBase)
	if err != nil {
		return Proof{}, err
	}
	newBase, err := resolveCommit(ctx, project, "new base", req.NewBase)
	if err != nil {
		return Proof{}, err
	}
	if err := requireAncestor(ctx, project, "old base", oldBase, oldHead); err != nil {
		return Proof{}, err
	}
	if err := requireAncestor(ctx, project, "new base", newBase, newHead); err != nil {
		return Proof{}, err
	}
	oldPatch, err := fingerprintPatch(ctx, project, oldBase, oldHead)
	if err != nil {
		return Proof{}, fmt.Errorf("fingerprint old delta: %w", err)
	}
	newPatch, err := fingerprintPatch(ctx, project, newBase, newHead)
	if err != nil {
		return Proof{}, fmt.Errorf("fingerprint new delta: %w", err)
	}
	if oldPatch.StablePatchID != newPatch.StablePatchID ||
		oldPatch.VerbatimID != newPatch.VerbatimID ||
		oldPatch.MetadataSHA256 != newPatch.MetadataSHA256 {
		return Proof{}, fmt.Errorf(
			"patch-id proof refused: scoped deltas differ (stable %s/%s, verbatim %s/%s, metadata %s/%s)",
			oldPatch.StablePatchID, newPatch.StablePatchID,
			oldPatch.VerbatimID, newPatch.VerbatimID,
			oldPatch.MetadataSHA256, newPatch.MetadataSHA256,
		)
	}
	proof.ProofType = ProofPatchID
	proof.OldBase = oldBase
	proof.NewBase = newBase
	proof.OldPatch = &oldPatch
	proof.NewPatch = &newPatch
	return proof, nil
}

func NewArtifact(proof Proof, now time.Time) (Artifact, error) {
	a := Artifact{
		SchemaVersion: SchemaVersion,
		Kind:          Kind,
		RepositoryID:  proof.RepositoryID,
		OldHead:       proof.OldHead,
		NewHead:       proof.NewHead,
		ProofType:     proof.ProofType,
		OldTree:       proof.OldTree,
		NewTree:       proof.NewTree,
		OldBase:       proof.OldBase,
		NewBase:       proof.NewBase,
		OldPatch:      clonePatch(proof.OldPatch),
		NewPatch:      clonePatch(proof.NewPatch),
		CreatedAt:     now.UTC().Format(time.RFC3339Nano),
	}
	digest, err := artifactDigest(a)
	if err != nil {
		return Artifact{}, err
	}
	a.ArtifactSHA256 = digest
	if err := Validate(a); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

func Validate(a Artifact) error {
	if a.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported rebinding schema version %d", a.SchemaVersion)
	}
	if a.Kind != Kind {
		return fmt.Errorf("rebinding kind is %q, want %q", a.Kind, Kind)
	}
	if !validDigest(a.RepositoryID) {
		return fmt.Errorf("repository_id must be a sha256 digest")
	}
	if !validObjectID(a.OldHead) || !validObjectID(a.NewHead) || a.OldHead == a.NewHead {
		return fmt.Errorf("old_head and new_head must be distinct full Git object IDs")
	}
	if !validObjectID(a.OldTree) || !validObjectID(a.NewTree) {
		return fmt.Errorf("old_tree and new_tree must be full Git object IDs")
	}
	switch a.ProofType {
	case ProofTree:
		if a.OldTree != a.NewTree {
			return fmt.Errorf("tree proof contains different tree hashes")
		}
		if a.OldBase != "" || a.NewBase != "" || a.OldPatch != nil || a.NewPatch != nil {
			return fmt.Errorf("tree proof must not contain patch-id fields")
		}
	case ProofPatchID:
		if !validObjectID(a.OldBase) || !validObjectID(a.NewBase) {
			return fmt.Errorf("patch-id proof requires full old_base and new_base object IDs")
		}
		if a.OldPatch == nil || a.NewPatch == nil {
			return fmt.Errorf("patch-id proof requires both patch fingerprints")
		}
		if err := validatePatch(*a.OldPatch); err != nil {
			return fmt.Errorf("old patch: %w", err)
		}
		if err := validatePatch(*a.NewPatch); err != nil {
			return fmt.Errorf("new patch: %w", err)
		}
		if a.OldPatch.StablePatchID != a.NewPatch.StablePatchID ||
			a.OldPatch.VerbatimID != a.NewPatch.VerbatimID ||
			a.OldPatch.MetadataSHA256 != a.NewPatch.MetadataSHA256 {
			return fmt.Errorf("patch-id proof fingerprints differ")
		}
	default:
		return fmt.Errorf("unknown rebinding proof type %q", a.ProofType)
	}
	if _, err := time.Parse(time.RFC3339Nano, a.CreatedAt); err != nil {
		return fmt.Errorf("invalid created_at: %w", err)
	}
	want, err := artifactDigest(a)
	if err != nil {
		return err
	}
	if a.ArtifactSHA256 != want {
		return fmt.Errorf("artifact_sha256 mismatch")
	}
	return nil
}

// Verify re-runs the recorded proof against the repository's Git objects. A
// self-consistent JSON document is never sufficient on its own.
func Verify(ctx context.Context, projectDir string, a Artifact) error {
	if err := Validate(a); err != nil {
		return err
	}
	proof, err := Prove(ctx, Request{
		ProjectDir: projectDir,
		OldHead:    a.OldHead,
		NewHead:    a.NewHead,
		OldBase:    a.OldBase,
		NewBase:    a.NewBase,
		ProofType:  a.ProofType,
	})
	if err != nil {
		return err
	}
	if proof.RepositoryID != a.RepositoryID {
		return fmt.Errorf("artifact belongs to a different Git repository")
	}
	if !MatchesProof(a, proof) {
		return fmt.Errorf("recorded rebinding proof does not match current Git objects")
	}
	return nil
}

// MatchesProof reports whether an artifact's proof-bearing fields exactly
// match a freshly derived proof.
func MatchesProof(a Artifact, proof Proof) bool {
	derived := Artifact{
		SchemaVersion: SchemaVersion,
		Kind:          Kind,
		RepositoryID:  proof.RepositoryID,
		OldHead:       proof.OldHead,
		NewHead:       proof.NewHead,
		ProofType:     proof.ProofType,
		OldTree:       proof.OldTree,
		NewTree:       proof.NewTree,
		OldBase:       proof.OldBase,
		NewBase:       proof.NewBase,
		OldPatch:      proof.OldPatch,
		NewPatch:      proof.NewPatch,
	}
	recorded := a
	recorded.CreatedAt = ""
	recorded.ArtifactSHA256 = ""
	return reflect.DeepEqual(derived, recorded)
}

// ID returns the deterministic immutable artifact filename.
func ID(a Artifact) string {
	return a.OldHead + "--" + a.NewHead + ".json"
}

// Path returns an artifact's canonical project-local path.
func Path(projectDir, id string) string {
	return filepath.Join(projectDir, ".amq-squad", "reviews", "rebindings", id)
}

func clonePatch(in *PatchFingerprint) *PatchFingerprint {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func artifactDigest(a Artifact) (string, error) {
	a.ArtifactSHA256 = ""
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validatePatch(p PatchFingerprint) error {
	if !validObjectID(p.StablePatchID) || !validObjectID(p.VerbatimID) {
		return fmt.Errorf("patch IDs must be full hexadecimal IDs")
	}
	if !validDigest(p.MetadataSHA256) || !validDigest(p.DiffSHA256) {
		return fmt.Errorf("patch metadata and diff hashes must be sha256 digests")
	}
	return nil
}

func validObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

func validDigest(s string) bool {
	return len(s) == len("sha256:")+64 && strings.HasPrefix(s, "sha256:") && validHex(s[len("sha256:"):])
}

func validHex(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func resolveRepository(ctx context.Context, projectDir string) (string, string, error) {
	if strings.TrimSpace(projectDir) == "" {
		return "", "", fmt.Errorf("project directory is required")
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return "", "", err
	}
	topRaw, err := gitOutput(ctx, abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("resolve Git repository: %w", err)
	}
	top, err := filepath.EvalSymlinks(strings.TrimSpace(string(topRaw)))
	if err != nil {
		return "", "", fmt.Errorf("resolve Git repository path: %w", err)
	}
	commonRaw, err := gitOutput(ctx, top, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	common := strings.TrimSpace(string(commonRaw))
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", "", fmt.Errorf("resolve Git common directory path: %w", err)
	}
	sum := sha256.Sum256([]byte(common))
	return top, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func resolveCommit(ctx context.Context, project, label, revision string) (string, error) {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	out, err := gitOutput(ctx, project, nil, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", label, revision, err)
	}
	id := strings.TrimSpace(string(out))
	if !validObjectID(id) {
		return "", fmt.Errorf("resolved %s is not a full Git object ID", label)
	}
	return id, nil
}

func resolveTree(ctx context.Context, project, commit string) (string, error) {
	out, err := gitOutput(ctx, project, nil, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("resolve tree for %s: %w", commit, err)
	}
	tree := strings.TrimSpace(string(out))
	if !validObjectID(tree) {
		return "", fmt.Errorf("resolved tree is not a full Git object ID")
	}
	return tree, nil
}

// ResolveCommit resolves a revision to a full commit object ID in projectDir.
func ResolveCommit(ctx context.Context, projectDir, revision string) (string, error) {
	project, _, err := resolveRepository(ctx, projectDir)
	if err != nil {
		return "", err
	}
	return resolveCommit(ctx, project, "commit", revision)
}

func requireAncestor(ctx context.Context, project, label, base, head string) error {
	if _, err := gitOutput(ctx, project, nil, "merge-base", "--is-ancestor", base, head); err != nil {
		return fmt.Errorf("%s %s is not an ancestor of head %s", label, base, head)
	}
	return nil
}

func fingerprintPatch(ctx context.Context, project, base, head string) (PatchFingerprint, error) {
	diffArgs := []string{
		"diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv",
		"--no-color", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/",
		base, head, "--",
	}
	diff, err := gitOutput(ctx, project, nil, diffArgs...)
	if err != nil {
		return PatchFingerprint{}, err
	}
	stable, err := patchID(ctx, project, diff, "--stable")
	if err != nil {
		return PatchFingerprint{}, err
	}
	verbatim, err := patchID(ctx, project, diff, "--verbatim")
	if err != nil {
		return PatchFingerprint{}, err
	}
	nameStatus, err := gitOutput(ctx, project, nil, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--name-status", "-z", base, head, "--")
	if err != nil {
		return PatchFingerprint{}, err
	}
	summary, err := gitOutput(ctx, project, nil, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--summary", base, head, "--")
	if err != nil {
		return PatchFingerprint{}, err
	}
	meta := sha256.New()
	_, _ = meta.Write(nameStatus)
	_, _ = meta.Write([]byte{0})
	_, _ = meta.Write(summary)
	diffSum := sha256.Sum256(diff)
	return PatchFingerprint{
		StablePatchID:  stable,
		VerbatimID:     verbatim,
		MetadataSHA256: "sha256:" + hex.EncodeToString(meta.Sum(nil)),
		DiffSHA256:     "sha256:" + hex.EncodeToString(diffSum[:]),
	}, nil
}

func patchID(ctx context.Context, project string, diff []byte, mode string) (string, error) {
	out, err := gitOutput(ctx, project, diff, "patch-id", mode)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || !validObjectID(fields[0]) {
		if len(fields) == 0 {
			return "", fmt.Errorf("empty scoped delta has no patch-id")
		}
		return "", fmt.Errorf("unexpected git patch-id output")
	}
	return fields[0], nil
}

func gitOutput(ctx context.Context, project string, stdin []byte, args ...string) ([]byte, error) {
	base := []string{
		"-c", "color.ui=false",
		"-c", "core.quotePath=true",
		"-c", "diff.algorithm=myers",
		"-C", project,
	}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = sanitizedGitEnv(os.Environ())
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr cappedBuffer
	stdout.limit = gitMaxBytes
	stderr.limit = 1 << 20
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.exceeded {
		return nil, fmt.Errorf("git output exceeds %d bytes", gitMaxBytes)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.buf.String())
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return stdout.buf.Bytes(), nil
}

func sanitizedGitEnv(env []string) []string {
	out := make([]string, 0, len(env)+6)
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "PWD" || key == "LC_ALL" || strings.HasPrefix(key, "GIT_") {
			continue
		}
		out = append(out, entry)
	}
	return append(out,
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int64
	exceeded bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - int64(b.buf.Len())
	if remaining > 0 {
		if int64(len(p)) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.exceeded = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.exceeded = true
	}
	return n, nil
}

type Store struct {
	ProjectDir string
	root       *os.Root
}

// OpenStore opens the descriptor-confined rebinding store, optionally creating
// its non-symlink directory ancestors.
func OpenStore(projectDir string, create bool) (*Store, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("rebinding project root must be a non-symlink directory")
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	opened, openErr := statRoot(root)
	after, afterErr := os.Lstat(abs)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(before, opened) || !os.SameFile(after, opened) {
		root.Close()
		return nil, fmt.Errorf("rebinding project root identity changed during open")
	}
	current := abs
	for _, component := range []string{".amq-squad", "reviews", "rebindings"} {
		info, statErr := root.Lstat(component)
		if errors.Is(statErr, os.ErrNotExist) && create {
			if mkdirErr := root.Mkdir(component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				root.Close()
				return nil, mkdirErr
			}
			if err := syncRoot(root); err != nil {
				root.Close()
				return nil, err
			}
			info, statErr = root.Lstat(component)
		}
		if errors.Is(statErr, os.ErrNotExist) && !create {
			root.Close()
			return nil, os.ErrNotExist
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			root.Close()
			return nil, fmt.Errorf("rebinding store ancestor must be a non-symlink directory: %s", filepath.Join(current, component))
		}
		child, openErr := root.OpenRoot(component)
		if openErr != nil {
			root.Close()
			return nil, openErr
		}
		opened, openErr := statRoot(child)
		visible, visibleErr := root.Lstat(component)
		if openErr != nil || visibleErr != nil || !os.SameFile(info, opened) || !os.SameFile(visible, opened) {
			child.Close()
			root.Close()
			return nil, fmt.Errorf("rebinding store ancestor identity changed: %s", filepath.Join(current, component))
		}
		root.Close()
		root = child
		current = filepath.Join(current, component)
	}
	return &Store{ProjectDir: abs, root: root}, nil
}

func (s *Store) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

func (s *Store) Read(id string) (Artifact, error) {
	if !validID(id) {
		return Artifact{}, fmt.Errorf("invalid rebinding artifact id")
	}
	info, err := s.root.Lstat(id)
	if err != nil {
		return Artifact{}, err
	}
	if err := validateLeaf(info, id, 1); err != nil {
		return Artifact{}, err
	}
	f, err := s.root.Open(id)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	opened, openErr := f.Stat()
	visible, visibleErr := s.root.Lstat(id)
	if openErr != nil || visibleErr != nil ||
		validateLeaf(opened, id, 1) != nil || validateLeaf(visible, id, 1) != nil ||
		!os.SameFile(info, opened) || !os.SameFile(opened, visible) {
		return Artifact{}, fmt.Errorf("rebinding artifact identity changed during read")
	}
	b, err := io.ReadAll(io.LimitReader(f, storeMaxBytes+1))
	if err != nil {
		return Artifact{}, err
	}
	if len(b) > storeMaxBytes {
		return Artifact{}, fmt.Errorf("rebinding artifact exceeds %d bytes", storeMaxBytes)
	}
	var a Artifact
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return Artifact{}, fmt.Errorf("decode rebinding artifact: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return Artifact{}, fmt.Errorf("decode rebinding artifact: multiple JSON values")
	}
	if err := Validate(a); err != nil {
		return Artifact{}, err
	}
	if ID(a) != id {
		return Artifact{}, fmt.Errorf("rebinding artifact filename does not match its heads")
	}
	return a, nil
}

// Write publishes a new artifact without replacing an existing path. Exact
// replays are idempotent; conflicting content at the deterministic ID refuses.
func (s *Store) Write(a Artifact) (bool, error) {
	if err := Validate(a); err != nil {
		return false, err
	}
	id := ID(a)
	if existing, err := s.Read(id); err == nil {
		if reflect.DeepEqual(existing, a) {
			return true, nil
		}
		return false, fmt.Errorf("rebinding artifact %s already exists with different content", id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	tempName := "." + id + "." + hex.EncodeToString(sum[:8]) + ".tmp"
	temp, err := s.root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("create rebinding artifact temp: %w", err)
	}
	removeTemp := true
	defer func() {
		_ = temp.Close()
		if removeTemp {
			_ = s.root.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return false, err
	}
	if err := temp.Sync(); err != nil {
		return false, err
	}
	tempInfo, err := temp.Stat()
	if err != nil {
		return false, err
	}
	if err := validateLeaf(tempInfo, tempName, 1); err != nil {
		return false, err
	}
	if err := s.root.Link(tempName, id); err != nil {
		if existing, readErr := s.Read(id); readErr == nil && reflect.DeepEqual(existing, a) {
			return true, nil
		}
		return false, fmt.Errorf("publish immutable rebinding artifact: %w", err)
	}
	if err := syncRoot(s.root); err != nil {
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if err := s.root.Remove(tempName); err != nil {
		removeTemp = false
		return false, err
	}
	removeTemp = false
	if err := syncRoot(s.root); err != nil {
		return false, err
	}
	published, err := s.Read(id)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(published, a) {
		return false, fmt.Errorf("published rebinding artifact changed")
	}
	return false, nil
}

func validID(id string) bool {
	parts := strings.Split(strings.TrimSuffix(id, ".json"), "--")
	return strings.HasSuffix(id, ".json") && len(parts) == 2 && validObjectID(parts[0]) && validObjectID(parts[1])
}

func validateLeaf(info os.FileInfo, label string, wantLinks uint64) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("rebinding artifact %s must be a non-symlink regular file", label)
	}
	links, ok := linkCount(info)
	if !ok || links != wantLinks {
		return fmt.Errorf("rebinding artifact %s link count must be %d", label, wantLinks)
	}
	return nil
}

func linkCount(info os.FileInfo) (uint64, bool) {
	if info == nil || info.Sys() == nil {
		return 0, false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, false
	}
	field := value.FieldByName("Nlink")
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Int() < 0 {
			return 0, false
		}
		return uint64(field.Int()), true
	default:
		return 0, false
	}
}

func statRoot(root *os.Root) (os.FileInfo, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func syncRoot(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}
