package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/commandevidence"
)

const maxCommandEvidenceLinks = 64

var verifyCommandEvidenceLinkRecord = verifyCommandEvidenceLink

// LinkCommandEvidenceForProfile atomically appends one bounded immutable
// evidence projection using the exact task-file digest accepted before the
// command ran. It never changes task execution state.
func LinkCommandEvidenceForProfile(projectDir, profile, session, id, expectedTaskSHA256, actor string, link CommandEvidenceLink, now time.Time) (Task, bool, error) {
	id, actor = strings.TrimSpace(id), strings.TrimSpace(actor)
	if !canonicalTaskID(id) || actor == "" || strings.TrimSpace(expectedTaskSHA256) == "" || now.IsZero() {
		return Task{}, false, fmt.Errorf("command evidence link requires canonical task id, task digest, actor, and timestamp")
	}
	if err := validateCommandEvidenceLink(projectDir, actor, link); err != nil {
		return Task{}, false, err
	}
	if err := verifyCommandEvidenceLinkRecord(projectDir, profile, session, id, link); err != nil {
		return Task{}, false, err
	}
	var out Task
	changed := false
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
		path := filepath.Join(dir, id+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != strings.TrimSpace(expectedTaskSHA256) {
			return fmt.Errorf("selected task changed before command evidence link")
		}
		var target Task
		if err := json.Unmarshal(b, &target); err != nil || target.ID != id {
			return fmt.Errorf("accepted task bytes do not contain exact task %s", id)
		}
		t := &target
		if t.Status != StatusInProgress || strings.TrimSpace(t.AssignedTo) != actor {
			return fmt.Errorf("task %s must remain in_progress for actor %s before evidence link", id, actor)
		}
		if authority := AuthorityActor(*t); authority != "" && authority != actor {
			return fmt.Errorf("task %s authority actor is %s, not %s", id, authority, actor)
		}
		for _, existing := range t.CommandEvidence {
			if existing.AttemptID != link.AttemptID {
				continue
			}
			if !sameCommandEvidenceLink(existing, link) {
				return fmt.Errorf("task %s command evidence attempt %s conflicts with existing link", id, link.AttemptID)
			}
			out = *t
			return nil
		}
		if len(t.CommandEvidence) >= maxCommandEvidenceLinks {
			return fmt.Errorf("task %s command evidence exceeds %d-link cap", id, maxCommandEvidenceLinks)
		}
		link.LinkedAt = now.UTC()
		t.CommandEvidence = append(t.CommandEvidence, link)
		t.UpdatedAt = now.UTC()
		latest, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		latestSum := sha256.Sum256(latest)
		if hex.EncodeToString(latestSum[:]) != strings.TrimSpace(expectedTaskSHA256) || string(latest) != string(b) {
			return fmt.Errorf("selected task changed immediately before command evidence commit")
		}
		if err := verifyCommandEvidenceLinkRecord(projectDir, profile, session, id, link); err != nil {
			return err
		}
		out, changed = *t, true
		return commitTasks(dir, []Task{*t}, now.UTC())
	})
	return out, changed, err
}

func validateCommandEvidenceLink(projectDir, actor string, link CommandEvidenceLink) error {
	if strings.TrimSpace(link.AttemptID) == "" || len(link.AttemptID) > 128 || strings.ContainsAny(link.AttemptID, `/\`) || link.Actor != actor || len(link.Actor) > 128 || strings.ContainsRune(link.Actor, 0) || len(link.Subject) > 240 || strings.ContainsRune(link.Subject, 0) {
		return fmt.Errorf("command evidence link identity exceeds bounded shape")
	}
	if !commandEvidenceProcessState(link.ProcessState) || !commandEvidenceFinalizationState(link.FinalizationState) {
		return fmt.Errorf("command evidence link state is incomplete")
	}
	for label, value := range map[string]string{
		"manifest": link.ManifestSHA256,
		"outcome":  link.OutcomeSHA256,
		"summary":  link.SummarySHA256,
	} {
		if !commandEvidenceSHA256(value) {
			return fmt.Errorf("command evidence %s digest is invalid", label)
		}
	}
	for _, path := range []string{link.ManifestPath, link.OutcomePath, link.SummaryPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("command evidence link path is not canonical absolute")
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) || !strings.HasPrefix(rel, filepath.Join(".amq-squad", "evidence", "commands")+string(filepath.Separator)) {
			return fmt.Errorf("command evidence link path escapes the project evidence namespace")
		}
	}
	projection, err := json.Marshal(link)
	if err != nil || len(projection) > 4096 {
		return fmt.Errorf("command evidence link exceeds 4096-byte projection cap")
	}
	return nil
}

func verifyCommandEvidenceLink(projectDir, profile, session, taskID string, link CommandEvidenceLink) error {
	store, err := commandevidence.NewStore(projectDir, profile, session)
	if err != nil {
		return err
	}
	result, err := store.Read(taskID, link.AttemptID)
	if err != nil {
		return fmt.Errorf("verify immutable command evidence: %w", err)
	}
	if result.Outcome == nil || result.Summary == nil || result.ManifestPath != link.ManifestPath || result.ManifestSHA256 != link.ManifestSHA256 || result.OutcomePath != link.OutcomePath || result.Outcome.OutcomeSHA256 != link.OutcomeSHA256 || result.SummaryPath != link.SummaryPath || result.Summary.SummarySHA256 != link.SummarySHA256 || result.Manifest.Actor != link.Actor || truncateCommandEvidenceSubject(result.Manifest.Subject) != link.Subject || result.Process.State != link.ProcessState || result.Outcome.FinalizationState != link.FinalizationState {
		return fmt.Errorf("command evidence link does not match exact immutable task/profile/session/attempt records")
	}
	expectedDir := filepath.Join(store.Root, "tasks", taskID, "attempts", link.AttemptID)
	if filepath.Dir(link.ManifestPath) != expectedDir || filepath.Base(link.ManifestPath) != "manifest.json" || filepath.Dir(link.OutcomePath) != expectedDir || filepath.Base(link.OutcomePath) != "outcome.json" || filepath.Dir(link.SummaryPath) != expectedDir || filepath.Base(link.SummaryPath) != "summary.json" {
		return fmt.Errorf("command evidence link paths do not match exact immutable record filenames")
	}
	return nil
}

func truncateCommandEvidenceSubject(value string) string {
	if len(value) <= 240 {
		return value
	}
	limit := 240
	for limit > 0 && value[limit]&0xc0 == 0x80 {
		limit--
	}
	return value[:limit]
}

func sameCommandEvidenceLink(a, b CommandEvidenceLink) bool {
	a.LinkedAt, b.LinkedAt = time.Time{}, time.Time{}
	return a == b
}

func commandEvidenceProcessState(value string) bool {
	switch value {
	case "succeeded", "exited_nonzero", "signaled", "spawn_failed", "wait_failed":
		return true
	default:
		return false
	}
}

func commandEvidenceFinalizationState(value string) bool {
	return value == "complete" || value == "artifact_incomplete"
}

func commandEvidenceSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && hex.EncodeToString(decoded) == strings.TrimPrefix(value, "sha256:")
}
