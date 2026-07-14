package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var namespaceMigrationFailpoint = func(string) error { return nil }
var namespaceMigrationBeforeAdmission = func(namespaceMigrationPlan) error { return nil }
var namespaceMigrationBeforeReservation = func(namespaceMigrationPlan) error { return nil }

type namespaceMigrationLocks struct {
	locks []*flock.Exclusive
}

func (l *namespaceMigrationLocks) close() {
	for i := len(l.locks) - 1; i >= 0; i-- {
		_ = l.locks[i].Close()
	}
	l.locks = nil
}

func acquireNamespaceMigrationLocks(project string, profiles ...string) (*namespaceMigrationLocks, error) {
	paths := []string{namespaceMigrationGlobalLockPath(project)}
	seenProfiles := map[string]bool{}
	for _, profile := range profiles {
		profile = strings.TrimSpace(profile)
		if seenProfiles[profile] {
			continue
		}
		seenProfiles[profile] = true
		paths = append(paths, team.ProfileLockPath(project, profile))
	}
	sort.Strings(paths[1:])
	result := &namespaceMigrationLocks{}
	for _, path := range paths {
		f, err := openContainedNamespaceLock(project, path)
		if err != nil {
			result.close()
			return nil, err
		}
		lock, acquired, err := flock.TryExclusiveFile(f)
		if err != nil {
			result.close()
			return nil, err
		}
		if !acquired {
			result.close()
			return nil, fmt.Errorf("namespace migration refused: lock is held: %s", path)
		}
		result.locks = append(result.locks, lock)
	}
	return result, nil
}

func executeNamespaceMigration(initial namespaceMigrationPlan) (result namespaceMigrationResult, retErr error) {
	if err := namespaceMigrationBeforeAdmission(initial); err != nil {
		return result, err
	}
	admissions, err := acquireNamespaceMigrationAdmissions(initial.ProjectDir, initial.Source, initial.Target)
	if err != nil {
		return result, err
	}
	defer admissions.close()
	locks, err := acquireNamespaceMigrationLocks(initial.ProjectDir, initial.Source.Profile, initial.Target.Profile)
	if err != nil {
		return result, err
	}
	defer locks.close()
	if active, err := activeNamespaceMigration(initial.ProjectDir, initial.Source.Profile, initial.Source.Session); err != nil {
		return result, err
	} else if active != nil {
		return result, fmt.Errorf("namespace migration %s already names source endpoint; recover it first with %s", active.ID, active.Recovery)
	}
	if active, err := activeNamespaceMigration(initial.ProjectDir, initial.Target.Profile, initial.Target.Session); err != nil {
		return result, err
	} else if active != nil {
		return result, fmt.Errorf("namespace migration %s already names target endpoint; recover it first with %s", active.ID, active.Recovery)
	}
	if err := namespaceMigrationBeforeReservation(initial); err != nil {
		return result, err
	}
	plan := initial
	txRoot := filepath.Dir(namespaceMigrationJournalPath(plan.ProjectDir, plan.ID))
	if err := os.Mkdir(txRoot, 0o700); err != nil {
		if os.IsExist(err) {
			return result, fmt.Errorf("migration journal %s already exists; inspect or recover it", plan.ID)
		}
		return result, fmt.Errorf("create migration journal: %w", err)
	}
	journal := namespaceMigrationJournal{
		SchemaVersion: namespaceMigrationSchema,
		ID:            plan.ID,
		ProjectDir:    plan.ProjectDir,
		Source:        plan.Source,
		Target:        plan.Target,
		Phase:         migrationPhasePlanned,
		CreatedAt:     namespaceMigrationNow(),
		UpdatedAt:     namespaceMigrationNow(),
		Recovery:      plan.Recovery,
		Rollback:      "amq-squad namespace rollback --project " + shellQuote(plan.ProjectDir) + " --id " + shellQuote(plan.ID),
		Plan:          plan,
	}
	for _, artifact := range plan.Artifacts {
		if artifact.Policy == "shared_key_cas" {
			continue
		}
		journal.Entries = append(journal.Entries, namespaceMigrationJournalEntry{
			Name:         artifact.Name,
			Source:       artifact.Source,
			Target:       artifact.Target,
			Stage:        filepath.Join(txRoot, "stage", artifact.Name),
			Backup:       filepath.Join(txRoot, "backup", artifact.Name),
			SourceSHA256: artifact.SHA256,
			BackupOnly:   artifact.Policy == "backup_only",
		})
	}
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	result = namespaceMigrationResult{ID: plan.ID, Status: "recovery_required", Phase: journal.Phase, Source: plan.Source, Target: plan.Target, Manifest: namespaceMigrationJournalPath(plan.ProjectDir, plan.ID), Backup: filepath.Join(txRoot, "backup"), Recovery: journal.Recovery, Rollback: journal.Rollback}
	defer func() {
		if retErr == nil || journal.Phase.terminal() {
			return
		}
		journal.Phase = migrationPhaseRecoveryRequired
		journal.Error = retErr.Error()
		_ = writeNamespaceMigrationJournal(&journal)
		result.Phase = journal.Phase
	}()
	if err := namespaceMigrationFailpoint("reserved"); err != nil {
		return result, err
	}
	// The planned manifest is the writer barrier. Re-run the complete planner
	// only after that durable reservation exists so every endpoint writer sees
	// the migration while liveness, locks, profiles, and digests are rechecked.
	lockedPlan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: initial.ProjectDir, Source: initial.Source, Target: initial.Target, OwnsEndpointLocks: true})
	if err != nil {
		return abortNamespaceMigrationReservation(&journal, result, err)
	}
	if len(lockedPlan.Blockers) > 0 {
		return abortNamespaceMigrationReservation(&journal, result, fmt.Errorf("namespace migration refused after reservation: %s", strings.Join(lockedPlan.Blockers, "; ")))
	}
	if initial.Fingerprint != lockedPlan.Fingerprint {
		return abortNamespaceMigrationReservation(&journal, result, fmt.Errorf("namespace migration preflight changed after reservation: planned %s, now %s; rerun preview", initial.Fingerprint, lockedPlan.Fingerprint))
	}
	plan = lockedPlan
	journal.Plan = lockedPlan
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("planned"); err != nil {
		return result, err
	}

	if err := os.Mkdir(filepath.Join(txRoot, "stage"), 0o700); err != nil {
		return result, fmt.Errorf("create stage: %w", err)
	}
	if err := os.Mkdir(filepath.Join(txRoot, "backup"), 0o700); err != nil {
		return result, fmt.Errorf("create backup: %w", err)
	}
	for i := range journal.Entries {
		entry := &journal.Entries[i]
		if entry.BackupOnly {
			continue
		}
		artifact := migrationPlanArtifact(plan, entry.Name)
		if err := copyMigrationArtifactToStage(plan, artifact, entry.Stage); err != nil {
			return result, fmt.Errorf("stage %s: %w", entry.Name, err)
		}
		digest, _, _, _, err := migrationTreeDigest(entry.Stage)
		if err != nil {
			return result, fmt.Errorf("verify staged %s: %w", entry.Name, err)
		}
		entry.TargetSHA256 = digest
		entry.Staged = true
		if err := writeNamespaceMigrationJournal(&journal); err != nil {
			return result, err
		}
		if err := namespaceMigrationFailpoint("staged:" + entry.Name); err != nil {
			return result, err
		}
	}
	if err := prepareNamespaceSharedState(&journal); err != nil {
		return result, err
	}
	journal.Phase = migrationPhaseStaged
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("staged"); err != nil {
		return result, err
	}

	// The first source rename is the point of no longer being a preview. Repeat
	// the complete planner and compare all evidence immediately beforehand.
	recheck, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: plan.ProjectDir, Source: plan.Source, Target: plan.Target, OwnsEndpointLocks: true})
	if err != nil {
		return result, err
	}
	if len(recheck.Blockers) > 0 || recheck.Fingerprint != plan.Fingerprint {
		return result, fmt.Errorf("namespace migration evidence changed before first rename; recovery required: blockers=%v fingerprint=%s", recheck.Blockers, recheck.Fingerprint)
	}
	for _, entry := range journal.Entries {
		if entry.BackupOnly || entry.Target == "" {
			continue
		}
		created, err := ensureMigrationTargetParents(plan.ProjectDir, filepath.Dir(entry.Target))
		if err != nil {
			return result, err
		}
		journal.CreatedParents = append(journal.CreatedParents, created...)
	}
	journal.CreatedParents = dedupeMigrationStrings(journal.CreatedParents)
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	for i := range journal.Entries {
		entry := &journal.Entries[i]
		if err := ensureMigrationPathDigest(entry.Source, entry.SourceSHA256); err != nil {
			return result, err
		}
		if err := namespaceRenameNoReplace(entry.Source, entry.Backup); err != nil {
			return result, fmt.Errorf("backup %s without overwrite: %w", entry.Name, err)
		}
		entry.BackedUp = true
		if err := syncMigrationParents(entry.Source, entry.Backup); err != nil {
			return result, err
		}
		if err := writeNamespaceMigrationJournal(&journal); err != nil {
			return result, err
		}
		if err := ensureMigrationPathDigest(entry.Backup, entry.SourceSHA256); err != nil {
			return result, fmt.Errorf("verify retained backup %s after source rename: %w", entry.Name, err)
		}
		if err := namespaceMigrationFailpoint("backed_up:" + entry.Name); err != nil {
			return result, err
		}
	}
	if err := ensureNamespaceMigrationSourcesAbsent(journal); err != nil {
		return result, err
	}
	journal.Phase = migrationPhaseSourceBackedUp
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("source_backed_up"); err != nil {
		return result, err
	}

	for i := range journal.Entries {
		entry := &journal.Entries[i]
		if entry.BackupOnly {
			continue
		}
		if _, err := os.Lstat(entry.Target); err == nil || !os.IsNotExist(err) {
			return result, fmt.Errorf("target appeared before publication: %s", entry.Target)
		}
		if err := namespaceRenameNoReplace(entry.Stage, entry.Target); err != nil {
			return result, fmt.Errorf("publish %s without overwrite: %w", entry.Name, err)
		}
		entry.Published = true
		if err := syncMigrationParents(entry.Stage, entry.Target); err != nil {
			return result, err
		}
		if err := writeNamespaceMigrationJournal(&journal); err != nil {
			return result, err
		}
		if err := namespaceMigrationFailpoint("published:" + entry.Name); err != nil {
			return result, err
		}
	}
	journal.Phase = migrationPhaseTargetsPublished
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("targets_published"); err != nil {
		return result, err
	}
	if err := publishNamespaceSharedState(&journal); err != nil {
		return result, err
	}
	journal.Phase = migrationPhaseSharedStatePublished
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("shared_state_published"); err != nil {
		return result, err
	}
	if err := verifyPublishedNamespaceMigration(journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("verified"); err != nil {
		return result, err
	}
	journal.Phase = migrationPhaseCommitted
	journal.Error = ""
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	if err := namespaceMigrationFailpoint("committed"); err != nil {
		// The committed phase is durable. A caller error after this boundary must
		// not demote it to recovery_required.
		return namespaceMigrationResult{ID: plan.ID, Status: "committed", Phase: journal.Phase, Source: plan.Source, Target: plan.Target, Manifest: namespaceMigrationJournalPath(plan.ProjectDir, plan.ID), Backup: filepath.Join(txRoot, "backup"), Rollback: journal.Rollback, Detail: err.Error()}, nil
	}
	return namespaceMigrationResult{ID: plan.ID, Status: "committed", Phase: journal.Phase, Source: plan.Source, Target: plan.Target, Manifest: namespaceMigrationJournalPath(plan.ProjectDir, plan.ID), Backup: filepath.Join(txRoot, "backup"), Rollback: journal.Rollback, Detail: "all target artifacts and shared state verified; backup retained"}, nil
}

func migrationPlanArtifact(plan namespaceMigrationPlan, name string) namespaceMigrationArtifact {
	for _, artifact := range plan.Artifacts {
		if artifact.Name == name {
			return artifact
		}
	}
	return namespaceMigrationArtifact{Name: name}
}

func abortNamespaceMigrationReservation(journal *namespaceMigrationJournal, result namespaceMigrationResult, cause error) (namespaceMigrationResult, error) {
	journal.Phase = migrationPhaseAborted
	journal.Error = cause.Error()
	if err := writeNamespaceMigrationJournal(journal); err != nil {
		terminalizeErr := fmt.Errorf("terminalize harmless migration preflight refusal: %w", err)
		journal.Phase = migrationPhaseRecoveryRequired
		journal.Error = cause.Error() + "; " + terminalizeErr.Error()
		_ = writeNamespaceMigrationJournal(journal)
		result.Status = string(migrationPhaseRecoveryRequired)
		result.Phase = migrationPhaseRecoveryRequired
		result.Detail = journal.Error
		return result, fmt.Errorf("%v; %w", cause, terminalizeErr)
	}
	result.Status = string(migrationPhaseAborted)
	result.Phase = migrationPhaseAborted
	result.Detail = cause.Error()
	result.Recovery = ""
	return result, cause
}

func writeNamespaceMigrationJournal(journal *namespaceMigrationJournal) error {
	journal.UpdatedAt = namespaceMigrationNow()
	path := namespaceMigrationJournalPath(journal.ProjectDir, journal.ID)
	b, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal migration journal: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create migration journal temp: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("publish migration journal: %w", err)
	}
	return syncMigrationDir(filepath.Dir(path))
}

func readNamespaceMigrationJournal(project, id string) (namespaceMigrationJournal, error) {
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return namespaceMigrationJournal{}, usageErrorf("namespace migration requires a valid --id")
	}
	canonicalProject, err := canonicalMigrationProject(project)
	if err != nil {
		return namespaceMigrationJournal{}, err
	}
	project = canonicalProject
	path := namespaceMigrationJournalPath(project, id)
	b, err := os.ReadFile(path)
	if err != nil {
		return namespaceMigrationJournal{}, fmt.Errorf("read migration journal %s: %w", path, err)
	}
	var journal namespaceMigrationJournal
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&journal); err != nil {
		return namespaceMigrationJournal{}, fmt.Errorf("parse migration journal %s: %w", path, err)
	}
	if journal.SchemaVersion != namespaceMigrationSchema || journal.ID != id || filepath.Clean(journal.ProjectDir) != filepath.Clean(project) {
		return namespaceMigrationJournal{}, fmt.Errorf("migration journal identity mismatch")
	}
	return journal, nil
}

func copyMigrationArtifactToStage(plan namespaceMigrationPlan, artifact namespaceMigrationArtifact, target string) error {
	info, err := os.Lstat(artifact.Source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyMigrationDirectory(plan, artifact, artifact.Source, target, "")
	}
	return copyMigrationFile(plan, artifact, artifact.Source, target, "", info)
}

func copyMigrationDirectory(plan namespaceMigrationPlan, artifact namespaceMigrationArtifact, source, target, rel string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("directory changed or became symlink: %s", source)
	}
	if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
		return err
	}
	dir, openedInfo, err := openNamespaceNoFollow(source, true)
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) {
		_ = dir.Close()
		return fmt.Errorf("directory identity changed during staging: %s", source)
	}
	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		childRel := filepath.Join(rel, entry.Name())
		childInfo, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		publish, err := migrationEntryPublish(plan, artifact, childRel, childInfo)
		if err != nil {
			return err
		}
		if !publish {
			continue
		}
		if childInfo.IsDir() {
			if err := copyMigrationDirectory(plan, artifact, sourcePath, targetPath, childRel); err != nil {
				return err
			}
		} else if err := copyMigrationFile(plan, artifact, sourcePath, targetPath, childRel, childInfo); err != nil {
			return err
		}
	}
	if err := syncMigrationDir(target); err != nil {
		return err
	}
	return os.Chmod(target, info.Mode().Perm())
}

func copyMigrationFile(plan namespaceMigrationPlan, artifact namespaceMigrationArtifact, source, target, rel string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || migrationLinkCount(info) > 1 {
		return fmt.Errorf("unsafe source file changed during staging: %s", source)
	}
	fSource, openedInfo, err := openNamespaceNoFollow(source, false)
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) || migrationLinkCount(openedInfo) > 1 {
		_ = fSource.Close()
		return fmt.Errorf("file identity changed during staging: %s", source)
	}
	b, err := io.ReadAll(fSource)
	closeErr := fSource.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	b, publish, err := migrationTransformFile(plan, artifact, rel, b)
	if err != nil {
		return err
	}
	if !publish {
		return nil
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}

func migrationEntryPublish(plan namespaceMigrationPlan, artifact namespaceMigrationArtifact, rel string, info os.FileInfo) (bool, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("symlink refused during stage: %s", filepath.Join(artifact.Source, rel))
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return false, fmt.Errorf("special file refused during stage: %s", filepath.Join(artifact.Source, rel))
	}
	base := filepath.Base(rel)
	if strings.HasSuffix(base, ".lock") || strings.Contains(base, ".tmp") || strings.Contains(base, ".candidate-") {
		return false, nil
	}
	if artifact.Name == "amq_root" && base == "presence.json" {
		return false, nil
	}
	_ = plan
	return true, nil
}

func ensureMigrationPathDigest(path, want string) error {
	got, _, _, _, err := migrationTreeDigest(path)
	if err != nil {
		return fmt.Errorf("revalidate %s: %w", path, err)
	}
	if got != want {
		return fmt.Errorf("artifact changed before commit: %s digest %s, planned %s", path, got, want)
	}
	return nil
}

func ensureNamespaceMigrationSourcesAbsent(journal namespaceMigrationJournal) error {
	for _, entry := range journal.Entries {
		if !entry.BackedUp {
			continue
		}
		if _, err := os.Lstat(entry.Source); err == nil {
			return fmt.Errorf("source path reappeared after backup; recovery required: %s", entry.Source)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("verify moved source remains absent %s: %w", entry.Source, err)
		}
	}
	return nil
}

func ensureMigrationTargetParents(project, dir string) ([]string, error) {
	project, dir = filepath.Clean(project), filepath.Clean(dir)
	if !pathContains(project, dir) {
		return nil, fmt.Errorf("target parent escapes project: %s", dir)
	}
	var missing []string
	current := dir
	for current != project {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil, fmt.Errorf("unsafe target parent: %s", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, current)
		current = filepath.Dir(current)
	}
	var created []string
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o755); err != nil {
			return created, err
		}
		created = append(created, missing[i])
		if err := syncMigrationDir(filepath.Dir(missing[i])); err != nil {
			return created, err
		}
	}
	return created, nil
}

func rejectMigrationSymlinkComponents(project, target string) error {
	project, target = filepath.Clean(project), filepath.Clean(target)
	if !pathContains(project, target) {
		return fmt.Errorf("migration path escapes project: %s", target)
	}
	rel, _ := filepath.Rel(project, target)
	current := project
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink component refused: %s", current)
		}
	}
	return nil
}

func syncMigrationParents(paths ...string) error {
	seen := map[string]bool{}
	for _, path := range paths {
		dir := filepath.Dir(path)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if err := syncMigrationDir(dir); err != nil {
			return err
		}
	}
	return nil
}

func syncMigrationDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func verifyPublishedNamespaceMigration(journal namespaceMigrationJournal) error {
	if err := ensureNamespaceMigrationSourcesAbsent(journal); err != nil {
		return err
	}
	for _, entry := range journal.Entries {
		if entry.BackupOnly {
			if !entry.BackedUp {
				return fmt.Errorf("backup-only artifact %s is not backed up", entry.Name)
			}
			continue
		}
		if !entry.Published || !entry.BackedUp {
			return fmt.Errorf("artifact %s lacks complete journal evidence", entry.Name)
		}
		if err := ensureMigrationPathDigest(entry.Target, entry.TargetSHA256); err != nil {
			return err
		}
		if err := ensureMigrationPathDigest(entry.Backup, entry.SourceSHA256); err != nil {
			return err
		}
	}
	if journal.SharedState.Prepared && !journal.SharedState.Published {
		return fmt.Errorf("shared notify state is not published")
	}
	return nil
}

func prepareNamespaceSharedState(journal *namespaceMigrationJournal) error {
	artifact := migrationPlanArtifact(journal.Plan, "notify_state")
	if artifact.Name == "" || artifact.Source == "" || artifact.SHA256 == "" {
		return nil
	}
	txRoot := filepath.Dir(namespaceMigrationJournalPath(journal.ProjectDir, journal.ID))
	backup := filepath.Join(txRoot, "backup", "notify_state.original")
	stage := filepath.Join(txRoot, "stage", "notify_state.rewritten")
	if err := copyExactMigrationFile(artifact.Source, backup); err != nil {
		return fmt.Errorf("backup shared notify state: %w", err)
	}
	payload, changed, err := rewriteNamespaceNotifyState(artifact.Source, journal.Source, journal.Target)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	info, _ := os.Stat(artifact.Source)
	if err := writeExclusiveSyncedFile(stage, payload, info.Mode().Perm()); err != nil {
		return err
	}
	publishedHash, _, err := migrationRegularFileDigest(stage)
	if err != nil {
		return err
	}
	journal.SharedState = namespaceMigrationSharedState{Path: artifact.Source, Backup: backup, Stage: stage, OriginalSHA256: artifact.SHA256, PublishedSHA256: publishedHash, Prepared: true}
	return writeNamespaceMigrationJournal(journal)
}

func publishNamespaceSharedState(journal *namespaceMigrationJournal) error {
	state := &journal.SharedState
	if !state.Prepared {
		return nil
	}
	if state.Published {
		if err := ensureMigrationPathDigest(state.Path, state.PublishedSHA256); err != nil {
			return fmt.Errorf("verify already-published notify state: %w", err)
		}
		return nil
	}
	if err := ensureMigrationPathDigest(state.Path, state.OriginalSHA256); err != nil {
		return fmt.Errorf("notify state CAS refused: %w", err)
	}
	if err := os.Rename(state.Stage, state.Path); err != nil {
		return fmt.Errorf("publish shared notify state: %w", err)
	}
	if err := syncMigrationDir(filepath.Dir(state.Path)); err != nil {
		return err
	}
	state.Published = true
	return writeNamespaceMigrationJournal(journal)
}

func copyExactMigrationFile(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || migrationLinkCount(info) > 1 {
		return fmt.Errorf("unsafe regular file: %s", source)
	}
	f, openedInfo, err := openNamespaceNoFollow(source, false)
	if err != nil {
		return err
	}
	if !os.SameFile(info, openedInfo) || migrationLinkCount(openedInfo) > 1 {
		_ = f.Close()
		return fmt.Errorf("file identity changed during copy: %s", source)
	}
	b, err := io.ReadAll(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return writeExclusiveSyncedFile(target, b, info.Mode().Perm())
}

func writeExclusiveSyncedFile(path string, payload []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, bytes.NewReader(payload)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func recoverNamespaceMigration(project, id string, dryRun bool) (namespaceMigrationResult, error) {
	project, err := canonicalMigrationProject(project)
	if err != nil {
		return namespaceMigrationResult{}, err
	}
	journal, err := readNamespaceMigrationJournal(project, id)
	if err != nil {
		return namespaceMigrationResult{}, err
	}
	result := namespaceMigrationResult{ID: id, Status: string(journal.Phase), Phase: journal.Phase, Source: journal.Source, Target: journal.Target, Manifest: namespaceMigrationJournalPath(project, id), Backup: filepath.Join(filepath.Dir(namespaceMigrationJournalPath(project, id)), "backup"), Recovery: journal.Recovery, Rollback: journal.Rollback, DryRun: dryRun}
	if journal.Phase.terminal() {
		result.Detail = "journal is already terminal"
		return result, nil
	}
	if dryRun {
		forward, evidenceErr := migrationCanFinishForward(journal)
		if evidenceErr != nil {
			result.Status = string(migrationPhaseManualRecoveryRequired)
			result.Detail = evidenceErr.Error()
		} else if forward {
			result.Status = "would_finish_forward"
			result.Detail = "all target publications are journaled and verified"
		} else {
			result.Status = "would_restore_source"
			result.Detail = "incomplete publication will be quarantined and source backups restored"
		}
		return result, nil
	}
	admissions, err := acquireNamespaceMigrationAdmissions(project, journal.Source, journal.Target)
	if err != nil {
		return result, err
	}
	defer admissions.close()
	locks, err := acquireNamespaceMigrationLocks(project, journal.Source.Profile, journal.Target.Profile)
	if err != nil {
		return result, err
	}
	defer locks.close()
	// Re-read after admission and migration/profile locks. The first read was
	// only sufficient to discover the endpoints whose admissions must be held.
	journal, err = readNamespaceMigrationJournal(project, id)
	if err != nil {
		return result, err
	}
	if journal.Phase.terminal() {
		result.Status, result.Phase = string(journal.Phase), journal.Phase
		result.Detail = "journal became terminal while waiting for writer admission"
		return result, nil
	}
	forward, evidenceErr := migrationCanFinishForward(journal)
	if evidenceErr != nil {
		journal.Phase = migrationPhaseManualRecoveryRequired
		journal.Error = evidenceErr.Error()
		_ = writeNamespaceMigrationJournal(&journal)
		result.Status, result.Phase, result.Detail = string(journal.Phase), journal.Phase, evidenceErr.Error()
		return result, fmt.Errorf("manual recovery required: %w", evidenceErr)
	}
	if forward {
		if err := publishNamespaceSharedState(&journal); err != nil {
			return result, err
		}
		if err := verifyPublishedNamespaceMigration(journal); err != nil {
			return result, err
		}
		if err := namespaceMigrationFailpoint("recovery_verified"); err != nil {
			return result, err
		}
		journal.Phase = migrationPhaseCommitted
		journal.Error = ""
		if err := writeNamespaceMigrationJournal(&journal); err != nil {
			return result, err
		}
		result.Status, result.Phase, result.Detail = "committed", journal.Phase, "recovery verified all publications and completed forward"
		return result, nil
	}
	if err := restoreNamespaceMigrationSource(&journal); err != nil {
		journal.Phase = migrationPhaseManualRecoveryRequired
		journal.Error = err.Error()
		_ = writeNamespaceMigrationJournal(&journal)
		return result, fmt.Errorf("manual recovery required: %w", err)
	}
	journal.Phase = migrationPhaseRolledBack
	journal.Error = ""
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		return result, err
	}
	result.Status, result.Phase, result.Detail = "rolled_back", journal.Phase, "partial targets retained in quarantine; source restored without overwrite"
	return result, nil
}

func migrationCanFinishForward(journal namespaceMigrationJournal) (bool, error) {
	allPublished := true
	for _, entry := range journal.Entries {
		if entry.BackupOnly {
			if entry.BackedUp {
				if err := ensureMigrationPathDigest(entry.Backup, entry.SourceSHA256); err != nil {
					return false, err
				}
			} else {
				allPublished = false
			}
			continue
		}
		if entry.Published {
			if err := ensureMigrationPathDigest(entry.Target, entry.TargetSHA256); err != nil {
				return false, err
			}
		} else {
			allPublished = false
		}
		if entry.BackedUp {
			if err := ensureMigrationPathDigest(entry.Backup, entry.SourceSHA256); err != nil {
				return false, err
			}
		} else if entry.Published {
			return false, fmt.Errorf("artifact %s is published without recorded source backup", entry.Name)
		}
	}
	if journal.SharedState.Published {
		if err := ensureMigrationPathDigest(journal.SharedState.Path, journal.SharedState.PublishedSHA256); err != nil {
			return false, err
		}
	}
	if allPublished {
		if err := ensureNamespaceMigrationSourcesAbsent(journal); err != nil {
			return false, err
		}
	}
	return allPublished, nil
}

func restoreNamespaceMigrationSource(journal *namespaceMigrationJournal) error {
	txRoot := filepath.Dir(namespaceMigrationJournalPath(journal.ProjectDir, journal.ID))
	quarantine := filepath.Join(txRoot, "quarantine")
	if err := os.MkdirAll(quarantine, 0o700); err != nil {
		return err
	}
	for i := len(journal.Entries) - 1; i >= 0; i-- {
		entry := &journal.Entries[i]
		if entry.Published {
			if err := ensureMigrationPathDigest(entry.Target, entry.TargetSHA256); err != nil {
				return err
			}
			dest := filepath.Join(quarantine, "published-"+entry.Name)
			if err := namespaceRenameNoReplace(entry.Target, dest); err != nil {
				return fmt.Errorf("quarantine target %s: %w", entry.Name, err)
			}
			entry.Published = false
		}
		if entry.Staged {
			if _, err := os.Lstat(entry.Stage); err == nil {
				dest := filepath.Join(quarantine, "staged-"+entry.Name)
				if err := namespaceRenameNoReplace(entry.Stage, dest); err != nil {
					return err
				}
			}
		}
	}
	if journal.SharedState.Published {
		if err := ensureMigrationPathDigest(journal.SharedState.Path, journal.SharedState.PublishedSHA256); err != nil {
			return err
		}
		if err := namespaceRenameNoReplace(journal.SharedState.Path, filepath.Join(quarantine, "notify_state.published")); err != nil {
			return err
		}
		if err := copyExactMigrationFile(journal.SharedState.Backup, journal.SharedState.Path); err != nil {
			return err
		}
		journal.SharedState.Published = false
	}
	for i := len(journal.Entries) - 1; i >= 0; i-- {
		entry := &journal.Entries[i]
		if !entry.BackedUp {
			continue
		}
		if _, err := os.Lstat(entry.Source); err == nil || !os.IsNotExist(err) {
			return fmt.Errorf("source path was recreated; refusing overwrite: %s", entry.Source)
		}
		created, err := ensureMigrationTargetParents(journal.ProjectDir, filepath.Dir(entry.Source))
		if err != nil {
			return err
		}
		journal.CreatedParents = append(journal.CreatedParents, created...)
		if err := namespaceRenameNoReplace(entry.Backup, entry.Source); err != nil {
			return err
		}
		entry.BackedUp = false
	}
	return nil
}

func rollbackNamespaceMigration(project, id string, dryRun bool) (namespaceMigrationResult, error) {
	project, err := canonicalMigrationProject(project)
	if err != nil {
		return namespaceMigrationResult{}, err
	}
	original, err := readNamespaceMigrationJournal(project, id)
	if err != nil {
		return namespaceMigrationResult{}, err
	}
	if original.Phase != migrationPhaseCommitted {
		return namespaceMigrationResult{}, fmt.Errorf("rollback requires committed migration %s, found phase %s", id, original.Phase)
	}
	for _, entry := range original.Entries {
		if entry.BackupOnly {
			continue
		}
		if err := ensureMigrationPathDigest(entry.Target, entry.TargetSHA256); err != nil {
			return namespaceMigrationResult{}, fmt.Errorf("target diverged since commit: %w", err)
		}
	}
	plan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: project, Source: original.Target, Target: original.Source, DryRun: dryRun})
	if err != nil {
		return namespaceMigrationResult{}, err
	}
	if len(plan.Blockers) > 0 {
		return namespaceMigrationResult{ID: plan.ID, Status: "blocked", Source: plan.Source, Target: plan.Target, DryRun: dryRun, Detail: strings.Join(plan.Blockers, "; ")}, fmt.Errorf("rollback refused: %s", strings.Join(plan.Blockers, "; "))
	}
	if dryRun {
		return namespaceMigrationResult{ID: plan.ID, Status: "would_rollback", Phase: migrationPhasePlanned, Source: plan.Source, Target: plan.Target, DryRun: true, Detail: "reverse transaction preflight passed; committed target digests are unchanged"}, nil
	}
	result, err := executeNamespaceMigration(plan)
	if err != nil {
		return result, err
	}
	original.Phase = migrationPhaseRolledBack
	original.Error = "rolled back by reverse transaction " + result.ID
	if err := writeNamespaceMigrationJournal(&original); err != nil {
		return result, err
	}
	result.Status = "rolled_back"
	result.Detail = "committed migration reversed by stopped-only transaction; reverse backup retained"
	return result, nil
}
