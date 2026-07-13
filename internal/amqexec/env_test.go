package amqexec

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestNoUpdateCheckEnvCanonicalizesWithoutMutatingInput(t *testing.T) {
	input := make([]string, 4, 8)
	copy(input, []string{"PATH=/bin", "AMQ_NO_UPDATE_CHECK=0", "HOME=/home/test", "AMQ_NO_UPDATE_CHECK"})
	before := append([]string(nil), input...)

	got := NoUpdateCheckEnv(input)
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("input mutated\n got: %#v\nwant: %#v", input, before)
	}
	want := []string{"PATH=/bin", "HOME=/home/test", "AMQ_NO_UPDATE_CHECK=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment\n got: %#v\nwant: %#v", got, want)
	}
	if input[:cap(input)][len(input)] != "" {
		t.Fatalf("input backing array was mutated: %#v", input[:cap(input)])
	}
}

func TestNoUpdateCheckEnvNilInheritsWithoutMutatingParent(t *testing.T) {
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	t.Setenv("AMQ_CHILD_ENV_SENTINEL", "present")

	got := NoUpdateCheckEnv(nil)
	if valueForKey(got, "AMQ_CHILD_ENV_SENTINEL") != "present" {
		t.Fatalf("nil environment did not inherit parent: %#v", got)
	}
	if countKey(got, "AMQ_NO_UPDATE_CHECK") != 1 || valueForKey(got, "AMQ_NO_UPDATE_CHECK") != "1" {
		t.Fatalf("suppression variable is not canonical: %#v", got)
	}
	if got := os.Getenv("AMQ_NO_UPDATE_CHECK"); got != "0" {
		t.Fatalf("parent environment mutated to %q", got)
	}
}

func TestNoUpdateCheckEnvPreservesNonNilEmptyEnvironment(t *testing.T) {
	got := NoUpdateCheckEnv([]string{})
	want := []string{"AMQ_NO_UPDATE_CHECK=1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment\n got: %#v\nwant: %#v", got, want)
	}
}

func countKey(env []string, want string) int {
	count := 0
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == want || entry == want {
			count++
		}
	}
	return count
}

func valueForKey(env []string, want string) string {
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == want {
			return value
		}
	}
	return ""
}
