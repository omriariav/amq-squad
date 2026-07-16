package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	namespaceMigrationSchema = 1
	namespaceMigrationDir    = "namespace-migrations"
	namespaceAdmissionDir    = "namespace-admission"
)

type namespaceAdmissionLocks struct {
	locks []*flock.Exclusive
}

// namespaceWriterBeforeAdmission is a deterministic concurrency seam for the
// interval after a command has resolved its namespace identity but before it
// attempts to acquire the shared writer admission. Production never pauses in
// that interval; tests use the seam to prove the mandatory re-resolution under
// the acquired admission rejects a namespace that migrated in the meantime.
var namespaceWriterBeforeAdmission = func(string, string, string) error { return nil }

func (l *namespaceAdmissionLocks) close() {
	for i := len(l.locks) - 1; i >= 0; i-- {
		_ = l.locks[i].Close()
	}
	l.locks = nil
}

func acquireRevalidatedContextWriter(initial contextResolution, profileWide bool, resolve func() (contextResolution, error)) (contextResolution, *namespaceAdmissionLocks, error) {
	var (
		admission *namespaceAdmissionLocks
		err       error
	)
	if profileWide {
		admission, err = acquireNamespaceProfileWriterAdmission(initial.ProjectDir, initial.Profile)
	} else {
		admission, err = acquireNamespaceWriterAdmission(initial.ProjectDir, initial.Profile, initial.Session)
	}
	if err != nil {
		return contextResolution{}, nil, err
	}
	current, err := resolve()
	if err != nil {
		admission.close()
		return contextResolution{}, nil, fmt.Errorf("namespace writer refused: context re-resolution under admission failed: %w", err)
	}
	if err := validateReResolvedContext(initial, current, profileWide); err != nil {
		admission.close()
		return contextResolution{}, nil, err
	}
	return current, admission, nil
}

func acquireRevalidatedAMQWriter(initial amqContext, resolve func() (amqContext, error)) (amqContext, *namespaceAdmissionLocks, error) {
	admission, err := acquireNamespaceWriterAdmission(initial.ProjectDir, initial.Profile, initial.Session)
	if err != nil {
		return amqContext{}, nil, err
	}
	current, err := resolve()
	if err != nil {
		admission.close()
		return amqContext{}, nil, fmt.Errorf("namespace writer refused: AMQ context re-resolution under admission failed: %w", err)
	}
	if err := validateReResolvedAMQContext(initial, current); err != nil {
		admission.close()
		return amqContext{}, nil, err
	}
	return current, admission, nil
}

func validateReResolvedContext(initial, current contextResolution, profileWide bool) error {
	changes := compareNamespaceIdentity(initial.ProjectDir, initial.Profile, "", initial.BaseRoot, initial.Handle, current.ProjectDir, current.Profile, "", current.BaseRoot, current.Handle)
	if !profileWide {
		changes = compareNamespaceIdentity(initial.ProjectDir, initial.Profile, initial.Session, initial.Root, initial.Handle, current.ProjectDir, current.Profile, current.Session, current.Root, current.Handle)
		if filepath.Clean(initial.BaseRoot) != filepath.Clean(current.BaseRoot) {
			changes = append(changes, fmt.Sprintf("base_root %q -> %q", initial.BaseRoot, current.BaseRoot))
		}
		if initial.NamespaceGeneration != current.NamespaceGeneration {
			changes = append(changes, fmt.Sprintf("namespace_generation %q -> %q", initial.NamespaceGeneration, current.NamespaceGeneration))
		}
	}
	if len(changes) > 0 {
		return fmt.Errorf("namespace writer refused: context changed before admission (%s); retry against the current namespace", strings.Join(dedupeMigrationStrings(changes), ", "))
	}
	return nil
}

func validateReResolvedAMQContext(initial, current amqContext) error {
	changes := compareNamespaceIdentity(initial.ProjectDir, initial.Profile, initial.Session, initial.Root, initial.Me, current.ProjectDir, current.Profile, current.Session, current.Root, current.Me)
	initialBase := absoluteAMQRoot(initial.ProjectDir, initial.Env.BaseRoot)
	currentBase := absoluteAMQRoot(current.ProjectDir, current.Env.BaseRoot)
	if filepath.Clean(initialBase) != filepath.Clean(currentBase) {
		changes = append(changes, fmt.Sprintf("base_root %q -> %q", initialBase, currentBase))
	}
	if initial.PinMode != current.PinMode {
		changes = append(changes, fmt.Sprintf("pin_mode %d -> %d", initial.PinMode, current.PinMode))
	}
	if initial.NamespaceGeneration != current.NamespaceGeneration {
		changes = append(changes, fmt.Sprintf("namespace_generation %q -> %q", initial.NamespaceGeneration, current.NamespaceGeneration))
	}
	if len(changes) > 0 {
		return fmt.Errorf("namespace writer refused: AMQ context changed before admission (%s); retry against the current namespace", strings.Join(dedupeMigrationStrings(changes), ", "))
	}
	return nil
}

func compareNamespaceIdentity(initialProject, initialProfile, initialSession, initialRoot, initialHandle, currentProject, currentProfile, currentSession, currentRoot, currentHandle string) []string {
	var changes []string
	if filepath.Clean(initialProject) != filepath.Clean(currentProject) {
		changes = append(changes, fmt.Sprintf("project %q -> %q", initialProject, currentProject))
	}
	if !squadnamespace.ProfilesEqual(initialProfile, currentProfile) {
		changes = append(changes, fmt.Sprintf("profile %q -> %q", initialProfile, currentProfile))
	}
	if strings.TrimSpace(initialSession) != strings.TrimSpace(currentSession) {
		changes = append(changes, fmt.Sprintf("session %q -> %q", initialSession, currentSession))
	}
	if filepath.Clean(initialRoot) != filepath.Clean(currentRoot) {
		changes = append(changes, fmt.Sprintf("root %q -> %q", initialRoot, currentRoot))
	}
	if strings.TrimSpace(initialHandle) != strings.TrimSpace(currentHandle) {
		changes = append(changes, fmt.Sprintf("handle %q -> %q", initialHandle, currentHandle))
	}
	return changes
}

func validateReResolvedEndpoint(operation string, initial, current squadnamespace.Ref, initialHandle, currentHandle string) error {
	changes := compareNamespaceIdentity(initial.TeamHome, initial.Profile, initial.Session, initial.AMQRoot, initialHandle, current.TeamHome, current.Profile, current.Session, current.AMQRoot, currentHandle)
	if len(changes) > 0 {
		return fmt.Errorf("%s refused: namespace target changed before admission (%s); retry against the current namespace", operation, strings.Join(dedupeMigrationStrings(changes), ", "))
	}
	return nil
}

type namespaceEndpointIdentity struct {
	Ref        squadnamespace.Ref
	Handle     string
	Generation string
}

func captureNamespaceEndpointIdentity(ref squadnamespace.Ref, handle string) (namespaceEndpointIdentity, error) {
	generation, err := namespaceEndpointGeneration(ref.TeamHome, ref.Profile, ref.Session)
	if err != nil {
		return namespaceEndpointIdentity{}, err
	}
	return namespaceEndpointIdentity{Ref: ref, Handle: strings.TrimSpace(handle), Generation: generation}, nil
}

func validateReResolvedEndpointIdentity(operation string, initial, current namespaceEndpointIdentity) error {
	if err := validateReResolvedEndpoint(operation, initial.Ref, current.Ref, initial.Handle, current.Handle); err != nil {
		return err
	}
	if initial.Generation != current.Generation {
		return fmt.Errorf("%s refused: namespace generation changed before admission (%q -> %q); retry against the current namespace", operation, initial.Generation, current.Generation)
	}
	return nil
}

func namespaceEndpointGeneration(projectDir, profile, session string) (string, error) {
	root := namespaceMigrationRoot(projectDir)
	if err := rejectMigrationSymlinkComponents(projectDir, root); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return "none", nil
	}
	if err != nil {
		return "", err
	}
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	var generations []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := namespaceMigrationJournalPath(projectDir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read namespace generation manifest %s: %w", path, err)
		}
		var journal namespaceMigrationJournal
		if err := json.Unmarshal(b, &journal); err != nil {
			return "", fmt.Errorf("parse namespace generation manifest %s: %w", path, err)
		}
		if journal.SchemaVersion != namespaceMigrationSchema || journal.ID != entry.Name() {
			return "", fmt.Errorf("namespace generation manifest %s has unsupported or mismatched identity", path)
		}
		if !namespaceRefMatches(journal.Source, profile, session) && !namespaceRefMatches(journal.Target, profile, session) {
			continue
		}
		digest := sha256.Sum256(b)
		generations = append(generations, fmt.Sprintf("%s:%x", journal.ID, digest))
	}
	if len(generations) == 0 {
		return "none", nil
	}
	sort.Strings(generations)
	return strings.Join(generations, ";"), nil
}

func namespaceAdmissionProfileLockPath(projectDir, profile string) string {
	return filepath.Join(projectDir, team.DirName, namespaceAdmissionDir, squadnamespace.NormalizeProfile(profile), ".profile.lock")
}

func namespaceAdmissionEndpointLockPath(projectDir, profile, session string) string {
	return filepath.Join(projectDir, team.DirName, namespaceAdmissionDir, squadnamespace.NormalizeProfile(profile), strings.TrimSpace(session)+".lock")
}

func preparedManifestAdmissionLockPath(projectDir, profile, session string) string {
	return filepath.Join(projectDir, team.DirName, namespaceAdmissionDir, squadnamespace.NormalizeProfile(profile), strings.TrimSpace(session)+".prepared.lock")
}

func acquireNamespaceAdmissionPaths(projectDir string, shared bool, paths ...string) (*namespaceAdmissionLocks, error) {
	paths = dedupeMigrationStrings(paths)
	sort.Strings(paths)
	result := &namespaceAdmissionLocks{}
	for _, path := range paths {
		f, err := openContainedNamespaceLock(projectDir, path)
		if err != nil {
			result.close()
			return nil, fmt.Errorf("namespace writer admission refused: %w", err)
		}
		var lock *flock.Exclusive
		if shared {
			var acquired bool
			lock, acquired, err = flock.TrySharedFile(f)
			if err == nil && !acquired {
				err = fmt.Errorf("namespace writer admission refused: migration owns %s", path)
			}
		} else {
			lock, err = flock.AcquireExclusiveFile(f)
		}
		if err != nil {
			result.close()
			return nil, err
		}
		result.locks = append(result.locks, lock)
	}
	return result, nil
}

func acquireNamespaceWriterAdmission(projectDir, profile, session string) (*namespaceAdmissionLocks, error) {
	if err := team.ValidateSessionName(strings.TrimSpace(session)); err != nil {
		return nil, fmt.Errorf("namespace writer admission requires valid session: %w", err)
	}
	if err := namespaceWriterBeforeAdmission(projectDir, squadnamespace.NormalizeProfile(profile), strings.TrimSpace(session)); err != nil {
		return nil, err
	}
	return acquireNamespaceAdmissionPaths(projectDir, true, namespaceAdmissionEndpointLockPath(projectDir, profile, session))
}

// acquirePreparedManifestReaderAdmission serializes a live prepared launch
// against every sanctioned preparation writer. The shared lock is held from
// accepted-state read through launch-record write and exec.
func acquirePreparedManifestReaderAdmission(projectDir, profile, session string) (*namespaceAdmissionLocks, error) {
	if err := team.ValidateSessionName(strings.TrimSpace(session)); err != nil {
		return nil, fmt.Errorf("prepared manifest reader admission requires valid session: %w", err)
	}
	return acquireNamespaceAdmissionPaths(projectDir, true, preparedManifestAdmissionLockPath(projectDir, profile, session))
}

// acquirePreparedManifestWriterAdmission is the matching exclusive authority
// for artifact preparation. It composes with the namespace shared admission:
// migration excludes both paths, while a live launch excludes preparation.
func acquirePreparedManifestWriterAdmission(projectDir, profile, session string) (*namespaceAdmissionLocks, error) {
	if err := team.ValidateSessionName(strings.TrimSpace(session)); err != nil {
		return nil, fmt.Errorf("prepared manifest writer admission requires valid session: %w", err)
	}
	return acquireNamespaceAdmissionPaths(projectDir, false, preparedManifestAdmissionLockPath(projectDir, profile, session))
}

func acquireNamespaceProfileWriterAdmission(projectDir, profile string) (*namespaceAdmissionLocks, error) {
	if err := namespaceWriterBeforeAdmission(projectDir, squadnamespace.NormalizeProfile(profile), ""); err != nil {
		return nil, err
	}
	return acquireNamespaceAdmissionPaths(projectDir, true, namespaceAdmissionProfileLockPath(projectDir, profile))
}

func acquireNamespaceWatcherAdmission(projectDir, profile, session string) (*namespaceAdmissionLocks, error) {
	if err := team.ValidateSessionName(strings.TrimSpace(session)); err != nil {
		return nil, fmt.Errorf("namespace watcher admission requires valid session: %w", err)
	}
	if err := namespaceWriterBeforeAdmission(projectDir, squadnamespace.NormalizeProfile(profile), strings.TrimSpace(session)); err != nil {
		return nil, err
	}
	return acquireNamespaceAdmissionPaths(projectDir, true,
		namespaceAdmissionProfileLockPath(projectDir, profile),
		namespaceAdmissionEndpointLockPath(projectDir, profile, session),
	)
}

func acquireNamespaceMigrationAdmissions(projectDir string, refs ...squadnamespace.Ref) (*namespaceAdmissionLocks, error) {
	var paths []string
	profiles := map[string]bool{}
	for _, ref := range refs {
		profile := squadnamespace.NormalizeProfile(ref.Profile)
		if !profiles[profile] {
			profiles[profile] = true
			paths = append(paths, namespaceAdmissionProfileLockPath(projectDir, profile))
		}
		paths = append(paths, namespaceAdmissionEndpointLockPath(projectDir, profile, ref.Session))
	}
	return acquireNamespaceAdmissionPaths(projectDir, false, paths...)
}

type namespaceMigrationPhase string

const (
	migrationPhasePlanned                namespaceMigrationPhase = "planned"
	migrationPhaseStaged                 namespaceMigrationPhase = "staged"
	migrationPhaseSourceBackedUp         namespaceMigrationPhase = "source_backed_up"
	migrationPhaseTargetsPublished       namespaceMigrationPhase = "targets_published"
	migrationPhaseSharedStatePublished   namespaceMigrationPhase = "shared_state_published"
	migrationPhaseCommitted              namespaceMigrationPhase = "committed"
	migrationPhaseAborted                namespaceMigrationPhase = "aborted"
	migrationPhaseRecoveryRequired       namespaceMigrationPhase = "recovery_required"
	migrationPhaseManualRecoveryRequired namespaceMigrationPhase = "manual_recovery_required"
	migrationPhaseRolledBack             namespaceMigrationPhase = "rolled_back"
)

func (p namespaceMigrationPhase) terminal() bool {
	switch p {
	case migrationPhaseCommitted, migrationPhaseAborted, migrationPhaseRolledBack:
		return true
	default:
		return false
	}
}

type namespaceMigrationJournal struct {
	SchemaVersion  int                              `json:"schema_version"`
	ID             string                           `json:"id"`
	ProjectDir     string                           `json:"project_dir"`
	Source         squadnamespace.Ref               `json:"source"`
	Target         squadnamespace.Ref               `json:"target"`
	Phase          namespaceMigrationPhase          `json:"phase"`
	CreatedAt      time.Time                        `json:"created_at"`
	UpdatedAt      time.Time                        `json:"updated_at"`
	Recovery       string                           `json:"recovery_command"`
	Rollback       string                           `json:"rollback_command,omitempty"`
	Plan           namespaceMigrationPlan           `json:"plan"`
	Entries        []namespaceMigrationJournalEntry `json:"entries"`
	SharedState    namespaceMigrationSharedState    `json:"shared_state"`
	CreatedParents []string                         `json:"created_parents,omitempty"`
	Error          string                           `json:"error,omitempty"`
}

type namespaceMigrationJournalEntry struct {
	Name         string `json:"name"`
	Source       string `json:"source"`
	Target       string `json:"target,omitempty"`
	Stage        string `json:"stage,omitempty"`
	Backup       string `json:"backup"`
	SourceSHA256 string `json:"source_sha256"`
	TargetSHA256 string `json:"target_sha256,omitempty"`
	Staged       bool   `json:"staged"`
	BackedUp     bool   `json:"backed_up"`
	Published    bool   `json:"published"`
	BackupOnly   bool   `json:"backup_only,omitempty"`
}

type namespaceMigrationSharedState struct {
	Path            string `json:"path,omitempty"`
	Backup          string `json:"backup,omitempty"`
	Stage           string `json:"stage,omitempty"`
	OriginalSHA256  string `json:"original_sha256,omitempty"`
	PublishedSHA256 string `json:"published_sha256,omitempty"`
	Prepared        bool   `json:"prepared"`
	Published       bool   `json:"published"`
}

func namespaceMigrationRoot(projectDir string) string {
	return filepath.Join(projectDir, team.DirName, namespaceMigrationDir)
}

func namespaceMigrationJournalPath(projectDir, id string) string {
	return filepath.Join(namespaceMigrationRoot(projectDir), id, "manifest.json")
}

func namespaceMigrationGlobalLockPath(projectDir string) string {
	return filepath.Join(namespaceMigrationRoot(projectDir), ".migration.lock")
}

func activeNamespaceMigration(projectDir, profile, session string) (*namespaceMigrationJournal, error) {
	root := namespaceMigrationRoot(projectDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan namespace migration journals: %w", err)
	}
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	var matches []namespaceMigrationJournal
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := namespaceMigrationJournalPath(projectDir, entry.Name())
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			// A transaction directory without a readable manifest is ambiguous.
			return nil, fmt.Errorf("read namespace migration journal %s: %w", path, readErr)
		}
		var journal namespaceMigrationJournal
		if err := json.Unmarshal(b, &journal); err != nil {
			return nil, fmt.Errorf("parse namespace migration journal %s: %w", path, err)
		}
		if journal.SchemaVersion != namespaceMigrationSchema || strings.TrimSpace(journal.ID) != entry.Name() {
			return nil, fmt.Errorf("namespace migration journal %s has unsupported or mismatched identity", path)
		}
		if journal.Phase.terminal() {
			continue
		}
		if namespaceRefMatches(journal.Source, profile, session) || namespaceRefMatches(journal.Target, profile, session) {
			matches = append(matches, journal)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.ID)
		}
		return nil, fmt.Errorf("multiple active namespace migrations name %s/%s: %s; manual recovery required", profile, session, strings.Join(ids, ", "))
	}
	return &matches[0], nil
}

func namespaceRefMatches(ref squadnamespace.Ref, profile, session string) bool {
	return squadnamespace.ProfilesEqual(ref.Profile, profile) && strings.TrimSpace(ref.Session) == strings.TrimSpace(session)
}

func namespaceMigrationConflictForProfileSession(projectDir, profile, session string) *namespaceConflictData {
	journal, err := activeNamespaceMigration(projectDir, profile, session)
	if err != nil {
		return &namespaceConflictData{
			Kind:        "namespace_migration_ambiguous",
			Profile:     squadnamespace.NormalizeProfile(profile),
			Session:     strings.TrimSpace(session),
			NamespaceID: squadnamespace.ID(profile, session),
			Detail:      "namespace migration state is unreadable or ambiguous: " + err.Error(),
		}
	}
	if journal == nil {
		retired, err := retiredNamespaceMigrationEndpoint(projectDir, profile, session)
		if err != nil {
			return &namespaceConflictData{
				Kind:        "namespace_migration_ambiguous",
				Profile:     squadnamespace.NormalizeProfile(profile),
				Session:     strings.TrimSpace(session),
				NamespaceID: squadnamespace.ID(profile, session),
				Detail:      "namespace migration history is unreadable or ambiguous: " + err.Error(),
			}
		}
		if retired == nil {
			return nil
		}
		requested := squadnamespace.Resolve(projectDir, profile, session)
		valid := retired.Target
		kind := "namespace_migration_retired_source"
		if retired.Phase == migrationPhaseRolledBack {
			valid = retired.Source
			kind = "namespace_migration_retired_target"
		}
		return &namespaceConflictData{
			Kind:               kind,
			Profile:            requested.Profile,
			Session:            requested.Session,
			NamespaceID:        requested.ID,
			RequestedAMQRoot:   requested.AMQRoot,
			ConflictingAMQRoot: valid.AMQRoot,
			Detail:             fmt.Sprintf("namespace migration %s completed in phase %s and retired endpoint %s; retry against current endpoint %s", retired.ID, retired.Phase, requested.ID, valid.ID),
			MigrationID:        retired.ID,
			MigrationPhase:     string(retired.Phase),
		}
	}
	requested := squadnamespace.Resolve(projectDir, profile, session)
	recovery := journal.Recovery
	if strings.TrimSpace(recovery) == "" {
		recovery = "amq-squad namespace recover --project " + shellQuote(projectDir) + " --id " + shellQuote(journal.ID)
	}
	return &namespaceConflictData{
		Kind:               "namespace_migration_in_progress",
		Profile:            requested.Profile,
		Session:            requested.Session,
		NamespaceID:        requested.ID,
		RequestedAMQRoot:   requested.AMQRoot,
		ConflictingAMQRoot: journal.Source.AMQRoot,
		Detail:             fmt.Sprintf("namespace migration %s is in phase %s and names endpoint %s; mutations are frozen until recovery completes", journal.ID, journal.Phase, requested.ID),
		RecoveryCommands:   []string{recovery},
		MigrationID:        journal.ID,
		MigrationPhase:     string(journal.Phase),
	}
}

func retiredNamespaceMigrationEndpoint(projectDir, profile, session string) (*namespaceMigrationJournal, error) {
	root := namespaceMigrationRoot(projectDir)
	if err := rejectMigrationSymlinkComponents(projectDir, root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var matches []namespaceMigrationJournal
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		journal, err := readNamespaceMigrationJournal(projectDir, entry.Name())
		if err != nil {
			return nil, err
		}
		retired := journal.Phase == migrationPhaseCommitted && namespaceRefMatches(journal.Source, profile, session)
		retired = retired || journal.Phase == migrationPhaseRolledBack && namespaceRefMatches(journal.Target, profile, session)
		if retired {
			matches = append(matches, journal)
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].UpdatedAt.After(matches[j].UpdatedAt) })
	return &matches[0], nil
}

func ensureNoNamespaceMigration(operation, projectDir, profile, session string) error {
	return namespaceConflictError(operation, namespaceMigrationConflictForProfileSession(projectDir, profile, session))
}

func ensureNoNamespaceMigrationForProfile(operation, projectDir, profile string) error {
	root := namespaceMigrationRoot(projectDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s refused: scan namespace migrations: %w", operation, err)
	}
	profile = squadnamespace.NormalizeProfile(profile)
	var matches []namespaceMigrationJournal
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		journal, readErr := readNamespaceMigrationJournal(projectDir, entry.Name())
		if readErr != nil {
			return fmt.Errorf("%s refused: namespace migration state is unreadable: %w", operation, readErr)
		}
		if journal.Phase.terminal() {
			continue
		}
		if squadnamespace.ProfilesEqual(journal.Source.Profile, profile) || squadnamespace.ProfilesEqual(journal.Target.Profile, profile) {
			matches = append(matches, journal)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, journal := range matches {
			ids = append(ids, journal.ID)
		}
		return fmt.Errorf("%s refused: multiple active namespace migrations name profile %s: %s; manual recovery required", operation, profile, strings.Join(ids, ", "))
	}
	journal := matches[0]
	endpoint := journal.Source
	if !squadnamespace.ProfilesEqual(endpoint.Profile, profile) {
		endpoint = journal.Target
	}
	return namespaceConflictError(operation, namespaceMigrationConflictForProfileSession(projectDir, endpoint.Profile, endpoint.Session))
}
