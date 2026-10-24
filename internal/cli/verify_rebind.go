package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/reviewrebind"
)

type verifyRebindResult struct {
	OK           bool                  `json:"ok"`
	ArtifactID   string                `json:"artifact_id"`
	ArtifactPath string                `json:"artifact_path"`
	Existing     bool                  `json:"existing"`
	Artifact     reviewrebind.Artifact `json:"artifact"`
}

func runVerifyRebind(args []string) error {
	fs := flag.NewFlagSet("verify rebind", flag.ContinueOnError)
	project := fs.String("project", ".", "Git project containing the reviewed and rebuilt commits")
	oldHead := fs.String("old-head", "", "reviewed commit or revision (required)")
	newHead := fs.String("new-head", "", "rebuilt commit or revision (required)")
	oldBase := fs.String("old-base", "", "explicit base of the reviewed delta (required for patch-id proof)")
	newBase := fs.String("new-base", "", "explicit base of the rebuilt delta (required for patch-id proof)")
	proofType := fs.String("proof", reviewrebind.ProofAuto, "proof type: auto, tree, or patch-id")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad verify rebind - prove and record review-safe Git equivalence

Usage:
  amq-squad verify rebind --project DIR --old-head A --new-head B [--proof tree]
  amq-squad verify rebind ... --proof patch-id --old-base BASE_A --new-base BASE_B

Proves identical Git trees, or identical scoped patch IDs (stable and whitespace-
sensitive, plus changed path/status/mode), between two full commit IDs, and records
only a successful proof, immutably, at .amq-squad/reviews/rebindings/<old>--<new>.json.
It is evidence, not approval: 'verify merge' re-runs the proof before carrying a
clean review to new-head, and CI must still be bound to new-head.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	for name, v := range map[string]string{"old-head": *oldHead, "new-head": *newHead} {
		if strings.TrimSpace(v) == "" {
			return usageErrorf("--%s is required", name)
		}
	}
	proof, err := reviewrebind.Prove(context.Background(), reviewrebind.Request{ProjectDir: *project, OldHead: *oldHead, NewHead: *newHead,
		OldBase: *oldBase, NewBase: *newBase, ProofType: strings.ReplaceAll(strings.TrimSpace(*proofType), "-", "_")})
	if err != nil {
		return fmt.Errorf("review rebinding refused: %w", err)
	}
	store, err := reviewrebind.OpenStore(proof.ProjectDir, true)
	if err != nil {
		return fmt.Errorf("open rebinding store: %w", err)
	}
	defer store.Close()
	id := reviewrebind.ID(reviewrebind.Artifact{OldHead: proof.OldHead, NewHead: proof.NewHead})
	artifact, readErr := store.Read(id)
	existing := readErr == nil
	switch {
	case readErr == nil:
		if err := reviewrebind.Verify(context.Background(), proof.ProjectDir, artifact); err != nil {
			return fmt.Errorf("existing rebinding artifact is invalid: %w", err)
		}
		if !reviewrebind.MatchesProof(artifact, proof) {
			return fmt.Errorf("existing rebinding artifact uses a different proof")
		}
	case !errors.Is(readErr, os.ErrNotExist):
		return fmt.Errorf("read existing rebinding artifact: %w", readErr)
	default:
		if artifact, err = reviewrebind.NewArtifact(proof, time.Now()); err != nil {
			return fmt.Errorf("build rebinding artifact: %w", err)
		}
		if existing, err = store.Write(artifact); err != nil {
			return fmt.Errorf("record rebinding artifact: %w", err)
		}
	}
	result := verifyRebindResult{OK: true, ArtifactID: id, ArtifactPath: reviewrebind.Path(proof.ProjectDir, id), Existing: existing, Artifact: artifact}
	if *jsonOut {
		return printJSONEnvelope("verify_rebind", result)
	}
	fmt.Printf("review rebinding %s: %s\n", map[bool]string{false: "recorded", true: "already recorded"}[existing], result.ArtifactPath)
	switch artifact.ProofType {
	case reviewrebind.ProofTree:
		fmt.Printf("proof: identical tree %s (%s -> %s)\n", artifact.OldTree, artifact.OldHead, artifact.NewHead)
	case reviewrebind.ProofPatchID:
		fmt.Printf("proof: identical scoped patch-id %s (%s -> %s)\n", artifact.OldPatch.StablePatchID, artifact.OldHead, artifact.NewHead)
	}
	return nil
}
