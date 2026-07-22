package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var stagedAdmissionResolveAuthorizer = resolveVerifiedCurrentPaneActor

type stagedAdmissionData struct {
	Project           string                      `json:"project"`
	Profile           string                      `json:"profile"`
	Session           string                      `json:"session"`
	Namespace         string                      `json:"namespace"`
	Role              string                      `json:"role"`
	Handle            string                      `json:"handle"`
	ClaimID           string                      `json:"claim_id"`
	SupersedesClaimID string                      `json:"supersedes_claim_id,omitempty"`
	AcceptedIdentity  preparedRunMemberIdentity   `json:"accepted_identity"`
	EffectiveIdentity preparedRunMemberIdentity   `json:"effective_identity"`
	Authorizer        preparedRunStagedAuthorizer `json:"authorizer"`
	Lifecycle         preparedRunStagedLifecycle  `json:"lifecycle"`
}

func runTeamMemberStagedAdmission(args []string, replacement bool) error {
	roleID, rest, ok := peelPositional(args)
	if !ok {
		verb := "admit"
		if replacement {
			verb = "replace"
		}
		return usageErrorf("a staged role is required, e.g. 'team member %s reviewer --actor-mode review'", verb)
	}
	roleID = strings.ToLower(strings.TrimSpace(roleID))
	if err := team.ValidateRoleID(roleID); err != nil {
		return fmt.Errorf("role: %w", err)
	}
	name := "team member admit"
	if replacement {
		name = "team member replace"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "prepared workstream session")
	actorModeFlag := fs.String("actor-mode", "", "required effective actor capability: review or implementation")
	claimFlag := fs.String("claim", "", "exact active claim ID superseded by replacement")
	reasonFlag := fs.String("reason", "", "auditable admission or replacement reason")
	jsonOut := fs.Bool("json", false, "emit the immutable staged claim as JSON")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	actorMode := strings.ToLower(strings.TrimSpace(*actorModeFlag))
	if actorMode != team.ActorModeReview && actorMode != team.ActorModeImplementation {
		return usageErrorf("--actor-mode is required and must be %s or %s", team.ActorModeReview, team.ActorModeImplementation)
	}
	claimID := strings.TrimSpace(*claimFlag)
	if replacement && claimID == "" {
		return usageErrorf("team member replace requires --claim with the exact active claim ID")
	}
	if !replacement && claimID != "" {
		return usageErrorf("--claim is valid only for team member replace")
	}

	projectDir, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	cfg, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		session = inheritedSession(cfg)
	}
	if session == "" {
		return usageErrorf("--session is required when the profile has no single inherited workstream")
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(projectDir, profile, session)
	if err != nil {
		return fmt.Errorf("read accepted prepared generation: %w", err)
	}
	accepted, ok := manifest.StagedMembers[roleID]
	if !ok || !containsRole(manifest.StagedRoster, roleID) {
		return usageErrorf("role %q is not staged in prepared generation %s", roleID, manifest.Generation)
	}
	if _, err := narrowedPreparedStagedIdentity(accepted, actorMode); err != nil {
		return err
	}
	authorizer, err := stagedAdmissionResolveAuthorizer(projectDir, profile, session, cfg)
	if err != nil {
		return fmt.Errorf("verify staged admission authorizer: %w", err)
	}
	if squadnamespace.NormalizeProfile(authorizer.Profile) != squadnamespace.NormalizeProfile(profile) || authorizer.Session != session {
		return fmt.Errorf("verified authorizer belongs to %s/%s, not %s/%s", authorizer.Profile, authorizer.Session, profile, session)
	}
	request := preparedRunStagedAdmissionRequest{
		Role: roleID, Handle: accepted.Handle, AuthorizingRole: authorizer.Role, AuthorizingHandle: authorizer.Handle,
		ActorMode: actorMode, SupersedesClaimID: claimID, LifecycleReason: strings.TrimSpace(*reasonFlag),
	}
	claim, err := admitPreparedRunStagedClaim(projectDir, profile, session, preparedRunTokenFromSnapshot(manifest, digest), request)
	if err != nil {
		return err
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(projectDir, profile, session, manifest.Generation, roleID))
	if err != nil {
		return fmt.Errorf("read authoritative staged claim state: %w", err)
	}
	if pointer.ClaimID != claim.ClaimID {
		return fmt.Errorf("authoritative staged claim state changed after admission")
	}
	lifecycle := claim.Lifecycle
	lifecycle.State = pointer.LifecycleState
	data := stagedAdmissionData{
		Project: projectDir, Profile: profile, Session: session, Namespace: manifest.Namespace,
		Role: claim.Role, Handle: claim.Handle, ClaimID: claim.ClaimID, SupersedesClaimID: claim.Lifecycle.SupersedesClaimID,
		AcceptedIdentity: claim.Accepted, EffectiveIdentity: claim.Effective, Authorizer: claim.Authorizer, Lifecycle: lifecycle,
	}
	if *jsonOut {
		return printJSONEnvelope("staged_member_claim", data)
	}
	verb := "admitted"
	if replacement {
		verb = "replaced"
	}
	fmt.Fprintf(os.Stdout, "%s staged member %s/%s with immutable claim %s (actor_mode=%s)\n", verb, claim.Role, claim.Handle, claim.ClaimID, claim.Effective.ActorMode)
	if claim.Lifecycle.SupersedesClaimID != "" {
		fmt.Fprintf(os.Stdout, "supersedes: %s\n", claim.Lifecycle.SupersedesClaimID)
	}
	fmt.Fprintf(os.Stdout, "authorizer: %s/%s launch=%s\n", claim.Authorizer.Role, claim.Authorizer.Handle, claim.Authorizer.LaunchID)
	fmt.Fprintln(os.Stdout, "claim is recorded; terminal launch remains pending verified runtime preflight")
	return nil
}
