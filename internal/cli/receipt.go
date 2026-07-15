package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func runReceipt(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad receipt - inspect and refresh durable send receipts

Usage:
  amq-squad receipt show MESSAGE_ID [--project DIR] [--profile NAME] [--session NAME] [--json]

The lookup uses each recorded recipient identity when querying native AMQ
receipts. Direct invocations of the external raw amq binary are outside this
local projection and cannot be discovered by this command.
`)
		return nil
	}
	if args[0] != "show" {
		return usageErrorf("unknown receipt subcommand %q; use show", args[0])
	}
	return runReceiptShow(args[1:])
}

func runReceiptShow(args []string) error {
	fs := flag.NewFlagSet("receipt show", flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory")
	profile := fs.String("profile", "", "team profile namespace")
	session := fs.String("session", "", "workstream/session selector")
	jsonOut := fs.Bool("json", false, "emit a JSON envelope")
	registerScopedFlagAliases(fs, project, session, profile)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: amq-squad receipt show MESSAGE_ID [--project DIR] [--profile NAME] [--session NAME] [--json]\n")
	}
	messageID := ""
	parseArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		messageID = strings.TrimSpace(args[0])
		parseArgs = args[1:]
	}
	if err := parseFlags(fs, parseArgs); err != nil {
		return err
	}
	if messageID == "" && fs.NArg() == 1 {
		messageID = strings.TrimSpace(fs.Arg(0))
	} else if fs.NArg() != 0 {
		return usageErrorf("receipt show requires exactly one MESSAGE_ID")
	}
	if messageID == "" {
		return usageErrorf("receipt show requires exactly one MESSAGE_ID")
	}
	projectDir, resolvedProfile, err := resolveProjectProfile(*project, *profile, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	matches, err := findScopedDeliveryReceipts(projectDir, resolvedProfile, strings.TrimSpace(*session), messageID)
	if err != nil {
		return fmt.Errorf("receipt show %s: %w", messageID, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("receipt show %s: no durable amq-squad receipt found in project %s profile %s", messageID, projectDir, resolvedProfile)
	}
	if len(matches) != 1 {
		paths := make([]string, 0, len(matches))
		for _, match := range matches {
			paths = append(paths, match.Path)
		}
		return fmt.Errorf("receipt show %s: %d matching records across scope (%s); pass --profile and --session to select exactly one", messageID, len(matches), strings.Join(paths, ", "))
	}
	receipt := matches[0]
	selectedSession := strings.TrimSpace(receipt.Target.Session)
	var refreshErr error
	receipt, err = updateDeliveryReceiptLocked(projectDir, resolvedProfile, selectedSession, receipt.AttemptID, func(current *deliveryReceiptData) error {
		if current.MessageID != messageID {
			return fmt.Errorf("receipt message mapping changed under lock: wanted %s, found %s", messageID, current.MessageID)
		}
		if err := validateReceiptProvenance(*current, projectDir, resolvedProfile, selectedSession, current.Path); err != nil {
			return err
		}
		refreshErr = refreshDeliveryReceipt(current, projectDir, resolvedProfile, selectedSession)
		return nil // persist last_checked/error without presenting it as success
	})
	if err != nil {
		return fmt.Errorf("receipt show %s (attempt_id=%s state=%s path=%s): locked refresh: %w", messageID, receipt.AttemptID, receipt.DeliveryState, receipt.Path, err)
	}
	if refreshErr != nil {
		return fmt.Errorf("receipt show %s (attempt_id=%s state=%s path=%s): %w", messageID, receipt.AttemptID, receipt.DeliveryState, receipt.Path, refreshErr)
	}
	if *jsonOut {
		if err := printJSONEnvelope("receipt_show", receipt); err != nil {
			return err
		}
	} else {
		fmt.Printf("Receipt %s: state=%s attempt=%s recipients=%s path=%s", receipt.MessageID, receipt.DeliveryState, receipt.AttemptID, strings.Join(receipt.Recipients, ","), receipt.Path)
		if receipt.DrainedAt != nil {
			fmt.Printf(" drained_at=%s", receipt.DrainedAt.Format(time.RFC3339Nano))
		}
		fmt.Println()
	}
	return nil
}

func findScopedDeliveryReceipts(projectDir, profile, session, messageID string) ([]deliveryReceiptData, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, nil
	}
	normalizedProfile := squadnamespace.NormalizeProfile(profile)
	if session != "" {
		root, dir, err := openReceiptDirRoot(projectDir, normalizedProfile, session, false)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer root.Close()
		return scanReceiptDirectory(root, dir, projectDir, normalizedProfile, session, messageID)
	}
	namedProfileRoots := map[string]bool{}
	if normalizedProfile == team.DefaultProfile {
		profiles, err := team.ListProfiles(projectDir)
		if err != nil {
			return nil, err
		}
		for _, named := range profiles {
			namedProfileRoots[named] = true
		}
	}
	baseRoot, base, err := openReceiptBaseRoot(projectDir, normalizedProfile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer baseRoot.Close()
	dirFile, err := baseRoot.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dirFile.ReadDir(-1)
	dirFile.Close()
	if err != nil {
		return nil, err
	}
	var found []deliveryReceiptData
	for _, entry := range entries {
		if namedProfileRoots[entry.Name()] {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing symlink in delivery receipt scope: %s", filepath.Join(base, entry.Name()))
		}
		if !entry.IsDir() {
			continue
		}
		child, openErr := baseRoot.OpenRoot(entry.Name())
		if openErr != nil {
			return nil, openErr
		}
		matches, scanErr := scanReceiptDirectory(child, filepath.Join(base, entry.Name()), projectDir, normalizedProfile, entry.Name(), messageID)
		child.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		found = append(found, matches...)
	}
	return found, nil
}

func scanReceiptDirectory(root *os.Root, dir, projectDir, profile, session, messageID string) ([]deliveryReceiptData, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, nil
	}
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	var found []deliveryReceiptData
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing symlink in delivery receipt scope: %s", filepath.Join(dir, entry.Name()))
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue // never descend; orphan named-profile roots cannot poison default lookup
		}
		path := filepath.Join(dir, entry.Name())
		r, err := readDeliveryReceiptAt(root, entry.Name(), path)
		if err != nil {
			return nil, err
		}
		if err := validateReceiptProvenance(r, projectDir, profile, session, path); err != nil {
			return nil, err
		}
		if r.MessageID != "" && r.MessageID == messageID {
			r.Path = path
			found = append(found, r)
		}
	}
	return found, nil
}

func validateReceiptProvenance(receipt deliveryReceiptData, projectDir, profile, session, path string) error {
	projectAbs, err := canonicalPathForReceipt(projectDir)
	if err != nil {
		return err
	}
	recordProject, err := canonicalPathForReceipt(receipt.Target.ProjectDir)
	if err != nil || filepath.Clean(recordProject) != filepath.Clean(projectAbs) {
		return fmt.Errorf("receipt_corrupt: project provenance %q does not match selected project %q", receipt.Target.ProjectDir, projectAbs)
	}
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	if squadnamespace.NormalizeProfile(receipt.Target.Profile) != profile || strings.TrimSpace(receipt.Target.Session) != session || receipt.Target.NamespaceID != squadnamespace.ID(profile, session) {
		return fmt.Errorf("receipt_corrupt: namespace provenance %s/%s (%s) does not match selected %s/%s", receipt.Target.Profile, receipt.Target.Session, receipt.Target.NamespaceID, profile, session)
	}
	expectedPath := filepath.Join(deliveryReceiptDir(projectAbs, profile, session), receipt.AttemptID+".json")
	actualPath, actualErr := canonicalPathForReceipt(path)
	recordedPath, recordedErr := canonicalPathForReceipt(receipt.Path)
	if actualErr != nil || recordedErr != nil || filepath.Clean(actualPath) != filepath.Clean(expectedPath) || filepath.Base(path) != receipt.AttemptID+".json" || filepath.Clean(recordedPath) != filepath.Clean(expectedPath) {
		return fmt.Errorf("receipt_corrupt: attempt filename/path does not match %s", expectedPath)
	}
	expectedRoot := squadnamespace.AMQRoot(projectAbs, profile, session)
	recordRoot, rootErr := canonicalPathForReceipt(receipt.Root)
	expectedCanonicalRoot, expectedRootErr := canonicalPathForReceipt(expectedRoot)
	if rootErr != nil || expectedRootErr != nil || filepath.Clean(recordRoot) != filepath.Clean(expectedCanonicalRoot) {
		return fmt.Errorf("receipt_corrupt: AMQ root %q does not match canonical root %q", receipt.Root, expectedRoot)
	}
	if receipt.Sender == "" || len(receipt.Recipients) == 0 || receipt.Thread == "" {
		return fmt.Errorf("receipt_corrupt: sender, recipients, and thread provenance are mandatory")
	}
	return nil
}

func canonicalPathForReceipt(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	probe := abs
	var suffix []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(probe)
		if evalErr == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Join(parts...), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", evalErr
		}
		suffix = append([]string{filepath.Base(probe)}, suffix...)
		probe = parent
	}
}

func refreshDeliveryReceipt(receipt *deliveryReceiptData, projectDir, profile, session string) error {
	if receipt == nil {
		return fmt.Errorf("receipt_corrupt: nil delivery receipt")
	}
	if err := validateDeliveryReceiptCrossFields(*receipt); err != nil {
		return err
	}
	if receipt.ReconciledMessageID != "" {
		return fmt.Errorf("reconciled existing delivery receipt is terminal and cannot be refreshed")
	}
	now := time.Now().UTC()
	receipt.LastCheckedAt = &now
	receipt.LastCheckError = ""
	var failures []string
	expectedRoot := squadnamespace.AMQRoot(projectDir, profile, session)
	for _, consumer := range receipt.Recipients {
		ctx := amqContext{ProjectDir: projectDir, Profile: profile, Root: expectedRoot, Me: consumer, Session: session, PinMode: amqPinExactRoot}
		cmd := []string{"receipts", "list", "--root", expectedRoot, "--me", consumer, "--msg-id", receipt.MessageID, "--json"}
		out, err := runAMQCommand(amqCommandRequest{Dir: projectDir, Env: amqCommandEnv(ctx), Arg: cmd})
		if err != nil {
			failures = append(failures, fmt.Sprintf("recipient %s lookup failed: %v", consumer, err))
			continue
		}
		var list nativeAMQReceiptList
		payload := firstJSONObject(out)
		if len(payload) == 0 || json.Unmarshal(payload, &list) != nil {
			failures = append(failures, fmt.Sprintf("recipient %s returned corrupt receipt JSON", consumer))
			continue
		}
		if len(list.Receipts) > 1 {
			failures = append(failures, fmt.Sprintf("recipient %s returned duplicate/conflicting native receipts", consumer))
			continue
		}
		for _, native := range list.Receipts {
			if native.MsgID != receipt.MessageID || native.Consumer != consumer || (native.Stage != "drained" && native.Stage != "dlq") {
				failures = append(failures, fmt.Sprintf("recipient %s returned conflicting receipt evidence", consumer))
				continue
			}
			if applyErr := applyNativeReceipt(receipt, native); applyErr != nil {
				failures = append(failures, applyErr.Error())
			}
		}
	}
	if len(failures) > 0 {
		receipt.LastCheckError = strings.Join(failures, "; ")
		return fmt.Errorf("%s", receipt.LastCheckError)
	}
	return nil
}
