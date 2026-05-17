package cli

import (
	"flag"
	"fmt"
)

// previewFlags is the shared flag surface for printing a team's launch-command
// plan. Both `team show` and `up --dry-run` register it so the two preview
// entry points cannot drift.
type previewFlags struct {
	noBootstrap    *bool
	session        *string
	fresh          *bool
	trustRaw       *string
	model          *string
	codexArgsRaw   *string
	claudeArgsRaw  *string
	forceDuplicate *bool
}

func registerPreviewFlags(fs *flag.FlagSet) *previewFlags {
	return &previewFlags{
		noBootstrap:    fs.Bool("no-bootstrap", false, "emit launch commands that skip the generated bootstrap prompt"),
		session:        fs.String("session", "", "AMQ workstream session name (default: sanitized team-home directory name; lowercase a-z, 0-9, -, _)"),
		fresh:          fs.Bool("fresh", false, "fail if the selected workstream session already exists"),
		trustRaw:       fs.String("trust", "", "Codex trust profile for this run: sandboxed or trusted"),
		model:          fs.String("model", "", "per-persona model overrides for this run, e.g. cto=gpt-5,fullstack=sonnet"),
		codexArgsRaw:   fs.String("codex-args", "", "extra Codex args for this run, e.g. '--enable goals'"),
		claudeArgsRaw:  fs.String("claude-args", "", "extra Claude args for this run, e.g. '--chrome'"),
		forceDuplicate: fs.Bool("force-duplicate", false, "include --force-duplicate in emitted launch commands"),
	}
}

func (p *previewFlags) toEmitOptions(fs *flag.FlagSet) (emitTeamOptions, error) {
	trustMode, err := normalizeTrustMode(*p.trustRaw)
	if err != nil {
		return emitTeamOptions{}, err
	}
	modelOverrides, err := parseKV(*p.model)
	if err != nil {
		return emitTeamOptions{}, fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	binaryArgs, err := parseBinaryArgFlags(*p.codexArgsRaw, *p.claudeArgsRaw)
	if err != nil {
		return emitTeamOptions{}, err
	}
	return emitTeamOptions{
		NoBootstrap:      *p.noBootstrap,
		RequestedSession: *p.session,
		ExplicitSession:  flagWasSet(fs, "session"),
		Fresh:            *p.fresh,
		ExtraBinaryArgs:  binaryArgs,
		RequestedTrust:   trustMode,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelOverrides:   modelOverrides,
		ForceDuplicate:   *p.forceDuplicate,
	}, nil
}
