// Package gatetarget is the one place for typed-gate target strings and the
// target digest (2a-3 open question 9). guard builds a target from the call it
// sees, the gate store writer hashes the same string into the gate record,
// and the verifier compares the owner's typed prefix against that record: all
// three call this package, so a target string means one thing everywhere.
//
// Conventions, one line each, by operatorauth action:
//
//	tag              git tag <name> | git push refs/tags/<name> | git push --tags
//	                 | git tag <name> <commit> on <owner/repo> (the release flow, 3-2)
//	github_release   gh release create|upload|edit|delete <tag>
//	                 | gh release create <tag> on <owner/repo> (the release flow, 3-2)
//	package_publish  npm publish [<spec>] (and pnpm, yarn, cargo, gem push, twine upload, goreleaser release)
//	external_send    <mcp tool> <recipient> | gws <service> <verb> | mail|mailx|sendmail|msmtp [<recipient>]
//	credential_change  gh auth login ... | git config <credential key> | write <absolute path> | ssh-keygen ...
//	quarantine_clear clear <handle> nonce=<hex> incarnation=<task>@<unix nanos> config=<sha256:hex> (the Buzz-signed seat clear, phase 5)
//	spawn            grow <session root> seats=<N> | grow <session root> <limit>=<N>
//	                 | grow merge <owner/repo>#<pr> head=<sha> rules=<code,...> (a merge outside the grant, 3-2)
//
// The digest is the S1 non-merge formula: sha256 over the length-prefixed
// domain "amq-squad-gate-target-v1", the action, and the hex sha256 of the
// target string.
package gatetarget

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Domain is the digest domain string.
const Domain = "amq-squad-gate-target-v1"

// Typed classes of plan 7.1 / R30, as the owner reads them in the gate text.
const (
	ClassRelease    = "release"
	ClassSend       = "send"
	ClassCredential = "credential"
	ClassGrow       = "grow"
	// ClassClear is the fifth typed class (phase 5): the owner's signed
	// answer that lifts a seat's config-change quarantine.
	ClassClear = "clear"
)

// classOf maps each typed-gate action to its plan class. Merge is not a typed
// gate (plan 7.1).
var classOf = map[string]string{
	"tag": ClassRelease, "github_release": ClassRelease, "package_publish": ClassRelease,
	"external_send":     ClassSend,
	"credential_change": ClassCredential,
	"spawn":             ClassGrow,
	"quarantine_clear":  ClassClear,
}

// Class returns the plan class of a typed-gate action, "" for any other.
func Class(action string) string { return classOf[action] }

// Actions lists the typed-gate actions.
func Actions() []string {
	return []string{"credential_change", "external_send", "github_release", "package_publish", "quarantine_clear", "spawn", "tag"}
}

// Digest is the target digest of an action and its target string.
func Digest(action, target string) string {
	subject := sha256.Sum256([]byte(target))
	var b []byte
	for _, v := range []string{Domain, action, hex.EncodeToString(subject[:])} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
		b = append(b, v...)
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Validate checks that an action is a typed-gate action of the operatorauth
// taxonomy and the target is one canonical line.
func Validate(action, target string) error {
	if Class(action) == "" {
		return fmt.Errorf("action %q is not a typed-gate action (%s)", action, strings.Join(Actions(), ", "))
	}
	if _, err := operatorauth.LookupAction(action); err != nil {
		return err
	}
	return operatorauth.ValidateCanonicalSingleLineField("target", target, true)
}

// Commands maps a command prefix (command plus leading positionals) to the
// typed-gate action it performs; the target is the prefix plus the next
// positional (Command).
var Commands = map[string]string{
	"git tag": "tag", "gh release create": "github_release", "gh release upload": "github_release", "gh release edit": "github_release", "gh release delete": "github_release",
	"npm publish": "package_publish", "pnpm publish": "package_publish", "yarn publish": "package_publish", "cargo publish": "package_publish", "gem push": "package_publish", "twine upload": "package_publish", "goreleaser release": "package_publish",
	"mail": "external_send", "mailx": "external_send", "sendmail": "external_send", "msmtp": "external_send",
	"gh auth login": "credential_change", "gh auth logout": "credential_change", "gh auth refresh": "credential_change", "gh auth setup-git": "credential_change", "gh auth switch": "credential_change",
	"gh secret set": "credential_change", "gh secret delete": "credential_change", "gh ssh-key add": "credential_change", "gh ssh-key delete": "credential_change", "gh gpg-key add": "credential_change",
	"npm login": "credential_change", "npm adduser": "credential_change", "npm token": "credential_change", "docker login": "credential_change", "aws configure": "credential_change", "gcloud auth": "credential_change", "az login": "credential_change",
	"ssh-keygen": "credential_change", "ssh-add": "credential_change", "ssh-copy-id": "credential_change", "git credential approve": "credential_change", "git credential store": "credential_change",
	"security add-generic-password": "credential_change", "security add-internet-password": "credential_change", "security delete-generic-password": "credential_change", "security delete-internet-password": "credential_change",
}

// Command is the target of a Commands entry: the prefix and the next
// positional, if any ("git tag v1", "gh auth login", "npm publish").
func Command(prefix, next string) string { return strings.TrimSpace(prefix + " " + next) }

// TagPush is the target of a tag push: "git push refs/tags/<name>", or
// "git push --tags" for ref "--tags".
func TagPush(ref string) string { return "git push " + ref }

// MCPSend is the target of an MCP send tool call.
func MCPSend(tool, recipient string) string { return tool + " " + recipient }

// GWSSend is the target of a gws send, reply or forward.
func GWSSend(service, verb string) string { return "gws " + service + " " + verb }

// GitConfig is the target of a credential git config key.
func GitConfig(key string) string { return "git config " + key }

// Write is the target of a write into a credential location (an absolute
// path).
func Write(path string) string { return "write " + path }

// Grow is the target of the growth gate: seat number seats in a session.
func Grow(root string, seats int) string { return GrowLimit(root, "seats", seats) }

// GrowLimit is the target of the growth gate past any grant limit:
// "grow <root> <limit>=<value>" (seats, files, ...).
func GrowLimit(root, limit string, value int) string {
	return fmt.Sprintf("grow %s %s=%d", root, limit, value)
}

// MergeBeyond is the target of the gate a merge-train refusal raises (plan
// 7.2: a merge outside the grant is refused by the core and escalates to an
// owner-signed gate, the growth class). It binds the repository, the PR, its
// head SHA and the sorted refused rules, so an approval opens exactly this
// head past exactly these rules; a new head or another rule is a new gate.
func MergeBeyond(repository string, pr int, head string, rules []string) string {
	return fmt.Sprintf("grow merge %s#%d head=%s rules=%s", repository, pr, head, strings.Join(rules, ","))
}

// ReleaseTag is the release flow's tag target: the tag, the exact commit it
// names and the repository, so an approval tags exactly that commit there.
func ReleaseTag(repository, tag, commit string) string {
	return "git tag " + tag + " " + commit + " on " + repository
}

// ReleaseCreate is the release flow's github_release target.
func ReleaseCreate(repository, tag string) string {
	return "gh release create " + tag + " on " + repository
}

// GrowTask is the target of a growth gate for one task's widening past a
// grant limit (2b-2 milestone B): "grow <root> task=<id> <limit>=<value>",
// where value is a one-line JSON value (protected_paths: the widened globs
// that cross the grant's protected paths; size_cap: {"lines":N,"files":M}).
func GrowTask(root, taskID, limit, value string) string {
	return fmt.Sprintf("grow %s task=%s %s=%s", root, taskID, limit, value)
}

// QuarantineClear is the target of a seat's clear request: the handle, the
// request nonce, the seat incarnation (the quarantined task and the
// quarantine's time) and the digest of the quarantine's config diff, so an
// approval lifts exactly this quarantine of exactly this seat, once.
func QuarantineClear(handle, nonce, incarnation, configDigest string) string {
	return fmt.Sprintf("clear %s nonce=%s incarnation=%s config=%s", handle, nonce, incarnation, configDigest)
}

// ParseQuarantineClear splits a QuarantineClear target.
func ParseQuarantineClear(target string) (handle, nonce, incarnation, configDigest string, ok bool) {
	f := strings.Fields(target)
	if len(f) != 5 || f[0] != "clear" {
		return "", "", "", "", false
	}
	var got [3]string
	for i, key := range []string{"nonce=", "incarnation=", "config="} {
		v, found := strings.CutPrefix(f[2+i], key)
		if !found || v == "" {
			return "", "", "", "", false
		}
		got[i] = v
	}
	return f[1], got[0], got[1], got[2], QuarantineClear(f[1], got[0], got[1], got[2]) == target
}
