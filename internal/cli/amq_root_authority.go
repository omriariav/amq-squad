package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const amqRootConfigVersion = 1

var amqRootAuthorityNow = func() time.Time { return time.Now().UTC() }

type amqRootConfigDocument struct {
	Version    int      `json:"version"`
	CreatedUTC string   `json:"created_utc"`
	Agents     []string `json:"agents"`
}

type amqRootConfigRepair struct {
	Path         string
	Changed      bool
	CreatedPaths []string
}

type amqRootAuthorityRepair struct {
	Config              amqRootConfigRepair
	MailboxCreatedPaths []string
}

func amqAuthorityHandles(t team.Team) []string {
	handles := make([]string, 0, len(t.Members)+1)
	for _, member := range t.Members {
		handles = append(handles, memberHandle(member))
	}
	operator := team.EffectiveOperator(t)
	if operator.Enabled {
		handles = append(handles, operator.Handle)
	}
	return normalizeAMQAuthorityHandles(handles)
}

func normalizeAMQAuthorityHandles(handles []string) []string {
	unique := make(map[string]struct{}, len(handles))
	for _, handle := range handles {
		handle = strings.TrimSpace(handle)
		if handle != "" {
			unique[handle] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(unique))
	for handle := range unique {
		normalized = append(normalized, handle)
	}
	sort.Strings(normalized)
	return normalized
}

// reconcileAMQRootConfig makes the selected session root authoritative for
// explicit-root commands. Existing documents are parsed before
// mutation, their creation timestamp and unknown fields are preserved, and
// publication is a same-directory atomic rename.
func reconcileAMQRootConfig(root string, handles []string) (amqRootConfigRepair, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	result := amqRootConfigRepair{Path: filepath.Join(root, "meta", "config.json")}
	if root == "" || root == "." {
		return result, fmt.Errorf("AMQ root is required")
	}
	handles = normalizeAMQAuthorityHandles(handles)
	if len(handles) == 0 {
		return result, fmt.Errorf("AMQ root %s has no authority handles", root)
	}
	if _, err := directoryExists(root); err != nil {
		return result, err
	}
	if _, err := directoryExists(filepath.Dir(result.Path)); err != nil {
		return result, err
	}

	configRaw, configMode, exists, err := readAMQRootConfig(result.Path)
	if err != nil {
		return result, err
	}
	var document map[string]json.RawMessage
	if exists {
		document, err = decodeAMQRootConfig(result.Path, configRaw)
		if err != nil {
			return result, err
		}
		var current []string
		if err := json.Unmarshal(document["agents"], &current); err != nil {
			return result, fmt.Errorf("decode AMQ root agents at %s: %w", result.Path, err)
		}
		if equalStrings(current, handles) {
			return result, nil
		}
	} else {
		document = map[string]json.RawMessage{}
		version, _ := json.Marshal(amqRootConfigVersion)
		createdUTC, _ := json.Marshal(amqRootAuthorityNow().UTC().Format(time.RFC3339Nano))
		document["version"] = version
		document["created_utc"] = createdUTC
		configMode = 0o600
	}
	agents, err := json.Marshal(handles)
	if err != nil {
		return result, fmt.Errorf("encode AMQ root agents: %w", err)
	}
	document["agents"] = agents
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode AMQ root config: %w", err)
	}
	encoded = append(encoded, '\n')

	rootCreated, err := ensureLaunchDirectoryTracked(root)
	result.CreatedPaths = append(result.CreatedPaths, rootCreated...)
	if err != nil {
		return result, err
	}
	metaPath := filepath.Dir(result.Path)
	metaExisted, err := directoryExists(metaPath)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(metaPath, 0o755); err != nil {
		return result, fmt.Errorf("create AMQ root metadata directory %s: %w", metaPath, err)
	}
	if !metaExisted {
		result.CreatedPaths = append(result.CreatedPaths, metaPath)
	}
	if err := writeAMQRootConfigAtomic(result.Path, encoded, configMode); err != nil {
		return result, err
	}
	if !exists {
		result.CreatedPaths = append(result.CreatedPaths, result.Path)
	}
	result.Changed = true
	return result, nil
}

func readAMQRootConfig(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, fmt.Errorf("inspect AMQ root config %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("AMQ root config %s must be a non-symlink regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read AMQ root config %s: %w", path, err)
	}
	return data, info.Mode().Perm(), true, nil
}

func decodeAMQRootConfig(path string, data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var document map[string]json.RawMessage
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode AMQ root config %s: %w", path, err)
	}
	if document == nil {
		return nil, fmt.Errorf("decode AMQ root config %s: expected JSON object", path)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("additional JSON value")
		}
		return nil, fmt.Errorf("decode AMQ root config %s: trailing JSON content: %w", path, err)
	}
	var version int
	if err := json.Unmarshal(document["version"], &version); err != nil || version != amqRootConfigVersion {
		return nil, fmt.Errorf("decode AMQ root config %s: unsupported or missing version", path)
	}
	var createdUTC string
	if err := json.Unmarshal(document["created_utc"], &createdUTC); err != nil || strings.TrimSpace(createdUTC) == "" {
		return nil, fmt.Errorf("decode AMQ root config %s: invalid or missing created_utc", path)
	}
	if _, err := time.Parse(time.RFC3339Nano, createdUTC); err != nil {
		return nil, fmt.Errorf("decode AMQ root config %s: invalid created_utc: %w", path, err)
	}
	var agents []string
	if err := json.Unmarshal(document["agents"], &agents); err != nil {
		return nil, fmt.Errorf("decode AMQ root config %s: invalid or missing agents: %w", path, err)
	}
	return document, nil
}

func writeAMQRootConfigAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".amq-root-config-*")
	if err != nil {
		return fmt.Errorf("stage AMQ root config %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if mode == 0 {
		mode = 0o600
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set staged AMQ root config mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write staged AMQ root config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync staged AMQ root config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged AMQ root config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish AMQ root config %s: %w", path, err)
	}
	if opened, err := os.Open(dir); err == nil {
		_ = opened.Sync()
		_ = opened.Close()
	}
	return nil
}

func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("%s must be a non-symlink directory", path)
	}
	return true, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type amqMailboxRepairReport struct {
	MailboxRepair *struct {
		CreatedPaths []string `json:"created_paths"`
	} `json:"mailbox_repair"`
	Summary struct {
		Error int `json:"error"`
	} `json:"summary"`
}

func repairAMQRootMailboxes(projectDir, root string) ([]string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return nil, fmt.Errorf("AMQ root is required")
	}
	env := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	env = append(env, "AM_ROOT="+root)
	out, err := runAMQCommand(amqCommandRequest{
		Dir: projectDir,
		Env: env,
		Arg: []string{"doctor", "--root", root, "--fix-mailboxes", "--json"},
	})
	if err != nil {
		return nil, fmt.Errorf("repair AMQ mailboxes at %s: %w", root, err)
	}
	var report amqMailboxRepairReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("decode AMQ mailbox repair at %s: %w", root, err)
	}
	if report.Summary.Error != 0 {
		return nil, fmt.Errorf("repair AMQ mailboxes at %s: doctor reported %d error(s)", root, report.Summary.Error)
	}
	if report.MailboxRepair == nil {
		return nil, fmt.Errorf("repair AMQ mailboxes at %s: doctor omitted mailbox_repair", root)
	}
	return append([]string(nil), report.MailboxRepair.CreatedPaths...), nil
}

func repairAMQRootAuthority(projectDir, root string, handles []string) (amqRootAuthorityRepair, error) {
	var result amqRootAuthorityRepair
	config, err := reconcileAMQRootConfig(root, handles)
	result.Config = config
	if err != nil {
		return result, err
	}
	created, err := repairAMQRootMailboxes(projectDir, root)
	result.MailboxCreatedPaths = created
	if err != nil {
		return result, err
	}
	return result, nil
}

type amqRootAuthorityEnvResolver func(projectDir, profile, session, handle string) (amqEnv, error)

// repairTeamAMQRootAuthority upgrades existing live roots to the canonical
// root contract required by every supported AMQ release. detail controls
// whether every created/rewritten path is logged individually: doctor's
// explicit --fix-amq-root repair always wants the full trail, while the
// incidental mailbox bootstrap on 'resume --exec' (#722) collapses to one
// summary line by default and only expands with --verbose.
func repairTeamAMQRootAuthority(t team.Team, profile, workstream string, out io.Writer, resolve amqRootAuthorityEnvResolver, detail bool) error {
	if resolve == nil {
		resolve = resolveAMQEnvForTeamProfile
	}
	if out == nil {
		out = io.Discard
	}
	verbose := detail
	handles := amqAuthorityHandles(t)
	seen := map[string]bool{}
	bootstrapped := 0
	for _, member := range orderedTeamMembers(t.Members) {
		cwd := member.EffectiveCWD(t.Project)
		env, err := resolve(cwd, profile, workstream, memberHandle(member))
		if err != nil {
			return fmt.Errorf("resolve AMQ root authority for %s: %w", member.Role, err)
		}
		root := absoluteAMQRoot(cwd, env.Root)
		if seen[root] {
			continue
		}
		seen[root] = true
		repair, err := repairAMQRootAuthority(cwd, root, handles)
		if err != nil {
			return fmt.Errorf("repair AMQ root authority for %s: %w", member.Role, err)
		}
		for _, path := range repair.Config.CreatedPaths {
			if filepath.Clean(path) == filepath.Clean(repair.Config.Path) {
				continue
			}
			bootstrapped++
			if verbose {
				fmt.Fprintf(out, "AMQ root authority: created %s\n", path)
			}
		}
		if repair.Config.Changed {
			bootstrapped++
			if verbose {
				fmt.Fprintf(out, "AMQ root authority: wrote %s\n", repair.Config.Path)
			}
		}
		for _, path := range repair.MailboxCreatedPaths {
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, filepath.FromSlash(path))
			}
			bootstrapped++
			if verbose {
				fmt.Fprintf(out, "AMQ root authority: created %s\n", path)
			}
		}
	}
	// The per-path detail above is only useful when actively debugging a
	// mailbox layout; a fresh session can otherwise legitimately bootstrap
	// dozens of paths and bury the launch table (#722). Collapse it to one
	// summary line by default, full detail stays behind --verbose.
	if !verbose && bootstrapped > 0 {
		fmt.Fprintf(out, "AMQ root authority: bootstrapped %d path(s) (use --verbose for detail)\n", bootstrapped)
	}
	return nil
}

func launchAMQAuthorityHandles(projectDir, profile, session, root, handle string) ([]string, error) {
	if team.ExistsProfile(projectDir, profile) {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return nil, fmt.Errorf("read team for AMQ launch authority: %w", err)
		}
		active, _ := filterMembersBySession(t.Members, session)
		t.Members = active
		return normalizeAMQAuthorityHandles(append(amqAuthorityHandles(t), handle)), nil
	}
	handles := []string{handle, team.DefaultOperatorHandle}
	path := filepath.Join(root, "meta", "config.json")
	data, _, exists, err := readAMQRootConfig(path)
	if err != nil {
		return nil, err
	}
	if exists {
		document, err := decodeAMQRootConfig(path, data)
		if err != nil {
			return nil, err
		}
		var current []string
		if err := json.Unmarshal(document["agents"], &current); err != nil {
			return nil, fmt.Errorf("decode AMQ launch authority agents: %w", err)
		}
		handles = append(handles, current...)
	}
	return normalizeAMQAuthorityHandles(handles), nil
}

type amqRosterConfigSyncTarget struct {
	Root     string
	Handles  []string
	Snapshot launchFileSnapshot
}

type amqRosterConfigCandidate struct {
	CWD     string
	Session string
	Handle  string
}

// writeTeamProfileWithAMQRosterSyncUnderLock persists a roster mutation and
// updates every existing session config affected by the old/new team.
// The caller owns the profile lock. Configs are validated and snapshotted
// before the profile write; any later failure rolls both authorities back.
func writeTeamProfileWithAMQRosterSyncUnderLock(projectDir, profile string, before, after team.Team, resolve amqRootAuthorityEnvResolver) error {
	targets, err := planAMQRosterConfigSync(before, after, profile, resolve)
	if err != nil {
		return err
	}
	if err := team.WriteProfileUnderLock(projectDir, profile, after); err != nil {
		return err
	}
	var applied []amqRosterConfigSyncTarget
	for _, target := range targets {
		if _, err := reconcileAMQRootConfig(target.Root, target.Handles); err != nil {
			attempted := append(append([]amqRosterConfigSyncTarget(nil), applied...), target)
			rollbackErr := rollbackAMQRosterConfigSync(projectDir, profile, before, attempted)
			return errorsJoinWithContext(
				fmt.Errorf("sync AMQ roster config %s: %w", target.Snapshot.Path, err),
				rollbackErr,
			)
		}
		applied = append(applied, target)
	}
	return nil
}

func planAMQRosterConfigSync(before, after team.Team, profile string, resolve amqRootAuthorityEnvResolver) ([]amqRosterConfigSyncTarget, error) {
	if resolve == nil {
		resolve = resolveAMQEnvForTeamProfile
	}
	candidates := amqRosterConfigCandidates(before, after)
	targets := map[string]amqRosterConfigSyncTarget{}
	for _, candidate := range candidates {
		if !shouldResolveExistingAMQRoot(candidate.CWD, profile, candidate.Session) {
			continue
		}
		env, err := resolve(candidate.CWD, profile, candidate.Session, candidate.Handle)
		if err != nil {
			return nil, fmt.Errorf("resolve AMQ roster config for session %s: %w", candidate.Session, err)
		}
		root := absoluteAMQRoot(candidate.CWD, env.Root)
		exists, err := directoryExists(root)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		if _, ok := targets[root]; ok {
			continue
		}
		active, _ := filterMembersBySession(after.Members, candidate.Session)
		sessionTeam := after
		sessionTeam.Members = active
		path := filepath.Join(root, "meta", "config.json")
		snapshot, err := captureLaunchFileSnapshot(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot AMQ roster config %s: %w", path, err)
		}
		if snapshot.Exists {
			if _, err := decodeAMQRootConfig(path, snapshot.Data); err != nil {
				return nil, err
			}
		}
		targets[root] = amqRosterConfigSyncTarget{
			Root:     root,
			Handles:  amqAuthorityHandles(sessionTeam),
			Snapshot: snapshot,
		}
	}
	ordered := make([]amqRosterConfigSyncTarget, 0, len(targets))
	for _, target := range targets {
		ordered = append(ordered, target)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Root < ordered[j].Root })
	return ordered, nil
}

func amqRosterConfigCandidates(before, after team.Team) []amqRosterConfigCandidate {
	unique := map[string]amqRosterConfigCandidate{}
	addTeam := func(t team.Team) {
		sessions := map[string]bool{}
		for _, member := range t.Members {
			if session := strings.TrimSpace(member.Session); session != "" {
				sessions[session] = true
			}
		}
		if session := strings.TrimSpace(inheritedSession(t)); session != "" {
			sessions[session] = true
		}
		if session := strings.TrimSpace(t.Workstream); session != "" {
			sessions[session] = true
		}
		for _, member := range t.Members {
			cwd := member.EffectiveCWD(t.Project)
			for session := range sessions {
				key := cwd + "\x00" + session
				if _, exists := unique[key]; !exists {
					unique[key] = amqRosterConfigCandidate{CWD: cwd, Session: session, Handle: memberHandle(member)}
				}
			}
		}
	}
	addTeam(before)
	addTeam(after)
	ordered := make([]amqRosterConfigCandidate, 0, len(unique))
	for _, candidate := range unique {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CWD == ordered[j].CWD {
			return ordered[i].Session < ordered[j].Session
		}
		return ordered[i].CWD < ordered[j].CWD
	})
	return ordered
}

func shouldResolveExistingAMQRoot(cwd, profile, session string) bool {
	canonical := squadnamespace.AMQRoot(cwd, profile, session)
	if info, err := os.Stat(canonical); err == nil && info.IsDir() {
		return true
	}
	if _, err := os.Stat(filepath.Join(cwd, ".amqrc")); err == nil {
		return true
	}
	inherited := strings.TrimSpace(os.Getenv("AM_ROOT"))
	if inherited == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(cwd), filepath.Clean(inherited))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func rollbackAMQRosterConfigSync(projectDir, profile string, before team.Team, applied []amqRosterConfigSyncTarget) error {
	var rollbackErrs []error
	for i := len(applied) - 1; i >= 0; i-- {
		if err := restoreAMQRootConfigSnapshot(applied[i].Snapshot); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore AMQ roster config %s: %w", applied[i].Snapshot.Path, err))
		}
	}
	if err := team.WriteProfileUnderLock(projectDir, profile, before); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore team profile: %w", err))
	}
	return errors.Join(rollbackErrs...)
}

func restoreAMQRootConfigSnapshot(snapshot launchFileSnapshot) error {
	if !snapshot.Exists {
		if err := os.Remove(snapshot.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeAMQRootConfigAtomic(snapshot.Path, snapshot.Data, snapshot.Mode)
}

func errorsJoinWithContext(primary, rollback error) error {
	if rollback == nil {
		return primary
	}
	return fmt.Errorf("%w; rollback failed: %v", primary, rollback)
}
