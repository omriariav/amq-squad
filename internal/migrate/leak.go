package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The output funnel's helpers (M1). The verb collects everything it would
// print or return and passes it once through UnsafeText; if any of it would
// change under redact.Block it prints a fixed line naming only a location
// class from the closed words below, and exits 2.

// Location classes of a leak: closed words, never text from a file or a path.
const (
	ClassProfilePath = "profile path"
	ClassProjectPath = "project path"
	ClassProfileKey  = "a key under the profile"
	ClassProfileVal  = "a value under the profile"
	ClassLaunchKey   = "a key under launch.local.json"
	ClassInputName   = "an input file name"
	ClassRemovalPath = "a removal path"
	ClassOutput      = "the output"
)

// UnsafeText reports text the verb must not print: a real credential shape,
// or anything redact.Block would change.
func UnsafeText(s string) bool { return unsafeText(s) }

// LeakLine is the one line the verb prints when its output was unsafe.
func LeakLine(class string) string { return CodeCredential + ": " + class }

// LeakClass names where a credential-shaped string sits, as a closed word. It
// is best effort and read-only: the profile argument, then the keys and
// values of the profile and of launch.local.json, then the file names below
// .amq-squad and .agent-mail; ClassOutput when none is found.
func LeakClass(profile string, remove bool) string {
	abs, err := filepath.Abs(profile)
	if err != nil {
		return ClassOutput
	}
	if looksLikeCredential(abs) {
		if i := strings.Index(abs, "/"+dirName+"/"); i >= 0 && looksLikeCredential(abs[:i]) {
			return ClassProjectPath
		}
		return ClassProfilePath
	}
	project, _, profileRel, err := locate(abs)
	if err != nil {
		return ClassOutput
	}
	r, err := openRoot(project)
	if err != nil {
		var rf *Refusal
		if errors.As(err, &rf) && rf.Code == CodeCredential {
			return ClassProjectPath
		}
		return ClassOutput
	}
	defer r.Close()
	if f, _ := r.read(profileRel); f != nil {
		var v any
		if json.Unmarshal(f.Data, &v) == nil {
			if len(credentialKeyFields("", v)) > 0 {
				return ClassProfileKey
			}
			if len(credentialFields("", v)) > 0 {
				return ClassProfileVal
			}
		}
	}
	if f, _ := r.read(dirName + "/" + launchLocalFile); f != nil {
		var v any
		if json.Unmarshal(f.Data, &v) == nil && len(credentialKeyFields("", v)) > 0 {
			return ClassLaunchKey
		}
	}
	name := ClassInputName
	if remove {
		name = ClassRemovalPath
	}
	var found bool
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		if found || depth > 6 {
			return
		}
		names, _ := r.names(rel)
		for _, n := range names {
			if looksLikeCredential(n) {
				found = true
				return
			}
			walk(rel+"/"+n, depth+1)
		}
	}
	walk(dirName, 0)
	walk(agentMailDir, 0)
	if found {
		return name
	}
	return ClassOutput
}

// PartialReport is the report of an apply that stopped at its first refusal
// (M2): what was done, what was planned, and the refusal. There is no
// transaction, so the files named were written or removed and stay so.
func PartialReport(verb string, done []string, total int, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "amq-squad migrate: %s stopped at the first refusal after %d of %d; the files below were done and stay so.\n", verb, len(done), total)
	for _, f := range done {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprintf(&b, "refusal: %v\n", err)
	return b.String()
}
