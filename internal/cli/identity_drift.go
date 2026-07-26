package cli

import (
	"fmt"
	"strings"
)

// Identity-drift reporting.
//
// GitHub #540: the prepared-launch identity check tested four components and
// rendered one, so a mismatch on any of the other three produced
//
//	prepared launch record namespace drift: accepted=squad/v2-25-0 current=squad/v2-25-0
//
// an error whose two operands are byte-identical and which tells the operator
// nothing about what actually differed.
//
// The fix is not "remember to print more fields". It is that the only way to
// build one of these messages is to record fields through a constructor that
// refuses to record a field whose operands are equal, and which yields no error
// at all when nothing was recorded. A message showing `accepted=X current=X` is
// therefore unconstructible rather than merely untested.

// identityDriftField is one differing component of a compared identity.
type identityDriftField struct {
	Name     string
	Accepted string
	Current  string
}

// identityDrift accumulates the components that differ between an accepted
// identity and the current one. The zero value is ready to use.
type identityDrift struct {
	fields []identityDriftField
}

// add records a differing component. It is the ONLY way fields enter the
// report, and it drops any pair whose rendered operands are equal. That single
// guard is what makes an `accepted=X current=X` message impossible to build,
// independent of which predicate the caller compared with.
func (d *identityDrift) add(name, accepted, current string) {
	if accepted == current {
		return
	}
	d.fields = append(d.fields, identityDriftField{Name: name, Accepted: accepted, Current: current})
}

// compare records a drift when two components differ by exact string equality.
func (d *identityDrift) compare(name, accepted, current string) {
	d.add(name, accepted, current)
}

// comparePath records a drift when two components name different filesystem
// locations. Representation alone never counts as drift: "." and the absolute
// path it resolves to are the same location and are not reported.
func (d *identityDrift) comparePath(name, accepted, current string) {
	if sameFilesystemPath(accepted, current) {
		return
	}
	d.add(name, accepted, current)
}

// drifted reports whether any component differed.
func (d *identityDrift) drifted() bool { return len(d.fields) > 0 }

// err renders every differing component with both of its values, or returns nil
// when the identities match. subject describes what was compared and becomes
// the message prefix.
func (d *identityDrift) err(subject string) error {
	if !d.drifted() {
		return nil
	}
	parts := make([]string, 0, len(d.fields))
	for _, f := range d.fields {
		parts = append(parts, fmt.Sprintf("%s: accepted=%q current=%q", f.Name, f.Accepted, f.Current))
	}
	return fmt.Errorf("%s: %s", subject, strings.Join(parts, "; "))
}

// preparedLaunchIdentityDrift compares an agent's current launch identity
// against the accepted prepared manifest and returns nil when they agree.
//
// project is compared as a filesystem location rather than as a string, so a
// run prepared with `--project .` and bootstrapped from the resolved absolute
// path is correctly treated as the same project. Before #540 that comparison
// was the string one and it was what tripped this check while the message
// blamed the namespace.
func preparedLaunchIdentityDrift(manifest preparedRunManifest, project, profile, session string) error {
	var drift identityDrift
	drift.comparePath("project", manifest.Project, project)
	drift.compare("profile", manifest.Profile, profile)
	drift.compare("session", manifest.Session, session)
	drift.compare("namespace", manifest.Namespace, profile+"/"+session)
	return drift.err("prepared launch record identity drift")
}
