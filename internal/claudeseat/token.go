package claudeseat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// The Claude seats' credential (ruling 2026-10-24, option 1, with the
// reviewer's conditions). A fresh CLAUDE_CONFIG_DIR finds no login (the
// macOS Keychain item is keyed by the config dir; measured on Claude Code
// 2.1.310: "Not logged in"), so the owner runs `claude setup-token` once and
// writes the token to ~/.amq-squad/claude-token. seat-exec reads it from that
// guarded file only, never from the parent environment, and sets
// CLAUDE_CODE_OAUTH_TOKEN for Claude seats only. A missing or insecure file
// refuses the seat (claude_token_insecure); there is no fallback to the
// owner's config dir. The file is in grants.Implicit, so guard denies a seat
// that writes it, and guard denies a seat call that names it.
//
// Residual: the token is in the seat process environment, readable by the
// seat's shell tool and its children, the same reach a same-UID process had
// to the Keychain credential before, bounded to the owner's subscription
// (plan 7.3).

// TokenInsecure is the refusal of a Claude seat without a usable token file.
const TokenInsecure = "claude_token_insecure"

// TokenVar is the variable Claude Code reads the token from; the Claude
// runtime's one credential need.
const TokenVar = "CLAUDE_CODE_OAUTH_TOKEN"

// CredentialNeeds are the credential variables the Claude runtime declares;
// their values come only from the owner's token file.
var CredentialNeeds = []string{TokenVar}

// CredentialSource is what the launch record notes (never the value).
const CredentialSource = "owner token file"

// TokenRotation is the age past which doctor warns (a proposal).
const TokenRotation = 30 * 24 * time.Hour

// Reviewer condition 3 (a Bash-tool credential scrub, if Claude Code has
// one) is withdrawn for Claude Code 2.1.310 (phase 5 re-pass of 4-2, P1-2):
// its candidate, CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1, forces the permission
// mode to default (measured: the stream-json init reports
// permissionMode "default" and warns "Permission mode forced to default"),
// which defeats dontAsk. The seat's settings.json carries no env block, and
// the token's visibility to the seat's Bash children is the recorded limit
// of R33. The launch probes the effective permission mode of the installed
// Claude Code against the seat's settings (ProbeMode).

// TokenPath is ~/.amq-squad/claude-token.
func TokenPath(home string) string { return filepath.Join(home, ".amq-squad", "claude-token") }

// ReadToken reads the owner's token file: a regular file (not a symlink),
// owned by the user, readable by nobody else (no group or world bits), in an
// owner-only directory, holding one token on one line. It returns the token
// or the refusal detail.
func ReadToken(home string) (string, string) {
	p := TokenPath(home)
	st, err := os.Lstat(p)
	if err != nil {
		return "", fmt.Sprintf("%s: %v (the owner runs `claude setup-token` and writes the token there, mode 0600)", p, err)
	}
	if !st.Mode().IsRegular() {
		return "", p + " is not a regular file"
	}
	if why := grants.Insecure(p); why != "" {
		return "", why
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Sprintf("%s is readable or writable by others (mode %04o); it must be 0600", p, st.Mode().Perm())
	}
	if dst, err := os.Lstat(filepath.Dir(p)); err != nil || dst.Mode().Perm()&0o077 != 0 {
		return "", fmt.Sprintf("%s must be an owner-only directory (0700)", filepath.Dir(p))
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err.Error()
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") || len(tok) > 4096 {
		return "", p + " does not hold one token on one line"
	}
	return tok, ""
}

// TokenStatus is doctor's view of the token file: "" when it is usable and
// younger than the rotation window, else a warning (never the value).
func TokenStatus(home string, now time.Time) (ok bool, detail string) {
	if _, why := ReadToken(home); why != "" {
		return false, "Claude seats are refused (" + TokenInsecure + "): " + why
	}
	st, _ := os.Lstat(TokenPath(home))
	if age := now.Sub(st.ModTime()); age > TokenRotation {
		return false, fmt.Sprintf("%s is %d days old, past the %d-day rotation window: run claude setup-token again", TokenPath(home), int(age.Hours()/24), int(TokenRotation.Hours()/24))
	}
	return true, fmt.Sprintf("%s present, 0600, owner-only, %d days old", TokenPath(home), int(now.Sub(st.ModTime()).Hours()/24))
}
