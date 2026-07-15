package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func migrationTransformFile(plan namespaceMigrationPlan, artifact namespaceMigrationArtifact, rel string, payload []byte) ([]byte, bool, error) {
	slashRel := filepath.ToSlash(rel)
	switch artifact.Name {
	case "amq_root":
		switch {
		case strings.HasSuffix(slashRel, "/extensions/io.github.omriariav.amq-squad/launch.json"):
			return rewriteMigrationLaunchRecord(plan, payload)
		case strings.HasSuffix(slashRel, "/extensions/io.github.omriariav.amq-squad/bootstrap/ack.json"):
			return rewriteMigrationBootstrapAck(plan, payload)
		default:
			return payload, true, nil
		}
	case "delivery_receipts":
		return rewriteMigrationDeliveryReceipt(plan, rel, payload)
	case "goal_attempts":
		return rewriteMigrationGoalArtifact(plan, rel, payload)
	case "operator_loop":
		return rewriteMigrationOperatorLease(plan, payload)
	default:
		return payload, true, nil
	}
}

func rewriteMigrationLaunchRecord(plan namespaceMigrationPlan, payload []byte) ([]byte, bool, error) {
	var rec launch.Record
	if err := decodeMigrationJSON(payload, &rec); err != nil {
		return nil, false, fmt.Errorf("parse launch record: %w", err)
	}
	if !squadnamespace.ProfilesEqual(rec.TeamProfile, plan.Source.Profile) || strings.TrimSpace(rec.Session) != plan.Source.Session || filepath.Clean(rec.Root) != filepath.Clean(plan.Source.AMQRoot) {
		return nil, false, fmt.Errorf("launch record identity does not match source namespace for %s", rec.Handle)
	}
	rec.TeamProfile = plan.Target.Profile
	rec.Session = plan.Target.Session
	rec.Root = plan.Target.AMQRoot
	rec.BaseRoot = namespaceMigrationBaseRoot(plan.Target)
	rec.RootSource = "namespace_migration"
	// Runtime ownership cannot survive a stopped migration. Durable replay
	// inputs (conversation, argv, model, goal binding) remain intact.
	rec.AgentPID = 0
	rec.WakePID = 0
	rec.AgentTTY = ""
	rec.LauncherPaneID = ""
	rec.Tmux = nil
	rec.Terminal = nil
	if rec.GoalBinding != nil && rec.GoalBinding.NativeGoal && strings.TrimSpace(rec.GoalBinding.Command) != "" {
		goal, attemptID, err := parseGeneratedGoalBinding(rec.GoalBinding.Command)
		if err != nil {
			return nil, false, fmt.Errorf("launch record native goal binding is malformed: %w", err)
		}
		targetTeam, err := team.ReadProfile(plan.ProjectDir, plan.Target.Profile)
		if err != nil {
			return nil, false, err
		}
		rec.GoalBinding.Command = nativeGoalControlPrompt(goal, targetTeam, plan.Target.Profile, plan.Target.Session, rec.Role, attemptID)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	return append(b, '\n'), true, err
}

func rewriteMigrationBootstrapAck(plan namespaceMigrationPlan, payload []byte) ([]byte, bool, error) {
	var marker bootstrapack.Marker
	if err := decodeMigrationJSON(payload, &marker); err != nil {
		return nil, false, fmt.Errorf("parse bootstrap acknowledgement: %w", err)
	}
	if !squadnamespace.ProfilesEqual(marker.Profile, plan.Source.Profile) || marker.Session != plan.Source.Session || filepath.Clean(marker.Root) != filepath.Clean(plan.Source.AMQRoot) {
		return nil, false, fmt.Errorf("bootstrap acknowledgement does not match source namespace")
	}
	marker.Profile = plan.Target.Profile
	marker.Session = plan.Target.Session
	marker.Root = plan.Target.AMQRoot
	b, err := json.MarshalIndent(marker, "", "  ")
	return append(b, '\n'), true, err
}

func rewriteMigrationDeliveryReceipt(plan namespaceMigrationPlan, rel string, payload []byte) ([]byte, bool, error) {
	if filepath.Ext(rel) != ".json" {
		return nil, false, fmt.Errorf("unknown delivery receipt content %s", rel)
	}
	var receipt deliveryReceiptData
	if err := decodeMigrationJSON(payload, &receipt); err != nil {
		return nil, false, fmt.Errorf("parse delivery receipt %s: %w", rel, err)
	}
	if !squadnamespace.ProfilesEqual(receipt.Target.Profile, plan.Source.Profile) || receipt.Target.Session != plan.Source.Session || receipt.Target.NamespaceID != plan.Source.ID {
		return nil, false, fmt.Errorf("delivery receipt %s does not match source namespace", rel)
	}
	receipt.Target.Profile = plan.Target.Profile
	receipt.Target.Session = plan.Target.Session
	receipt.Target.NamespaceID = plan.Target.ID
	if receipt.Root != "" {
		if filepath.Clean(receipt.Root) != filepath.Clean(plan.Source.AMQRoot) {
			return nil, false, fmt.Errorf("delivery receipt %s has unexpected AMQ root", rel)
		}
		receipt.Root = plan.Target.AMQRoot
	}
	receipt.PaneID = ""
	receipt.Path = filepath.Join(migrationPlanArtifact(plan, "delivery_receipts").Target, filepath.Base(rel))
	b, err := json.MarshalIndent(receipt, "", "  ")
	return append(b, '\n'), true, err
}

func rewriteMigrationGoalArtifact(plan namespaceMigrationPlan, rel string, payload []byte) ([]byte, bool, error) {
	base := filepath.Base(rel)
	if filepath.Ext(base) != ".json" {
		return nil, false, fmt.Errorf("unknown goal-attempt content %s", rel)
	}
	if strings.HasSuffix(base, ".claim.json") {
		var claim goalAttemptClaim
		if err := decodeMigrationJSON(payload, &claim); err != nil {
			return nil, false, fmt.Errorf("parse goal claim %s: %w", rel, err)
		}
		return payload, true, nil
	}
	if strings.HasSuffix(base, ".consumed.json") {
		var consumed resumeGoalTransitionConsumed
		if err := decodeMigrationJSON(payload, &consumed); err != nil {
			return nil, false, fmt.Errorf("parse consumed goal transition %s: %w", rel, err)
		}
		return payload, true, nil
	}
	if strings.HasSuffix(base, ".bound.json") {
		var bound resumeGoalTransitionBound
		if err := decodeMigrationJSON(payload, &bound); err != nil {
			return nil, false, fmt.Errorf("parse goal transition bound %s: %w", rel, err)
		}
		handle, err := migrationBoundTransitionHandle(plan, rel, bound.TransitionID)
		if err != nil {
			return nil, false, err
		}
		digest, mod, err := migratedLaunchGeneration(plan, handle)
		if err != nil {
			return nil, false, err
		}
		bound.LaunchRecordDigest = digest
		bound.LaunchRecordModTime = mod
		b, err := json.MarshalIndent(bound, "", "  ")
		return append(b, '\n'), true, err
	}
	if strings.HasPrefix(base, ".resume-redelivery-") {
		var tr resumeGoalTransitionRecord
		if err := decodeMigrationJSON(payload, &tr); err != nil {
			return nil, false, fmt.Errorf("parse goal transition %s: %w", rel, err)
		}
		if !squadnamespace.ProfilesEqual(tr.Profile, plan.Source.Profile) || tr.Session != plan.Source.Session || filepath.Clean(tr.Project) != filepath.Clean(plan.ProjectDir) {
			return nil, false, fmt.Errorf("goal transition %s does not match source namespace", rel)
		}
		tr.Profile = plan.Target.Profile
		tr.Session = plan.Target.Session
		if tr.MemberSession == plan.Source.Session {
			tr.MemberSession = plan.Target.Session
		}
		targetProfile := team.ProfilePath(plan.ProjectDir, plan.Target.Profile)
		teamDigest, teamMod, err := readGoalFileGeneration(targetProfile)
		if err != nil {
			return nil, false, err
		}
		launchDigest, launchMod, err := migratedLaunchGeneration(plan, tr.Handle)
		if err != nil {
			return nil, false, err
		}
		tr.TeamRecordDigest, tr.TeamRecordModTime = teamDigest, teamMod
		tr.LaunchRecordDigest, tr.LaunchRecordModTime = launchDigest, launchMod
		b, err := json.MarshalIndent(tr, "", "  ")
		return append(b, '\n'), true, err
	}
	var attempt goalAttemptRecord
	if err := decodeMigrationJSON(payload, &attempt); err != nil {
		return nil, false, fmt.Errorf("parse goal attempt %s: %w", rel, err)
	}
	if !squadnamespace.ProfilesEqual(attempt.Profile, plan.Source.Profile) || attempt.Session != plan.Source.Session || attempt.Namespace.ID != plan.Source.ID || filepath.Clean(attempt.Project) != filepath.Clean(plan.ProjectDir) {
		return nil, false, fmt.Errorf("goal attempt %s does not match source namespace", rel)
	}
	attempt.Profile = plan.Target.Profile
	attempt.Session = plan.Target.Session
	attempt.Namespace = plan.Target
	b, err := json.MarshalIndent(attempt, "", "  ")
	return append(b, '\n'), true, err
}

func namespaceMigrationBaseRoot(ref squadnamespace.Ref) string {
	if squadnamespace.NormalizeProfile(ref.Profile) == team.DefaultProfile {
		return filepath.Dir(ref.AMQRoot)
	}
	return ref.AMQRoot
}

func migrationBoundTransitionHandle(plan namespaceMigrationPlan, rel, transitionID string) (string, error) {
	base := filepath.Base(rel)
	mainBase := strings.TrimSuffix(base, ".bound.json") + ".json"
	path := filepath.Join(migrationPlanArtifact(plan, "goal_attempts").Source, filepath.Dir(rel), mainBase)
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read paired goal transition for %s: %w", rel, err)
	}
	var tr resumeGoalTransitionRecord
	if err := decodeMigrationJSON(payload, &tr); err != nil {
		return "", fmt.Errorf("parse paired goal transition for %s: %w", rel, err)
	}
	if tr.TransitionID != transitionID || strings.TrimSpace(tr.Handle) == "" {
		return "", fmt.Errorf("goal transition bound %s does not match its paired transition", rel)
	}
	return tr.Handle, nil
}

func migratedLaunchGeneration(plan namespaceMigrationPlan, handle string) (string, int64, error) {
	launchRoot := filepath.Join(plan.Source.AMQRoot, "agents")
	if handle != "" {
		launchRoot = filepath.Join(launchRoot, handle, "extensions", launch.LayerName, launch.FileName)
		return migratedLaunchFileGeneration(plan, launchRoot, handle)
	}
	entries, err := os.ReadDir(launchRoot)
	if err != nil {
		return "", 0, err
	}
	for _, entry := range entries {
		path := filepath.Join(launchRoot, entry.Name(), "extensions", launch.LayerName, launch.FileName)
		if _, err := os.Stat(path); err == nil {
			return migratedLaunchFileGeneration(plan, path, entry.Name())
		}
	}
	return "", 0, fmt.Errorf("no launch record available for goal transition")
}

func migratedLaunchFileGeneration(plan namespaceMigrationPlan, path, expectedHandle string) (string, int64, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	var source launch.Record
	if err := decodeMigrationJSON(payload, &source); err != nil {
		return "", 0, fmt.Errorf("parse launch generation %s: %w", path, err)
	}
	if strings.TrimSpace(source.Handle) != strings.TrimSpace(expectedHandle) {
		return "", 0, fmt.Errorf("launch generation %s belongs to handle %q, want %q", path, source.Handle, expectedHandle)
	}
	rewritten, _, err := rewriteMigrationLaunchRecord(plan, payload)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return digestBytes(rewritten), info.ModTime().UnixNano(), nil
}

func rewriteMigrationOperatorLease(plan namespaceMigrationPlan, payload []byte) ([]byte, bool, error) {
	var lease operatorLoopLeaseFile
	if err := decodeMigrationJSON(payload, &lease); err != nil {
		return nil, false, err
	}
	if !squadnamespace.ProfilesEqual(lease.Profile, plan.Source.Profile) || lease.Session != plan.Source.Session || lease.NamespaceID != plan.Source.ID {
		return nil, false, fmt.Errorf("operator lease does not match source namespace")
	}
	lease.Profile = plan.Target.Profile
	lease.Session = plan.Target.Session
	lease.NamespaceID = plan.Target.ID
	lease.Owner = ""
	lease.OwnerID = ""
	lease.LeaseTTL = ""
	lease.LeaseExpiresAt = time.Time{}
	b, err := json.MarshalIndent(lease, "", "  ")
	return append(b, '\n'), true, err
}

func rewriteNamespaceNotifyState(path string, source, target squadnamespace.Ref) ([]byte, bool, error) {
	state, err := readNotifyState(path)
	if err != nil {
		return nil, false, err
	}
	sourcePrefix := source.Profile + "/" + source.Session + "\x00"
	targetPrefix := target.Profile + "/" + target.Session + "\x00"
	changed := false
	for key, value := range state.Items {
		if !strings.HasPrefix(key, sourcePrefix) {
			continue
		}
		targetKey := targetPrefix + strings.TrimPrefix(key, sourcePrefix)
		if _, exists := state.Items[targetKey]; exists {
			return nil, false, fmt.Errorf("notify state target key collision: %q", targetKey)
		}
		delete(state.Items, key)
		state.Items[targetKey] = value
		changed = true
	}
	b, err := json.MarshalIndent(state, "", "  ")
	return append(b, '\n'), changed, err
}

func decodeMigrationJSON(payload []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err == io.EOF {
		return nil
	} else if err == nil {
		return fmt.Errorf("trailing JSON value")
	} else {
		return err
	}
}
