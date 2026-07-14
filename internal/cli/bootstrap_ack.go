package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func runBootstrap(args []string) error {
	if len(args) == 0 || args[0] != "ack" {
		return usageErrorf("bootstrap requires subcommand: ack")
	}
	return runBootstrapAck(args[1:])
}

func runBootstrapAck(args []string) error {
	fs := flag.NewFlagSet("bootstrap ack", flag.ContinueOnError)
	skillVersion := fs.String("skill-version", "", "loaded amq-squad skill version")
	stepsRaw := fs.String("steps", "", "completed canonical startup steps")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: amq-squad bootstrap ack --skill-version VERSION --steps startup-files,initial-drain,context-review")
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	root := strings.TrimSpace(os.Getenv("AM_ROOT"))
	handle := strings.TrimSpace(os.Getenv("AM_ME"))
	if root == "" || handle == "" || !filepath.IsAbs(root) {
		return usageErrorf("bootstrap ack requires the launch-provided absolute AM_ROOT and AM_ME environment")
	}
	root = filepath.Clean(root)
	agentDir := filepath.Join(root, "agents", handle)
	rec, err := launch.Read(agentDir)
	if err != nil {
		return fmt.Errorf("read current launch identity: %w", err)
	}
	if rec.BootstrapExpectation == nil || !rec.BootstrapExpectation.Required {
		return usageErrorf("the current launch does not require bootstrap acknowledgement")
	}
	if rec.Handle != handle || filepath.Clean(rec.Root) != root || strings.TrimSpace(rec.Session) == "" {
		return usageErrorf("AM_ME/AM_ROOT do not match the current launch record")
	}
	teamHome := strings.TrimSpace(rec.TeamHome)
	if teamHome == "" {
		teamHome = rec.CWD
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(teamHome, rec.TeamProfile, rec.Session), rec.Handle)
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(teamHome, rec.TeamProfile, rec.Session)
	if err != nil {
		return err
	}
	defer admission.close()
	currentRec, err := launch.Read(agentDir)
	if err != nil {
		return fmt.Errorf("bootstrap ack refused: launch identity disappeared before admission: %w", err)
	}
	currentTeamHome := strings.TrimSpace(currentRec.TeamHome)
	if currentTeamHome == "" {
		currentTeamHome = currentRec.CWD
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(currentTeamHome, currentRec.TeamProfile, currentRec.Session), currentRec.Handle)
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("bootstrap ack", initialIdentity, currentIdentity); err != nil {
		return err
	}
	if currentRec.StartedAt != rec.StartedAt || currentRec.Root != rec.Root || currentRec.BootstrapExpectation == nil || rec.BootstrapExpectation == nil || currentRec.BootstrapExpectation.LaunchID != rec.BootstrapExpectation.LaunchID {
		return usageErrorf("bootstrap ack refused: launch generation changed before admission")
	}
	rec, teamHome = currentRec, currentTeamHome
	if err := ensureNoNamespaceMigration("bootstrap ack", teamHome, rec.TeamProfile, rec.Session); err != nil {
		return err
	}
	cfg, err := team.ReadProfile(teamHome, rec.TeamProfile)
	if err != nil {
		return fmt.Errorf("read current roster identity: %w", err)
	}
	actor, err := verifiedCurrentRosterActor(teamHome, rec.TeamProfile, rec.Session, cfg)
	if err != nil {
		return fmt.Errorf("verify current bootstrap actor: %w", err)
	}
	if actor.Handle != rec.Handle || !rootsMatch(actor.Root, rec.Root) || actor.Session != rec.Session || !launchRecordProfileMatches(rec, actor.Profile) {
		return usageErrorf("verified actor does not match the current launch identity")
	}
	steps := splitBootstrapSteps(*stepsRaw)
	if !bootstrapack.CompleteSteps(steps) {
		return usageErrorf("--steps must be exactly startup-files,initial-drain,context-review")
	}
	if strings.TrimSpace(*skillVersion) == "" {
		return usageErrorf("--skill-version is required")
	}
	marker := bootstrapack.Marker{
		LaunchID: rec.BootstrapExpectation.LaunchID, PromptVersion: rec.BootstrapExpectation.PromptVersion,
		AcknowledgedAt: time.Now().UTC(), Handle: rec.Handle, Role: rec.Role, Profile: rec.TeamProfile,
		Session: rec.Session, Root: root, SkillVersion: strings.TrimSpace(*skillVersion), Steps: steps,
	}
	// Re-read immediately before the atomic marker replacement. A reorient that
	// rotated launch identity while this command was running must not be attested.
	current, err := launch.Read(agentDir)
	if err != nil || current.BootstrapExpectation == nil || current.BootstrapExpectation.LaunchID != marker.LaunchID {
		return fmt.Errorf("current launch identity changed before bootstrap acknowledgement")
	}
	if err := bootstrapack.Write(agentDir, marker); err != nil {
		return err
	}
	quietNotice("bootstrap acknowledged for %s (%s)\n", rec.Handle, rec.Session)
	return nil
}

func splitBootstrapSteps(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}
