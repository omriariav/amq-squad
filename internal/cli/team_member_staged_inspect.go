package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func readPreparedRunStagedTransition(path string) (preparedRunStagedTransition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedRunStagedTransition{}, err
	}
	var transition preparedRunStagedTransition
	if err := json.Unmarshal(data, &transition); err != nil {
		return preparedRunStagedTransition{}, err
	}
	if transition.SchemaVersion != preparedRunStagedClaimSchema || transition.TransitionID == "" || transition.ClaimID == "" || transition.State == "" || !transition.GenerationRef.complete() {
		return preparedRunStagedTransition{}, fmt.Errorf("invalid staged transition %s", path)
	}
	return transition, nil
}

type preparedRunStagedStatusData struct {
	Project string                        `json:"project"`
	Profile string                        `json:"profile"`
	Session string                        `json:"session"`
	Claim   preparedRunStagedClaim        `json:"claim"`
	Current preparedRunStagedClaimPointer `json:"current"`
}

type preparedRunStagedHistoryData struct {
	Project     string                        `json:"project"`
	Profile     string                        `json:"profile"`
	Session     string                        `json:"session"`
	Role        string                        `json:"role"`
	Current     preparedRunStagedClaimPointer `json:"current"`
	Claims      []preparedRunStagedClaim      `json:"claims"`
	Transitions []preparedRunStagedTransition `json:"transitions"`
}

func runTeamMemberStagedInspect(args []string, history bool) error {
	roleID, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a staged role is required")
	}
	roleID = strings.ToLower(strings.TrimSpace(roleID))
	if err := team.ValidateRoleID(roleID); err != nil {
		return err
	}
	name := "team member status"
	if history {
		name = "team member history"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "prepared workstream session")
	jsonFlag := fs.Bool("json", false, "emit schema-versioned JSON")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	project, profile, err := resolveExistingTeamProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return err
	}
	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		session = inheritedSession(tm)
	}
	if session == "" {
		return usageErrorf("--session is required when the profile has no single inherited workstream")
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err != nil {
		return err
	}
	if !containsRole(manifest.StagedRoster, roleID) {
		return usageErrorf("role %q is not staged in prepared generation %s", roleID, manifest.Generation)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, profile, session, token.Generation, roleID))
	if err != nil {
		return fmt.Errorf("read authoritative staged status: %w", err)
	}
	claim, err := preparedRunStagedClaimForPointer(project, profile, session, token, roleID, pointer)
	if err != nil {
		return err
	}
	if err := validatePreparedRunStagedPointerLifecycle(project, profile, session, token, claim, pointer); err != nil {
		return fmt.Errorf("validate authoritative staged lifecycle: %w", err)
	}
	if !history {
		data := preparedRunStagedStatusData{Project: project, Profile: profile, Session: session, Claim: claim, Current: pointer}
		if *jsonFlag {
			return printJSONEnvelope("staged_member_status", data)
		}
		fmt.Fprintf(os.Stdout, "%s/%s claim=%s lifecycle=%s actor_mode=%s authorizer=%s/%s launch=%s\n", claim.Role, claim.Handle, claim.ClaimID, pointer.LifecycleState, claim.Effective.ActorMode, claim.Authorizer.Role, claim.Authorizer.Handle, claim.Authorizer.LaunchID)
		return nil
	}
	claims, transitions, err := readPreparedRunStagedHistory(project, profile, session, token, roleID)
	if err != nil {
		return err
	}
	data := preparedRunStagedHistoryData{Project: project, Profile: profile, Session: session, Role: roleID, Current: pointer, Claims: claims, Transitions: transitions}
	if *jsonFlag {
		return printJSONEnvelope("staged_member_history", data)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tCLAIM\tSTATE\tSUPERSEDED_BY\tREASON")
	for _, transition := range transitions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", transition.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), transition.ClaimID, transition.State, orDash(transition.SupersededBy), orDash(transition.Reason))
	}
	return w.Flush()
}

func readPreparedRunStagedHistory(project, profile, session string, token preparedRunToken, role string) ([]preparedRunStagedClaim, []preparedRunStagedTransition, error) {
	claimEntries, err := os.ReadDir(preparedRunStagedClaimsDir(project, profile, session, token.Generation, role))
	if err != nil {
		return nil, nil, err
	}
	claims := make([]preparedRunStagedClaim, 0, len(claimEntries))
	for _, entry := range claimEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil, nil, preparedRunIdentityMismatchf("unexpected staged claim history entry %s", entry.Name())
		}
		if strings.HasSuffix(entry.Name(), ".consumed.json") {
			continue
		}
		claim, err := readPreparedRunStagedClaim(filepath.Join(preparedRunStagedClaimsDir(project, profile, session, token.Generation, role), entry.Name()))
		if err != nil {
			return nil, nil, err
		}
		claims = append(claims, claim)
	}
	transitions := []preparedRunStagedTransition{}
	for _, claim := range claims {
		entries, err := os.ReadDir(preparedRunStagedTransitionsDir(project, profile, session, token.Generation, role, claim.ClaimID))
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				return nil, nil, preparedRunIdentityMismatchf("unexpected staged transition history entry %s", entry.Name())
			}
			transition, err := readPreparedRunStagedTransition(filepath.Join(preparedRunStagedTransitionsDir(project, profile, session, token.Generation, role, claim.ClaimID), entry.Name()))
			if err != nil {
				return nil, nil, err
			}
			transitions = append(transitions, transition)
		}
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].CreatedAt.Before(claims[j].CreatedAt) })
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].CreatedAt.Equal(transitions[j].CreatedAt) {
			return transitions[i].TransitionID < transitions[j].TransitionID
		}
		return transitions[i].CreatedAt.Before(transitions[j].CreatedAt)
	})
	return claims, transitions, nil
}
