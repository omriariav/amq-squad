package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
)

// runTrust is `amq-squad trust <repo>` (amendment 1, R4): it prints the
// owner-run steps to trust a repository in codex-cli and the candidate
// notify fan-out line (plan 16.8, 2b-3 ruling 2). It writes nothing:
// codex-cli owns config.toml (amendment 17 (d)).
func runTrust(args []string) error {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad trust - print the owner-run steps to trust a repository in codex-cli

Usage:
  amq-squad trust <repo>

A fresh codex-cli seat stops on its folder-trust prompt, and amq-squad never
answers it or writes ~/.codex/config.toml for you. This prints the lines to add
yourself, plus the candidate Codex notify fan-out line. It writes nothing.

Examples:
  amq-squad trust .
  amq-squad trust ~/Code/app
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErrorf("trust takes exactly one repository path; got %d", fs.NArg())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return executeTrust(os.Stdout, home, os.Getenv("CODEX_HOME"), fs.Arg(0))
}

func executeTrust(out io.Writer, home, codexHome, repoArg string) error {
	dir, err := filepath.Abs(repoArg)
	if err != nil {
		return err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("trust: %s is not a directory", dir)
	}
	repo := canonicalFilesystemPath(dir)
	configPath := hostcheck.CodexConfigPath(home, codexHome)
	fanout := filepath.Join(home, ".amq-squad", "bin", hostcheck.NotifyFanoutName)
	ct, err := hostcheck.ReadCodexTrust(configPath)
	if err != nil {
		return fmt.Errorf("trust: read %s: %w", configPath, err)
	}
	if len(ct.UntrustedRoots([]string{repo})) == 0 {
		fmt.Fprintf(out, "%s is already trusted in %s.\n\n", repo, configPath)
	}
	_, err = io.WriteString(out, hostcheck.TrustSteps(repo, configPath, fanout))
	return err
}
