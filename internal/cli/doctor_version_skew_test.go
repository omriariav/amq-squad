package cli

import (
	"strings"
	"testing"
)

func TestDoctorCheckVersionSkew(t *testing.T) {
	check := func(running, path, ver string, found bool) doctorCheck {
		return doctorCheckVersionSkew(doctorExecution{
			RunningVersion:    running,
			PathBinaryVersion: func() (string, string, bool) { return path, ver, found },
		})
	}
	cases := []struct {
		name         string
		got          doctorCheck
		wantStatus   doctorStatus
		wantInDetail string
	}{
		{"dev build is skipped without resolving PATH", check("dev", "/x", "v1.9.1", true), doctorOK, "dev"},
		{"empty running version is skipped", check("", "/x", "v1.9.1", true), doctorOK, "dev"},
		{"not on PATH warns", check("v2.0.0", "", "", false), doctorWarn, "not on PATH"},
		{"matching version is ok", check("v2.0.0", "/usr/local/bin/amq-squad", "v2.0.0", true), doctorOK, "matches this build"},
		{"version skew warns with both versions", check("v2.0.0", "/Users/x/go/bin/amq-squad", "v1.9.1", true), doctorWarn, "version skew"},
		{"unreadable PATH version warns", check("v2.0.0", "/x", "", true), doctorWarn, "could not read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q\ndetail: %s", tc.got.Status, tc.wantStatus, tc.got.Detail)
			}
			if !strings.Contains(tc.got.Detail, tc.wantInDetail) {
				t.Errorf("detail %q missing %q", tc.got.Detail, tc.wantInDetail)
			}
		})
	}

	// The skew case must name BOTH versions so the operator can see the mismatch.
	skew := check("v2.0.0", "/old/amq-squad", "v1.9.1", true)
	if !strings.Contains(skew.Detail, "v1.9.1") || !strings.Contains(skew.Detail, "v2.0.0") {
		t.Errorf("skew detail must name both versions, got %q", skew.Detail)
	}

	// A dev build must NOT shell out (PathBinaryVersion not consulted).
	called := false
	doctorCheckVersionSkew(doctorExecution{
		RunningVersion:    "",
		PathBinaryVersion: func() (string, string, bool) { called = true; return "", "", false },
	})
	if called {
		t.Error("dev build must not resolve the PATH binary (no shell-out)")
	}
}

func TestParseAmqSquadVersion(t *testing.T) {
	for in, want := range map[string]string{
		"amq-squad v2.0.0\n": "v2.0.0",
		"amq-squad v1.9.1":   "v1.9.1",
		"v2.0.0":             "v2.0.0",
		"  amq-squad  dev  ": "dev",
	} {
		if got := parseAmqSquadVersion(in); got != want {
			t.Errorf("parseAmqSquadVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
