package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/role"
	"github.com/omriariav/amq-squad/internal/rules"
	"github.com/omriariav/amq-squad/internal/team"
)

//go:embed bootstrap.md
var defaultBootstrapTemplate string

type bootstrapContext struct {
	Role          string
	Handle        string
	Binary        string
	Session       string
	CWD           string
	Root          string
	AgentDir      string
	TeamHome      string
	TeamRulesPath string
	RolePath      string
	LaunchPath    string
	CurrentTeam   []bootstrapTeamMember
}

type bootstrapTeamMember struct {
	Role    string
	Handle  string
	Binary  string
	Session string
	Project string
	CWD     string
	Route   string
	You     bool
}

func buildBootstrapPrompt(ctx bootstrapContext) (string, error) {
	tpl, err := template.New("bootstrap").Funcs(template.FuncMap{
		"orDefault": func(s, fallback string) string {
			if s == "" {
				return fallback
			}
			return s
		},
	}).Parse(defaultBootstrapTemplate)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap template: %w", err)
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("render bootstrap template: %w", err)
	}
	return b.String(), nil
}

func bootstrapContextFor(rec launch.Record, agentDir, teamHome string) bootstrapContext {
	teamRulesPath := ""
	if teamHome != "" {
		teamRulesPath = rules.Path(teamHome)
	} else if _, err := os.Stat(rules.Path(rec.CWD)); err == nil {
		teamRulesPath = rules.Path(rec.CWD)
	}
	return bootstrapContext{
		Role:          rec.Role,
		Handle:        rec.Handle,
		Binary:        rec.Binary,
		Session:       rec.Session,
		CWD:           rec.CWD,
		Root:          rec.Root,
		AgentDir:      agentDir,
		TeamHome:      teamHome,
		TeamRulesPath: teamRulesPath,
		RolePath:      role.Path(agentDir),
		LaunchPath:    filepath.Join(agentDir, launch.FileName),
		CurrentTeam:   bootstrapCurrentTeam(rec, teamHome),
	}
}

func bootstrapCurrentTeam(rec launch.Record, teamHome string) []bootstrapTeamMember {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.Read(home)
	if err != nil || len(t.Members) == 0 {
		return nil
	}

	currentProject := projectNameForCWD(rec.CWD)
	out := make([]bootstrapTeamMember, 0, len(t.Members))
	for _, m := range t.Members {
		cwd := m.EffectiveCWD(t.Project)
		project := projectNameForCWD(cwd)
		handle := memberHandle(m)
		out = append(out, bootstrapTeamMember{
			Role:    m.Role,
			Handle:  handle,
			Binary:  m.Binary,
			Session: m.Session,
			Project: project,
			CWD:     cwd,
			Route:   routeCommandFor(currentProject, project, handle, m.Session),
			You:     sameLaunchTarget(rec, cwd, handle, m),
		})
	}
	return out
}

func memberHandle(m team.Member) string {
	if m.Handle != "" {
		return m.Handle
	}
	if m.Role != "" {
		return m.Role
	}
	return m.Binary
}

func sameLaunchTarget(rec launch.Record, cwd, handle string, m team.Member) bool {
	return rec.Role == m.Role &&
		rec.Handle == handle &&
		rec.Session == m.Session &&
		rec.CWD == cwd
}

func routeCommandFor(currentProject, targetProject, handle, session string) string {
	args := []string{"amq", "send", "--to", handle}
	if currentProject != "" && targetProject != "" && currentProject != targetProject {
		args = append(args, "--project", targetProject)
	}
	if session != "" {
		args = append(args, "--session", session)
	}
	for i, arg := range args {
		args[i] = shellQuote(arg)
	}
	return strings.Join(args, " ")
}

func projectNameForCWD(cwd string) string {
	if cwd == "" {
		return ""
	}
	if dir, name := findProjectName(cwd); name != "" {
		return name
	} else if dir != "" {
		return filepath.Base(dir)
	}
	return filepath.Base(cwd)
}

func findProjectName(start string) (string, string) {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	for {
		path := filepath.Join(dir, ".amqrc")
		if b, err := os.ReadFile(path); err == nil {
			var cfg struct {
				Project string `json:"project"`
			}
			if json.Unmarshal(b, &cfg) == nil && cfg.Project != "" {
				return dir, cfg.Project
			}
			return dir, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}
