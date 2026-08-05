package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
)

// realAMQManagedContextPoison mirrors the context exported by a live managed
// agent and includes private wake state observed in AMQ v0.51.1. The AMQ_WAKE_
// sentinel makes the regression cover future wake-owned descriptors too.
var realAMQManagedContextPoison = []string{
	"AM_ROOT=/hostile/live/root",
	"AM_ROOT_ID=v1:hostile:root",
	"AM_BASE_ROOT=/hostile/live/base",
	"AM_BASE_ROOT_ID=v1:hostile:base",
	"AM_ME=hostile-live-agent",
	"AM_SESSION=hostile-live-session",
	"AMQ_GLOBAL_ROOT=/hostile/global/root",
	"AMQ_WAKE_OWNER=hostile-live-owner-token",
	"AMQ_WAKE_ATTENTION_FD=997",
	"AMQ_WAKE_PRIVATE_STOP_FD=998",
	"AMQ_WAKE_FUTURE_PRIVATE_STATE=hostile",
}

func isolateRealAMQManagedContext(t *testing.T) {
	t.Helper()
	for _, entry := range realAMQManagedContextPoison {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("invalid real-AMQ managed context poison %q", entry)
		}
		t.Setenv(key, value)
	}

	current := os.Environ()
	kept := make(map[string]bool)
	for _, entry := range envWithoutAMQIdentity(current) {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			kept[key] = true
		}
	}
	for _, entry := range current {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if kept[key] {
			continue
		}
		t.Cleanup(func() {
			if err := os.Setenv(key, value); err != nil {
				t.Errorf("restore sanitized real-AMQ managed context %s: %v", key, err)
			}
		})
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset sanitized real-AMQ managed context %s: %v", key, err)
		}
	}
}

func TestRealAMQFixtureEnvSanitizesManagedContext(t *testing.T) {
	clean := []string{
		"PATH=/fixture/bin",
		"AMQ_NO_UPDATE_CHECK=0",
		"AMQ_SQUAD_REAL_AMQ=/fixture/amq",
		"AMQ_SQUAD_REAL_AMQ_VERSION=v0.52.2",
	}
	poisoned := append(append([]string(nil), clean...), realAMQManagedContextPoison...)
	want := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(clean))
	got := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(poisoned))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("poisoned fixture env = %#v, want clean env %#v", got, want)
	}
}

func TestRealAMQProcessEnvSanitizesManagedContext(t *testing.T) {
	isolateRealAMQManagedContext(t)
	for _, entry := range realAMQManagedContextPoison {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := os.LookupEnv(key); ok {
			t.Fatalf("process fixture env retained %s", key)
		}
	}
}
