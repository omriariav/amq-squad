package claudeseat

import (
	"os"
	"path/filepath"
	"testing"
)

// The reviewer's key variants: keys NFC does not fold. A key differing only
// in case, or with leading or trailing whitespace, beside a complete seed is
// not the launch's key: Check refuses it as claude_config_unexpected
// (.claude.json is read into maps, so keys match exactly). A variant in
// place of the real key leaves the real one missing and is refused by the
// fact it fails to give. In settings.json, exact keys too: "Theme" is not
// the theme, "EnabledPlugins" does not enable the plugin.
func TestKeyVariantsAreNotTheLaunchsKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		file string
		edit func(m map[string]any)
		code string
	}{
		"HasCompletedOnboarding beside the real key":  {".claude.json", func(m map[string]any) { m["HasCompletedOnboarding"] = true }, ConfigUnexpected},
		"hasCompletedOnboarding with a leading space": {".claude.json", func(m map[string]any) { m[" hasCompletedOnboarding"] = true }, ConfigUnexpected},
		"a project key differing in case": {".claude.json", func(m map[string]any) {
			project(m, "/WT/c1")["hasTrustDialogAccepted"] = true
		}, ConfigUnexpected},
		"a project key with a trailing space": {".claude.json", func(m map[string]any) {
			project(m, "/wt/c1 ")["hasTrustDialogAccepted"] = true
		}, ConfigUnexpected},
		"an entry key differing in case": {".claude.json", func(m map[string]any) {
			project(m, "/wt/c1")["HasTrustDialogAccepted"] = true
		}, ConfigUnexpected},
		"an entry key with a trailing space": {".claude.json", func(m map[string]any) {
			project(m, "/wt/c1")["hasTrustDialogAccepted "] = true
		}, ConfigUnexpected},
		"the variant in place of the real key": {".claude.json", func(m map[string]any) {
			delete(m, "hasCompletedOnboarding")
			m["HasCompletedOnboarding"] = true
		}, OnboardingMissing},
		"Theme in settings.json": {"settings.json", func(m map[string]any) {
			m["Theme"] = m["theme"]
			delete(m, "theme")
		}, OnboardingMissing},
		"EnabledPlugins in settings.json": {"settings.json", func(m map[string]any) {
			m["EnabledPlugins"] = m["enabledPlugins"]
			delete(m, "enabledPlugins")
		}, HooksMissing},
	} {
		t.Run(name, func(t *testing.T) {
			dir := Dir(t.TempDir(), "c1", "l")
			if _, err := Write(dir, "dontAsk", "/wt/c1", t0); err != nil {
				t.Fatal(err)
			}
			editJSON(t, filepath.Join(dir, tc.file), tc.edit)
			digest, _ := Digest(dir) // a launch that wrote the variant
			if code, detail := Check(dir, os.Getuid(), digest, "dontAsk"); code != tc.code {
				t.Fatalf("want %s, got %q %s", tc.code, code, detail)
			}
		})
	}
}
