package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v2/internal/autonomy"
	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type namespaceMigrationArtifact struct {
	Name       string      `json:"name"`
	Source     string      `json:"source"`
	Target     string      `json:"target,omitempty"`
	Policy     string      `json:"policy"`
	Kind       string      `json:"kind"`
	SHA256     string      `json:"sha256"`
	Bytes      int64       `json:"bytes"`
	Mode       os.FileMode `json:"mode"`
	FileCount  int         `json:"file_count"`
	DirCount   int         `json:"dir_count"`
	Historical bool        `json:"historical,omitempty"`
}

type namespaceMigrationInputEvidence struct {
	SourceProfilePath   string `json:"source_profile_path"`
	SourceProfileSHA256 string `json:"source_profile_sha256"`
	TargetProfilePath   string `json:"target_profile_path"`
	TargetProfileSHA256 string `json:"target_profile_sha256"`
	AMQRCPath           string `json:"amqrc_path,omitempty"`
	AMQRCSHA256         string `json:"amqrc_sha256,omitempty"`
	Device              uint64 `json:"device"`
	AvailableBytes      uint64 `json:"available_bytes"`
	RequiredBytes       uint64 `json:"required_bytes"`
}

type namespaceMigrationLiveness struct {
	Endpoint string `json:"endpoint"`
	Handle   string `json:"handle,omitempty"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
}

type namespaceMigrationPlan struct {
	SchemaVersion int                             `json:"schema_version"`
	ID            string                          `json:"id"`
	ProjectDir    string                          `json:"project_dir"`
	Source        squadnamespace.Ref              `json:"source"`
	Target        squadnamespace.Ref              `json:"target"`
	Artifacts     []namespaceMigrationArtifact    `json:"artifacts"`
	History       []namespaceMigrationArtifact    `json:"history_references,omitempty"`
	Liveness      []namespaceMigrationLiveness    `json:"liveness"`
	Inputs        namespaceMigrationInputEvidence `json:"inputs"`
	Blockers      []string                        `json:"blockers,omitempty"`
	Warnings      []string                        `json:"warnings,omitempty"`
	Fingerprint   string                          `json:"fingerprint"`
	Recovery      string                          `json:"recovery_command"`
	DryRun        bool                            `json:"dry_run"`
}

type namespaceMigrationPlannerOptions struct {
	ProjectDir        string
	Source            squadnamespace.Ref
	Target            squadnamespace.Ref
	DryRun            bool
	Now               time.Time
	Probe             duplicateLaunchProbe
	OwnsEndpointLocks bool
}

var namespaceMigrationNow = func() time.Time { return time.Now().UTC() }

func planNamespaceMigration(opts namespaceMigrationPlannerOptions) (namespaceMigrationPlan, error) {
	project, err := canonicalMigrationProject(opts.ProjectDir)
	if err != nil {
		return namespaceMigrationPlan{}, err
	}
	opts.ProjectDir = project
	opts.Source = squadnamespace.Resolve(project, opts.Source.Profile, opts.Source.Session)
	opts.Target = squadnamespace.Resolve(project, opts.Target.Profile, opts.Target.Session)
	if opts.Now.IsZero() {
		opts.Now = namespaceMigrationNow()
	}
	if opts.Probe.PIDAlive == nil {
		opts.Probe = defaultDuplicateLaunchProbe
	}
	plan := namespaceMigrationPlan{
		SchemaVersion: namespaceMigrationSchema,
		ProjectDir:    project,
		Source:        opts.Source,
		Target:        opts.Target,
		DryRun:        opts.DryRun,
	}
	if opts.Source.ID == opts.Target.ID {
		plan.Blockers = append(plan.Blockers, "source and target namespace are identical")
	}
	if migrationPathsOverlap(opts.Source.AMQRoot, opts.Target.AMQRoot) {
		plan.Blockers = append(plan.Blockers, "source and target AMQ roots overlap or alias")
	}

	sourceTeam, sourceHash, err := migrationProfileInput(project, opts.Source.Profile)
	if err != nil {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("source profile: %v", err))
	}
	targetTeam, targetHash, err := migrationProfileInput(project, opts.Target.Profile)
	if err != nil {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("target profile: %v", err))
	}
	plan.Inputs.SourceProfilePath = team.ProfilePath(project, opts.Source.Profile)
	plan.Inputs.SourceProfileSHA256 = sourceHash
	plan.Inputs.TargetProfilePath = team.ProfilePath(project, opts.Target.Profile)
	plan.Inputs.TargetProfileSHA256 = targetHash
	if sourceHash != "" && targetHash != "" {
		if err := compatibleMigrationProfiles(sourceTeam, targetTeam, opts.Source.Session, opts.Target.Session); err != nil {
			plan.Blockers = append(plan.Blockers, err.Error())
		}
	}
	amqrcPath := filepath.Join(project, ".amqrc")
	if hash, _, err := migrationRegularFileDigest(amqrcPath); err == nil {
		plan.Inputs.AMQRCPath = amqrcPath
		plan.Inputs.AMQRCSHA256 = hash
	} else if !os.IsNotExist(err) {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf(".amqrc: %v", err))
	}

	artifacts := migrationArtifactSpecs(project, opts.Source, opts.Target)
	for _, spec := range artifacts {
		if err := rejectMigrationSymlinkComponents(project, spec.Source); err != nil {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s source path: %v", spec.Name, err))
			continue
		}
		if spec.Target != "" {
			if err := rejectMigrationSymlinkComponents(project, spec.Target); err != nil {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s target path: %v", spec.Name, err))
				continue
			}
		}
		artifact, present, artifactErr := inspectMigrationArtifact(spec)
		if artifactErr != nil {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s: %v", spec.Name, artifactErr))
			continue
		}
		if !present {
			continue
		}
		if spec.Historical {
			plan.History = append(plan.History, artifact)
		} else {
			plan.Artifacts = append(plan.Artifacts, artifact)
		}
	}
	if len(plan.Artifacts) == 0 {
		plan.Blockers = append(plan.Blockers, "source namespace has no migratable durable artifacts")
	}
	for _, artifact := range plan.Artifacts {
		if artifact.Target == "" || artifact.Policy == "backup_only" || artifact.Policy == "shared_key_cas" {
			continue
		}
		if info, statErr := os.Lstat(artifact.Target); statErr == nil {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("target artifact already exists: %s (%s)", artifact.Name, info.Mode()))
		} else if !os.IsNotExist(statErr) {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("inspect target artifact %s: %v", artifact.Name, statErr))
		}
	}

	plan.Liveness = append(plan.Liveness, inspectNamespaceEndpointLiveness("source", sourceTeam, opts.Source, opts.Probe, opts.Now, &plan.Blockers)...)
	plan.Liveness = append(plan.Liveness, inspectNamespaceEndpointLiveness("target", targetTeam, opts.Target, opts.Probe, opts.Now, &plan.Blockers)...)
	inspectNamespaceOperationalOwners("source", project, sourceTeam, opts.Source, opts.Now, &plan)
	inspectNamespaceOperationalOwners("target", project, targetTeam, opts.Target, opts.Now, &plan)
	inspectNamespaceLocks(project, opts.Source, opts.Target, plan.Artifacts, opts.OwnsEndpointLocks, &plan)

	device, free, spaceErr := migrationSpaceEvidence(project, plan.Artifacts)
	if spaceErr != nil {
		plan.Blockers = append(plan.Blockers, spaceErr.Error())
	}
	plan.Inputs.Device = device
	plan.Inputs.AvailableBytes = free
	var sourceBytes uint64
	for _, artifact := range plan.Artifacts {
		if artifact.Bytes > 0 && artifact.Policy != "history_reference" {
			sourceBytes += uint64(artifact.Bytes)
		}
	}
	plan.Inputs.RequiredBytes = sourceBytes + sourceBytes/10 + 1<<20
	if free > 0 && plan.Inputs.RequiredBytes > free {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("insufficient free space: need %d bytes, have %d", plan.Inputs.RequiredBytes, free))
	}

	sort.Strings(plan.Blockers)
	plan.Blockers = dedupeMigrationStrings(plan.Blockers)
	sort.Strings(plan.Warnings)
	plan.Warnings = dedupeMigrationStrings(plan.Warnings)
	plan.Fingerprint = namespaceMigrationPlanFingerprint(plan)
	plan.ID = "migration-" + strings.TrimPrefix(plan.Fingerprint, "sha256:")[:16]
	plan.Recovery = "amq-squad namespace recover --project " + shellQuote(project) + " --id " + shellQuote(plan.ID)
	return plan, nil
}

func canonicalMigrationProject(project string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(project))
	if err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project symlinks: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("project must resolve to a real directory")
	}
	return filepath.Clean(resolved), nil
}

func migrationPathsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if sameResolvedDir(a, b) {
		return true
	}
	return pathContains(a, b) || pathContains(b, a)
}

func migrationProfileInput(project, profile string) (team.Team, string, error) {
	path := team.ProfilePath(project, profile)
	hash, _, err := migrationRegularFileDigest(path)
	if err != nil {
		return team.Team{}, "", err
	}
	t, err := team.ReadProfile(project, profile)
	if err != nil {
		return team.Team{}, "", err
	}
	return t, hash, nil
}

func compatibleMigrationProfiles(source, target team.Team, sourceSession, targetSession string) error {
	if err := profilePinsOnlySession(source, sourceSession, "source"); err != nil {
		return err
	}
	if err := profilePinsOnlySession(target, targetSession, "target"); err != nil {
		return err
	}
	type memberIdentity struct {
		Role, Binary, Handle, Model, CWD, Launcher               string
		LauncherArgs, ClaudeArgs, CodexArgs, PermissionAllowlist []string
	}
	project := func(t team.Team) any {
		members := make([]memberIdentity, 0, len(t.Members))
		for _, m := range t.Members {
			members = append(members, memberIdentity{m.Role, m.Binary, m.Handle, m.Model, m.CWD, m.Launcher, m.LauncherArgs, m.ClaudeArgs, m.CodexArgs, m.PermissionAllowlist})
		}
		sort.Slice(members, func(i, j int) bool {
			return members[i].Role+"\x00"+members[i].Handle < members[j].Role+"\x00"+members[j].Handle
		})
		return struct {
			Members                                                                                           []memberIdentity
			Operator                                                                                          team.OperatorView
			Trust, Lead, Composition, ExecutionMode, ControlRoot, TargetProjectRoot, TargetContract, LeadMode string
			Orchestrated                                                                                      bool
		}{members, team.EffectiveOperator(t), t.Trust, t.Lead, team.EffectiveComposition(t), t.ExecutionMode, t.ControlRoot, t.TargetProjectRoot, t.TargetContract, team.EffectiveLeadMode(t), t.Orchestrated}
	}
	if !reflect.DeepEqual(project(source), project(target)) {
		return fmt.Errorf("source and target profile runnable roster/operator contracts are not compatible")
	}
	return nil
}

func profilePinsOnlySession(t team.Team, session, label string) error {
	if pin := strings.TrimSpace(t.Workstream); pin != "" && pin != session {
		return fmt.Errorf("%s profile workstream pin %q does not match endpoint session %q", label, pin, session)
	}
	for _, member := range t.Members {
		if pin := strings.TrimSpace(member.Session); pin != "" && pin != session {
			return fmt.Errorf("%s profile member %s pins session %q, not endpoint session %q", label, member.Role, pin, session)
		}
	}
	return nil
}

type migrationArtifactSpec struct {
	Name, Source, Target, Policy string
	Historical                   bool
}

func migrationArtifactSpecs(project string, source, target squadnamespace.Ref) []migrationArtifactSpec {
	profileScopedDir := func(name, profile, session string) string {
		base := filepath.Join(project, team.DirName, name)
		if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile {
			base = filepath.Join(base, squadnamespace.NormalizeProfile(profile))
		}
		return filepath.Join(base, session)
	}
	profileScopedFile := func(name, profile, session, suffix string) string {
		base := filepath.Join(project, team.DirName, name)
		if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile {
			base = filepath.Join(base, squadnamespace.NormalizeProfile(profile))
		}
		return filepath.Join(base, sanitizeWorkstreamName(session)+suffix)
	}
	alwaysProfileDir := func(name, profile, session string) string {
		return filepath.Join(project, team.DirName, name, squadnamespace.NormalizeProfile(profile), session)
	}
	return []migrationArtifactSpec{
		{Name: "amq_root", Source: source.AMQRoot, Target: target.AMQRoot, Policy: "rewrite_tree"},
		{Name: "brief", Source: source.Paths.Brief, Target: target.Paths.Brief, Policy: "copy_exact"},
		{Name: "tasks", Source: source.Paths.Tasks, Target: target.Paths.Tasks, Policy: "copy_exact"},
		{Name: "delivery_receipts", Source: profileScopedDir("receipts", source.Profile, source.Session), Target: profileScopedDir("receipts", target.Profile, target.Session), Policy: "rewrite_json_tree"},
		{Name: "goal_attempts", Source: profileScopedDir("goal-attempts", source.Profile, source.Session), Target: profileScopedDir("goal-attempts", target.Profile, target.Session), Policy: "rewrite_json_tree"},
		{Name: "collect_journal", Source: alwaysProfileDir("collect-journal", source.Profile, source.Session), Target: alwaysProfileDir("collect-journal", target.Profile, target.Session), Policy: "copy_exact"},
		{Name: "self_operator_evidence", Source: filepath.Join(alwaysProfileDir("evidence", source.Profile, source.Session), "self-operator"), Policy: "history_reference", Historical: true},
		{Name: "layout_finalization", Source: profileScopedFile("layout-finalization", source.Profile, source.Session, ".warning"), Target: profileScopedFile("layout-finalization", target.Profile, target.Session, ".warning"), Policy: "copy_exact"},
		{Name: "operator_loop", Source: profileScopedFile("operator-loop", source.Profile, source.Session, ".json"), Target: profileScopedFile("operator-loop", target.Profile, target.Session, ".json"), Policy: "rewrite_json"},
		{Name: "notification_watcher_runtime", Source: profileScopedFile("notification-watchers", source.Profile, source.Session, ".json"), Policy: "backup_only"},
		{Name: "notify_state", Source: defaultNotifyStatePath(project), Target: defaultNotifyStatePath(project), Policy: "shared_key_cas"},
		{Name: "namespace_audit", Source: filepath.Join(project, team.DirName, "namespace-audit", sanitizeWorkstreamName(source.Session)+".jsonl"), Policy: "history_reference", Historical: true},
		{Name: "boundary_audit", Source: filepath.Join(project, team.DirName, "boundary-audit", sanitizeWorkstreamName(source.Session)+".jsonl"), Policy: "history_reference", Historical: true},
		{Name: "operator_loop_audit", Source: profileScopedFile("operator-loop-audit", source.Profile, source.Session, ".jsonl"), Policy: "history_reference", Historical: true},
		{Name: "notification_watcher_log", Source: profileScopedFile(filepath.Join("notification-watchers", "logs"), source.Profile, source.Session, ".log"), Policy: "history_reference", Historical: true},
		{Name: "autonomy_audit", Source: autonomy.AuditPath(project, source.Session), Policy: "history_reference", Historical: true},
	}
}

func inspectMigrationArtifact(spec migrationArtifactSpec) (namespaceMigrationArtifact, bool, error) {
	if strings.TrimSpace(spec.Source) == "" {
		return namespaceMigrationArtifact{}, false, nil
	}
	info, err := os.Lstat(spec.Source)
	if os.IsNotExist(err) {
		return namespaceMigrationArtifact{}, false, nil
	}
	if err != nil {
		return namespaceMigrationArtifact{}, false, err
	}
	digest, bytes, files, dirs, err := migrationTreeDigest(spec.Source)
	if err != nil {
		return namespaceMigrationArtifact{}, false, err
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return namespaceMigrationArtifact{Name: spec.Name, Source: spec.Source, Target: spec.Target, Policy: spec.Policy, Kind: kind, SHA256: digest, Bytes: bytes, Mode: info.Mode().Perm(), FileCount: files, DirCount: dirs, Historical: spec.Historical}, true, nil
}

func migrationTreeDigest(root string) (string, int64, int, int, error) {
	h := sha256.New()
	var total int64
	files, dirs := 0, 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused: %s", path)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("special file refused: %s (%s)", path, info.Mode())
		}
		if info.Mode().IsRegular() && migrationLinkCount(info) > 1 {
			return fmt.Errorf("unexpected hard link refused: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00", filepath.ToSlash(rel), info.Mode().Perm(), info.Size())
		if info.IsDir() {
			dir, openedInfo, err := openNamespaceNoFollow(path, true)
			if err != nil {
				return err
			}
			if !os.SameFile(info, openedInfo) {
				_ = dir.Close()
				return fmt.Errorf("directory identity changed during digest: %s", path)
			}
			if err := dir.Close(); err != nil {
				return err
			}
			dirs++
			return nil
		}
		files++
		total += info.Size()
		f, openedInfo, err := openNamespaceNoFollow(path, false)
		if err != nil {
			return err
		}
		if migrationLinkCount(openedInfo) > 1 {
			_ = f.Close()
			return fmt.Errorf("unexpected hard link refused after open: %s", path)
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return "", 0, 0, 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), total, files, dirs, nil
}

func migrationRegularFileDigest(path string) (string, int64, error) {
	digest, size, files, _, err := migrationTreeDigest(path)
	if err != nil {
		return "", 0, err
	}
	if files != 1 {
		return "", 0, fmt.Errorf("expected regular file: %s", path)
	}
	return digest, size, nil
}

func migrationLinkCount(info os.FileInfo) uint64 {
	v := reflect.ValueOf(info.Sys())
	if !v.IsValid() {
		return 1
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return 1
	}
	f := v.FieldByName("Nlink")
	if !f.IsValid() {
		return 1
	}
	return f.Convert(reflect.TypeOf(uint64(0))).Uint()
}

func inspectNamespaceEndpointLiveness(label string, t team.Team, ref squadnamespace.Ref, probe duplicateLaunchProbe, now time.Time, blockers *[]string) []namespaceMigrationLiveness {
	agentsRoot := filepath.Join(ref.AMQRoot, "agents")
	entries, err := os.ReadDir(agentsRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		*blockers = append(*blockers, fmt.Sprintf("%s liveness scan: %v", label, err))
		return nil
	}
	memberByHandle := map[string]team.Member{}
	for _, m := range t.Members {
		h := strings.TrimSpace(m.Handle)
		if h == "" {
			h = m.Role
		}
		memberByHandle[h] = m
	}
	var out []namespaceMigrationLiveness
	for _, entry := range entries {
		if !entry.IsDir() {
			*blockers = append(*blockers, fmt.Sprintf("%s agent entry %s is not a directory", label, entry.Name()))
			continue
		}
		agentDir := filepath.Join(agentsRoot, entry.Name())
		m := memberByHandle[entry.Name()]
		if rec, err := launch.Read(agentDir); err == nil {
			m.Role, m.Handle, m.Binary, m.CWD = rec.Role, rec.Handle, rec.Binary, rec.CWD
		} else if _, statErr := os.Lstat(launch.Path(agentDir)); statErr == nil {
			*blockers = append(*blockers, fmt.Sprintf("%s agent %s launch record is malformed: %v", label, entry.Name(), err))
			continue
		}
		if m.Handle == "" {
			m.Handle = entry.Name()
		}
		if _, err := os.Lstat(filepath.Join(agentDir, ".wake.lock")); err == nil {
			if _, readErr := readWakeLock(agentDir); readErr != nil {
				*blockers = append(*blockers, fmt.Sprintf("%s agent %s wake ownership is malformed: %v", label, entry.Name(), readErr))
				continue
			}
		} else if !os.IsNotExist(err) {
			*blockers = append(*blockers, fmt.Sprintf("%s agent %s wake ownership is unreadable: %v", label, entry.Name(), err))
			continue
		}
		if _, err := os.Lstat(filepath.Join(agentDir, "presence.json")); err == nil {
			if _, readErr := readPresenceForEntry(agentDir); readErr != nil {
				*blockers = append(*blockers, fmt.Sprintf("%s agent %s presence is malformed: %v", label, entry.Name(), readErr))
				continue
			}
		} else if !os.IsNotExist(err) {
			*blockers = append(*blockers, fmt.Sprintf("%s agent %s presence is unreadable: %v", label, entry.Name(), err))
			continue
		}
		live := classifyAgentLiveness(agentDir, ref.AMQRoot, ref.Profile, m.Handle, m.Role, m.Binary, ref.Session, m.EffectiveCWD(t.Project), probe)
		out = append(out, namespaceMigrationLiveness{Endpoint: label, Handle: entry.Name(), State: string(live.Verdict), Detail: live.Detail})
		if live.Live() {
			*blockers = append(*blockers, fmt.Sprintf("%s endpoint member %s is live: %s", label, entry.Name(), live.Detail))
		} else if live.Verdict == livenessStale {
			*blockers = append(*blockers, fmt.Sprintf("%s endpoint member %s has ambiguous stale ownership: %s", label, entry.Name(), live.Detail))
		}
	}
	return out
}

func inspectNamespaceOperationalOwners(label, project string, t team.Team, ref squadnamespace.Ref, now time.Time, plan *namespaceMigrationPlan) {
	leasePath := operatorLoopLeasePath(project, ref.Profile, ref.Session)
	if info, err := os.Lstat(leasePath); err == nil && info.Size() == 0 {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s operator lease is empty and ambiguous", label))
	} else if err != nil && !os.IsNotExist(err) {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s operator lease is unreadable: %v", label, err))
	} else if lease, err := readOperatorLoopLease(leasePath); err != nil {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s operator lease: %v", label, err))
	} else if strings.TrimSpace(lease.OwnerID) != "" && now.Before(lease.LeaseExpiresAt) {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s operator lease is active for %s until %s", label, lease.OwnerID, lease.LeaseExpiresAt.UTC().Format(time.RFC3339)))
	}
	watchPath := notificationWatcherRuntimePath(project, ref.Profile, ref.Session)
	if info, err := os.Lstat(watchPath); err == nil && info.Size() == 0 {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s notification watcher runtime is empty and ambiguous", label))
	} else if err != nil && !os.IsNotExist(err) {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s notification watcher runtime is unreadable: %v", label, err))
	} else if watcher, err := readNotificationWatcherRecord(watchPath); err != nil {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s notification watcher: %v", label, err))
	} else if watcher.Expected && strings.TrimSpace(watcher.OwnerToken) != "" {
		host, _ := os.Hostname()
		local := strings.TrimSpace(watcher.Host) == "" || strings.TrimSpace(watcher.Host) == strings.TrimSpace(host)
		switch {
		case local && notificationWatcherProcessMatches(watcher):
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s notification watcher process is live for pid %d", label, watcher.PID))
		case local && watcher.PID > 0 && notificationWatcherPIDAlive(watcher.PID):
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s notification watcher pid %d is live but its process identity is ambiguous", label, watcher.PID))
		case now.Before(watcher.LeaseExpiresAt):
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s notification watcher ownership is active until %s", label, watcher.LeaseExpiresAt.UTC().Format(time.RFC3339)))
		}
	}
	_ = t
}

func inspectNamespaceLocks(project string, source, target squadnamespace.Ref, artifacts []namespaceMigrationArtifact, ownsEndpointLocks bool, plan *namespaceMigrationPlan) {
	seen := map[string]bool{}
	explicit := []string{
		operatorLoopLeaseLockPath(project, source.Profile, source.Session), operatorLoopLeaseLockPath(project, target.Profile, target.Session),
		notificationWatcherLockPath(project, source.Profile, source.Session), notificationWatcherLockPath(project, target.Profile, target.Session),
	}
	if !ownsEndpointLocks {
		explicit = append(explicit, team.ProfileLockPath(project, source.Profile), team.ProfileLockPath(project, target.Profile))
	}
	for _, path := range explicit {
		inspectNamespaceLock(path, seen, plan)
	}
	for _, artifact := range artifacts {
		if artifact.Historical {
			continue
		}
		walkErr := filepath.WalkDir(artifact.Source, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") || strings.HasSuffix(entry.Name(), ".wake.lock") {
				return nil
			}
			inspectNamespaceLock(path, seen, plan)
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("scan locks under %s: %v", artifact.Source, walkErr))
		}
	}
}

func inspectNamespaceLock(path string, seen map[string]bool, plan *namespaceMigrationPlan) {
	if seen[path] {
		return
	}
	seen[path] = true
	lock, acquired, err := flock.TryExclusive(path, false)
	if lock != nil {
		_ = lock.Close()
	}
	if err != nil {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("lock ownership unknown for %s: %v", path, err))
	} else if !acquired {
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("lock is held: %s", path))
	}
}

func migrationSpaceEvidence(project string, artifacts []namespaceMigrationArtifact) (uint64, uint64, error) {
	info, err := os.Stat(project)
	if err != nil {
		return 0, 0, err
	}
	device := migrationDevice(info)
	for _, artifact := range artifacts {
		info, err := os.Stat(artifact.Source)
		if err != nil {
			return 0, 0, fmt.Errorf("stat artifact %s: %w", artifact.Name, err)
		}
		if migrationDevice(info) != device {
			return 0, 0, fmt.Errorf("artifact %s is on another device; atomic rename would fail with EXDEV", artifact.Name)
		}
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(project, &stat); err != nil {
		return device, 0, fmt.Errorf("stat filesystem space: %w", err)
	}
	return device, uint64(stat.Bavail) * uint64(stat.Bsize), nil
}

func migrationDevice(info os.FileInfo) uint64 {
	v := reflect.ValueOf(info.Sys())
	if v.IsValid() && v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.Struct {
		if field := v.FieldByName("Dev"); field.IsValid() {
			return field.Convert(reflect.TypeOf(uint64(0))).Uint()
		}
	}
	return 0
}

func namespaceMigrationPlanFingerprint(plan namespaceMigrationPlan) string {
	copy := plan
	copy.ID = ""
	copy.DryRun = false
	copy.Fingerprint = ""
	copy.Recovery = ""
	copy.Liveness = nil
	copy.Blockers = nil
	copy.Warnings = nil
	copy.Inputs.AvailableBytes = 0
	b, _ := json.Marshal(copy)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum)
}

func dedupeMigrationStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
