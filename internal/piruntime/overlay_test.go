package piruntime

import (
	"strings"
	"testing"
)

// Review 2, item 1: NODE_* and JITI_* never reach pi from any overlay
// source, only from the forced set. One row per source: the caller's
// overlay (the CLI: identity, the solo lead's root and role), a seat's
// overlay (seat-exec's launch-record values, which arrive the same way),
// and the registry entry's declared needs (EnvNeeds keeps parent names).
func TestLoaderVarsFilteredFromEveryOverlay(t *testing.T) {
	e := fixture(t)
	rt, _, err := Ensure(tempHome(t), e)
	if err != nil {
		t.Fatal(err)
	}
	loader := map[string]string{
		"NODE_PATH": "/tmp/mods", "NODE_TLS_REJECT_UNAUTHORIZED": "0", "NODE_OPTIONS": "--require=/tmp/evil.cjs",
		"NODE_REPL_EXTERNAL_MODULE": "/tmp/repl.js", "JITI_FS_CACHE": "true", "JITI_ALIAS": "{}",
	}
	rows := map[string]Spec{
		"caller overlay": {Overlay: loader},
		"seat overlay":   {Overlay: map[string]string{"AM_ME": "pi-1", "NODE_PATH": "/tmp/seat-mods", "NODE_TLS_REJECT_UNAUTHORIZED": "0"}},
		"registry needs": {
			Parent:   []string{"NODE_PATH=/tmp/parent-mods", "NODE_TLS_REJECT_UNAUTHORIZED=0", "NODE_OPTIONS=--inspect"},
			EnvNeeds: []string{"NODE_PATH", "NODE_TLS_REJECT_UNAUTHORIZED", "NODE_OPTIONS"},
		},
	}
	for name, spec := range rows {
		t.Run(name, func(t *testing.T) {
			spec.Runtime, spec.Home, spec.Seat = rt, tempHome(t), "pi-1"
			l, err := Prepare(spec)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]string{}
			for _, kv := range l.Env {
				k, v, _ := strings.Cut(kv, "=")
				if _, dup := seen[k]; dup {
					t.Fatalf("%s twice", k)
				}
				seen[k] = v
			}
			for k, v := range seen {
				if !loaderVar(k) {
					continue
				}
				forced, isForced := Forced("", "", "", "")[k]
				if !isForced {
					t.Fatalf("%s=%q reached pi from the %s", k, v, name)
				}
				if v != forced {
					t.Fatalf("%s=%q, want the forced %q", k, v, forced)
				}
			}
			if v, ok := seen["NODE_OPTIONS"]; !ok || v != "" {
				t.Fatalf("NODE_OPTIONS=%q (present %t), want forced empty", v, ok)
			}
		})
	}
}
