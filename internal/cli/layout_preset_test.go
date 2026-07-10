package cli

import (
	"strings"
	"testing"
)

func TestRunStartLayoutPresetMappings(t *testing.T) {
	for _, tc := range []struct {
		preset, visibility, target, spawn, final, option string
		leadMain                                         bool
	}{
		{layoutPresetLeadLeft, visibilityCurrent, "current-window", "vertical", "main-vertical", "main-pane-width", true},
		{layoutPresetLeadTop, visibilityCurrent, "current-window", "horizontal", "main-horizontal", "main-pane-height", true},
		{layoutPresetEvenGrid, visibilityCurrent, "current-window", "tiled", "tiled", "", false},
		{layoutPresetOneWindowPerAgent, visibilitySiblingTabs, "new-window", "tiled", "", "", false},
	} {
		t.Run(tc.preset, func(t *testing.T) {
			got, err := resolveRunStartLayout(runStartLayoutInput{Preset: tc.preset, PresetSet: true, Visibility: visibilitySiblingTabs})
			if err != nil {
				t.Fatal(err)
			}
			if got.Visibility != tc.visibility || got.Target != tc.target || got.SpawnLayout != tc.spawn || got.FinalLayout != tc.final || got.MainPaneOption != tc.option || got.LeadMain != tc.leadMain || got.LauncherPane != launcherPaneCloseAfterStart {
				t.Fatalf("selection = %+v", got)
			}
		})
	}
}

func TestRunStartLayoutContradictionAndDefaultMatrix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input runStartLayoutInput
		want  string
		err   string
	}{
		{name: "external keeps launcher", input: runStartLayoutInput{Preset: layoutPresetLeadLeft, PresetSet: true, ExternalLead: true}, want: launcherPaneKeep},
		{name: "detached keeps launcher", input: runStartLayoutInput{Visibility: visibilityDetached}, want: launcherPaneKeep},
		{name: "legacy remains opt in", input: runStartLayoutInput{Visibility: visibilitySiblingTabs}, want: ""},
		{name: "preset visibility conflict", input: runStartLayoutInput{Visibility: visibilityDetached, VisibilitySet: true, Preset: layoutPresetLeadLeft, PresetSet: true}, err: "requires --visibility current"},
		{name: "external close conflict", input: runStartLayoutInput{Visibility: visibilityCurrent, LauncherPane: launcherPaneCloseAfterStart, LauncherPaneSet: true, ExternalLead: true}, err: "forces --launcher-pane keep"},
		{name: "detached close conflict", input: runStartLayoutInput{Visibility: visibilityDetached, LauncherPane: launcherPaneCloseAfterStart, LauncherPaneSet: true}, err: "forces --launcher-pane keep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRunStartLayout(tc.input)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.LauncherPane != tc.want {
				t.Fatalf("launcher = %q, want %q", got.LauncherPane, tc.want)
			}
		})
	}
}
