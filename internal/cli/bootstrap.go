package cli

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/role"
	"github.com/omriariav/amq-squad/internal/rules"
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
	}
}
