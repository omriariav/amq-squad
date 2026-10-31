package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/releaseflow"
)

const releaseUsage = `amq-squad release - the release gate flow (tag and release stop for the owner)

Usage:
  amq-squad release prepare --repository OWNER/NAME --tag TAG --commit SHA --notes-file PATH [--notes TEXT] [--branch BRANCH] [context flags] [--json]
  amq-squad release publish --repository OWNER/NAME --tag TAG --commit SHA --notes-file PATH [--notes TEXT] [--branch BRANCH] [context flags] [--json]

prepare writes the release into the compoundrelease store and raises two
typed gates through the core, tag ("git tag TAG SHA on OWNER/NAME") and
github_release ("gh release create TAG on OWNER/NAME"); the owner answers each
in the gate thread. publish runs, under the owner's gh outside any seat, the
tag and then the release, each only with its gate's verified owner answer; a
child without one is refused and nothing after it runs. A child that ran is
never run again.

--notes-file names the release notes document: its content is the GitHub
release body, and its digest is recorded in the release at prepare; publish
refuses a file whose content is not the prepared one (release_body_mismatch).
--notes is the gate note, shown to the owner in the gate question only.
Before any gate is raised, prepare resolves --commit in this repository
(commit_unknown) and requires the remote --branch (default main) to contain
it (commit_not_on_branch).

Exit codes: 0 done, 1 refused or error.
`

func runRelease(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, releaseUsage)
		if len(args) == 0 {
			return usageErrorf("release requires prepare or publish")
		}
		return nil
	}
	verb := args[0]
	if verb != "prepare" && verb != "publish" {
		return unknownSubcommandError("release", verb, "prepare", "publish")
	}
	g := newGateStoreFlags("release "+verb, releaseUsage)
	repo := g.fs.String("repository", "", "owner/name")
	tag := g.fs.String("tag", "", "the tag")
	commit := g.fs.String("commit", "", "the full commit SHA the tag names")
	notes := g.fs.String("notes", "", "the gate note shown to the owner in the gate question (one line)")
	notesFile := g.fs.String("notes-file", "", "the release notes document: its content is the GitHub release body")
	branch := g.fs.String("branch", "main", "the remote branch that must contain --commit")
	ghBin := g.fs.String("gh", "gh", "gh executable (the owner's identity)")
	sel, err := g.resolve(args[1:])
	if err != nil {
		return err
	}
	if os.Getenv("AMQ_SQUAD_SEAT") != "" {
		return fmt.Errorf("release: a seat process holds no release path (R33); the owner's harness runs the release")
	}
	if *notesFile == "" {
		return usageErrorf("release %s requires --notes-file (the release body)", verb)
	}
	body, err := os.ReadFile(*notesFile)
	if err != nil {
		return usageErrorf("release %s: --notes-file: %v", verb, err)
	}
	ctx := context.Background()
	core, err := gateCore(ctx, sel, verb == "prepare")
	if core == nil {
		return fmt.Errorf("release: %w", err)
	}
	transportErr := err
	scope := compoundrelease.Scope{ProjectDir: sel.ProjectDir, Profile: sel.Profile, Session: sel.Session,
		NamespaceGeneration: firstOf(sel.NamespaceGeneration, "none"), ParentGate: "gate/release-" + strings.ToLower(strings.ReplaceAll(*tag, "+", "-"))}
	store, err := compoundrelease.Open(scope, verb == "prepare")
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	defer store.Close()
	esc := mergetrain.StoreEscalator{Store: core.Store, Identity: core.Identity(), Now: hookNow, Squad: mergetrain.SquadOf(sel.Root)}
	if transportErr == nil {
		esc.Send = core.QuestionSender(ctx)
	}
	flow := &releaseflow.Flow{Release: store, Gates: core.Store, Raise: esc.Raise, Lead: firstOf(sel.Handle, "lead"), Squad: mergetrain.SquadOf(sel.Root),
		Exec: &releaseflow.GH{Bin: *ghBin}, Body: string(body), RepoDir: sel.ProjectDir, Branch: *branch,
		Accepted: func(rec gatestore.Record) error {
			trust, err := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
			if err != nil {
				return err
			}
			return gateverify.Verifier{Trust: trust, Pin: core.Pin, Namespace: core.Namespace}.Accepted(core.Store, rec)
		}}
	spec, err := releaseflow.Spec(scope, flow.Lead, "owner", *repo, *tag, *commit, *notes, string(body))
	if err != nil {
		return usageErrorf("release %s: %v", verb, err)
	}
	if verb == "prepare" {
		snap, err := flow.Prepare(spec)
		if err != nil {
			return fmt.Errorf("release prepare: %w", err)
		}
		data := releasePrepareData{ReleaseID: snap.Prepared.ReleaseID, State: snap.Pointer.State}
		for i, c := range snap.Prepared.Children {
			data.Gates = append(data.Gates, releaseGate{Role: c.Role, Target: c.Target, GateID: snap.Active.Children[i].QuestionMessageID,
				QuestionSent: core.Store.QuestionPublished(snap.Active.Children[i].QuestionMessageID)})
		}
		if transportErr != nil {
			data.QuestionError = transportErr.Error()
		}
		if *g.jsonOut {
			return printJSONEnvelope("release_prepare", data)
		}
		fmt.Printf("release %s %s\n", data.ReleaseID, data.State)
		for _, gt := range data.Gates {
			fmt.Printf("%s\tgate %s\t%s\tquestion sent %v\n", gt.Role, gt.GateID, gt.Target, gt.QuestionSent)
		}
		return nil
	}
	if _, err := store.Create(spec); err != nil { // the same spec, or the store refuses
		return fmt.Errorf("release publish: %w", err)
	}
	steps, err := flow.Publish(ctx, sel.Root)
	if *g.jsonOut {
		if perr := printJSONEnvelope("release_publish", map[string]any{"steps": steps, "error": errString(err)}); perr != nil {
			return perr
		}
	} else {
		for _, s := range steps {
			fmt.Printf("%s\t%s\tgate %s\t%s %s\n", s.Role, s.Status, s.GateID, s.Result, s.Detail)
		}
	}
	if err != nil {
		return fmt.Errorf("release publish: %w", err)
	}
	return nil
}

type releaseGate struct {
	Role         string `json:"role"`
	Target       string `json:"target"`
	GateID       string `json:"gate_id"`
	QuestionSent bool   `json:"question_sent"`
}

type releasePrepareData struct {
	ReleaseID     string        `json:"release_id"`
	State         string        `json:"state"`
	Gates         []releaseGate `json:"gates"`
	QuestionError string        `json:"question_error,omitempty"`
}
