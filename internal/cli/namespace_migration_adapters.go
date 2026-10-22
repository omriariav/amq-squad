package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
	case "goal_attempts":
		return nil, false, fmt.Errorf("goal attempt artifacts are no longer migrated: %s", rel)
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

func namespaceMigrationBaseRoot(ref squadnamespace.Ref) string {
	if squadnamespace.NormalizeProfile(ref.Profile) == team.DefaultProfile {
		return filepath.Dir(ref.AMQRoot)
	}
	return ref.AMQRoot
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
