package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

var externalOrchestratorMailboxContainmentHook = func(string, string) error { return nil }

type externalOrchestratorLifecycle struct {
	Registration externalOrchestratorRegistration
	Root         string
	AgentDir     string
}

func finishExternalOrchestratorMailboxVerification(lifecycle externalOrchestratorLifecycle, invokedEvidence externalOrchestratorTransitionEvidence, initErr error) (externalOrchestratorLifecycle, error) {
	detail := "exact canonical-root external orchestrator mailbox verified"
	if initErr != nil {
		detail += "; AMQ init returned nonzero after producing the verified mailbox: " + initErr.Error()
	}
	evidence := externalOrchestratorTransitionEvidence{
		AttemptID:     invokedEvidence.AttemptID,
		CanonicalRoot: lifecycle.Root,
		MailboxPath:   lifecycle.AgentDir,
		Outcome:       "verified",
		Detail:        detail,
	}
	record, _, err := transitionExternalOrchestratorRegistration(lifecycle.Registration.Identity.Scope, lifecycle.Registration.Generation, externalOrchestratorStateMailboxVerified, evidence, time.Now().UTC())
	if err != nil {
		return lifecycle, err
	}
	lifecycle.Registration = record
	return lifecycle, nil
}

func canonicalExternalOrchestratorPath(path string) (string, error) {
	canonical := canonicalFilesystemPath(path)
	if canonical == "" {
		return "", fmt.Errorf("AMQ root path is empty")
	}
	return canonical, nil
}

func verifyExternalOrchestratorConfigAgents(rootPath string, expected []string) error {
	agents, err := readExternalOrchestratorConfigAgents(rootPath)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, handle := range agents {
		present[handle] = true
	}
	for _, handle := range expected {
		if !present[handle] {
			return fmt.Errorf("AMQ config missing required agent %q", handle)
		}
	}
	return nil
}

func readExternalOrchestratorConfigAgents(rootPath string) ([]string, error) {
	before, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("AMQ root must be a non-symlink directory")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := statExternalOrchestratorRoot(root)
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("AMQ root identity changed while reading config")
	}
	metaInfo, err := root.Lstat("meta")
	if err != nil {
		return nil, err
	}
	if metaInfo.Mode()&os.ModeSymlink != 0 || !metaInfo.IsDir() {
		return nil, fmt.Errorf("AMQ meta must be a non-symlink directory")
	}
	meta, err := root.OpenRoot("meta")
	if err != nil {
		return nil, err
	}
	defer meta.Close()
	openedMeta, openErr := statExternalOrchestratorRoot(meta)
	afterMeta, afterErr := root.Lstat("meta")
	if openErr != nil || afterErr != nil || afterMeta.Mode()&os.ModeSymlink != 0 || !afterMeta.IsDir() || !os.SameFile(metaInfo, openedMeta) || !os.SameFile(afterMeta, openedMeta) {
		return nil, fmt.Errorf("AMQ meta identity changed while reading config")
	}
	info, err := meta.Lstat("config.json")
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("AMQ config must be a non-symlink regular file")
	}
	f, err := meta.Open("config.json")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	openedInfo, openErr := f.Stat()
	afterInfo, afterErr := meta.Lstat("config.json")
	if openErr != nil || afterErr != nil || afterInfo.Mode()&os.ModeSymlink != 0 || !afterInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || !os.SameFile(afterInfo, openedInfo) {
		return nil, fmt.Errorf("AMQ config identity changed while opening")
	}
	var config struct {
		Agents []string `json:"agents"`
	}
	if err := json.NewDecoder(f).Decode(&config); err != nil {
		return nil, fmt.Errorf("decode AMQ config: %w", err)
	}
	return config.Agents, nil
}

func markExternalOrchestratorMailboxUncertain(lifecycle externalOrchestratorLifecycle, prior externalOrchestratorTransitionEvidence, cause error) (externalOrchestratorLifecycle, error) {
	record := lifecycle.Registration
	if record.State != externalOrchestratorStateMailboxUncertain {
		evidence := prior
		evidence.Outcome = "uncertain"
		evidence.Detail = cause.Error()
		var err error
		record, _, err = transitionExternalOrchestratorRegistration(record.Identity.Scope, record.Generation, externalOrchestratorStateMailboxUncertain, evidence, time.Now().UTC())
		if err != nil {
			return lifecycle, fmt.Errorf("%v; persist mailbox_uncertain transition: %w", cause, err)
		}
		lifecycle.Registration = record
	}
	return lifecycle, fmt.Errorf("external orchestrator mailbox provisioning is uncertain; goal delivery blocked: %w", cause)
}

func verifyExternalOrchestratorMailbox(rootPath, handle string) error {
	canonical, err := canonicalExternalOrchestratorPath(rootPath)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(rootPath) {
		return fmt.Errorf("AMQ root is not the exact canonical root %q", rootPath)
	}
	before, err := os.Lstat(rootPath)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return fmt.Errorf("AMQ root must be an existing non-symlink directory: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := statExternalOrchestratorRoot(root)
	if err != nil || !os.SameFile(before, opened) {
		return fmt.Errorf("AMQ root identity changed during verification")
	}
	for _, relative := range []string{"inbox/new", "inbox/cur", "inbox/tmp", "outbox/sent", "dlq/new", "dlq/cur", "dlq/tmp"} {
		path := filepath.Join("agents", handle, filepath.FromSlash(relative))
		if err := verifyExternalOrchestratorDirectoryPath(root, rootPath, path); err != nil {
			return err
		}
	}
	return nil
}

func verifyExternalOrchestratorDirectoryPath(base *os.Root, basePath, relative string) error {
	components := strings.FieldsFunc(filepath.Clean(relative), func(r rune) bool { return r == '/' || r == '\\' })
	if len(components) == 0 {
		return fmt.Errorf("mailbox verification requires a contained directory path")
	}
	current := base
	currentPath := basePath
	owned := false
	defer func() {
		if owned {
			_ = current.Close()
		}
	}()
	for _, component := range components {
		componentPath := filepath.Join(currentPath, component)
		before, err := current.Lstat(component)
		if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return fmt.Errorf("mailbox component %s is missing or unsafe", componentPath)
		}
		if err := externalOrchestratorMailboxContainmentHook("after_component_validation", componentPath); err != nil {
			return err
		}
		child, err := current.OpenRoot(component)
		if err != nil {
			return fmt.Errorf("open mailbox component %s: %w", componentPath, err)
		}
		opened, openErr := statExternalOrchestratorRoot(child)
		after, afterErr := current.Lstat(component)
		if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
			_ = child.Close()
			return fmt.Errorf("mailbox component identity changed during verification: %s", componentPath)
		}
		if owned {
			_ = current.Close()
		}
		current = child
		currentPath = componentPath
		owned = true
	}
	return nil
}

func verifyExternalOrchestratorLaunch(lifecycle externalOrchestratorLifecycle) error {
	rec, err := launch.Read(lifecycle.AgentDir)
	if err != nil {
		return err
	}
	identity := lifecycle.Registration.Identity
	if !rec.External || rec.Handle != identity.Scope.Handle || rec.Role != goalOrchestratorRole || rec.Session != identity.Scope.Session || rec.Root != lifecycle.Root || rec.Tmux == nil || rec.Tmux.PaneID != identity.Runtime.PaneID {
		return errors.New("external orchestrator launch record does not match registered identity")
	}
	return nil
}
