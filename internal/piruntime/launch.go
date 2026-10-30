package piruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Model is one model a seat may use: the pi side of a registry entry (R16:
// the pi package restricts the model list to the seat's registry entry).
type Model struct {
	Provider      string // pi provider name, "ollama"
	ID            string // model id, "qwen3.6:27b"
	BaseURL       string // provider endpoint, "http://localhost:11434/v1"
	API           string // pi api kind, "openai-completions"
	APIKey        string // the literal pi needs for keyless local servers ("ollama"), never a secret
	ContextWindow int
	MaxTokens     int
	Reasoning     bool
}

// Ref is the provider/id form pi and the package use.
func (m Model) Ref() string { return m.Provider + "/" + m.ID }

// Seat names a pi agent directory: [A-Za-z0-9._-]+, not . or .. (S3).
var seatRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Spec is one pi launch: the lead or a pi seat.
type Spec struct {
	Runtime  Runtime
	Home     string
	Seat     string   // handle; the agent dir is ~/.amq-squad/pi-agent/<seat>
	AgentDir string   // override of the agent dir (tests, probes)
	Parent   []string // the parent environment
	EnvNeeds []string // provider variables the seat's runtime declares it needs
	Models   []Model  // the registry entry's models; the first is the default
	Thinking string   // the registry entry's effort as a pi thinking level ("" keeps pi's default)
	Overlay  map[string]string
	TmpDir   string   // the seat's TMPDIR (default <agent-dir>/tmp); created 0700
	Package  bool     // load the @amq-squad/pi package with -e
	Args     []string // pi arguments after the launcher's own
}

// AgentDirFor is ~/.amq-squad/pi-agent/<seat> (plan 4.1).
func AgentDirFor(home, seat string) string {
	return filepath.Join(home, ".amq-squad", "pi-agent", seat)
}

// Launch is the exec the launcher performs.
type Launch struct {
	Path     string
	Argv     []string
	Env      []string
	AgentDir string
	Removed  []string // scrubbed variable names, for the record
}

// Prepare writes the seat's pi config (models.json, settings.json), links fd
// and rg into <agent-dir>/bin, and returns the exec. It never resolves pi or
// node on PATH (R2): the path is the unpacked runtime's Node.
func Prepare(s Spec) (Launch, error) {
	if !seatRE.MatchString(s.Seat) || s.Seat == "." || s.Seat == ".." {
		return Launch{}, fmt.Errorf("invalid pi seat name %q", s.Seat)
	}
	agentDir := s.AgentDir
	if agentDir == "" {
		agentDir = AgentDirFor(s.Home, s.Seat)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return Launch{}, err
	}
	if err := writeAgentConfig(agentDir, s.Models, s.Thinking); err != nil {
		return Launch{}, err
	}
	if err := LinkTools(agentDir, s.Runtime); err != nil {
		return Launch{}, fmt.Errorf("link fd/rg into %s/bin: %w", agentDir, err)
	}
	tmp := s.TmpDir
	if tmp == "" {
		tmp = filepath.Join(agentDir, "tmp")
	}
	if err := privateDir(tmp); err != nil {
		return Launch{}, fmt.Errorf("seat TMPDIR %s: %w", tmp, err)
	}
	overlay := map[string]string{}
	for k, v := range s.Overlay {
		// Review 2, item 1: the NODE_* and JITI_* filter applies to every
		// overlay (the caller's, the seat's, the registry's), not only to
		// the parent environment; only the forced values below reach pi.
		if loaderVar(k) {
			continue
		}
		overlay[k] = v
	}
	// The forced set (reviewer ruling (a)) is applied last, so neither the
	// parent environment nor a caller's overlay can change it.
	for k, v := range Forced(agentDir, tmp, s.Runtime.Dir, s.Runtime.ManifestSHA256) {
		overlay[k] = v
	}
	overlay["AMQ_SQUAD_SEAT"] = s.Seat
	overlay["AMQ_SQUAD_MODELS"] = modelRefs(s.Models)
	env, removed := ScrubEnv(s.Parent, s.EnvNeeds, overlay)
	argv := s.Runtime.PiArgv()
	if s.Package {
		pkg := s.Runtime.PackageDir()
		if pkg == "" {
			return Launch{}, errors.New("the embedded runtime carries no @amq-squad/pi package")
		}
		argv = append(argv, "-e", pkg)
	}
	argv = append(argv, s.Args...)
	return Launch{Path: s.Runtime.Exe(), Argv: argv, Env: env, AgentDir: agentDir, Removed: removed}, nil
}

// Forced is the environment every lead and pi seat launch sets, whatever the
// caller or the parent environment says:
//   - PI_OFFLINE, PI_SKIP_VERSION_CHECK, PI_TELEMETRY=0 and the seat's
//     PI_CODING_AGENT_DIR (plan 4.1, ruling (a));
//   - JITI_FS_CACHE=false: pi 1.3.0 loads every extension through jiti 2.7.0
//     (loader.js always calls jiti.import; tryNative is false under the
//     bundled Node), whose file cache would otherwise keep the transpiled
//     package in <module>/../node_modules/.cache/jiti or os.tmpdir()/jiti and
//     run it from there, outside the verified runtime and outside
//     ~/.amq-squad (4-1 cold-start finding). With the cache off jiti
//     transforms in memory from the re-hashed bytes on every start;
//   - NODE_DISABLE_COMPILE_CACHE=1 (5-amq-A fix round): Node 22's module
//     compile cache (node-compile-cache/ under TMPDIR, which pi enables)
//     would otherwise keep V8 code caches of the runtime between starts;
//     with it off every start compiles from the bytes the re-hash verified;
//   - TMPDIR: the seat's own 0700 directory, so any other temp writer in pi
//     or node lands where the same-UID threat model applies and doctor can
//     see it;
//   - NODE_OPTIONS empty (review item 1): with every NODE_* variable of the
//     parent dropped, no --require, --import, --loader or inspector flag
//     reaches the verified node, and NODE_PATH and NODE_REPL_EXTERNAL_MODULE
//     cannot add a module root;
//   - AMQ_SQUAD_RUNTIME_DIR and AMQ_SQUAD_RUNTIME_MANIFEST_SHA256 (review
//     item 2 (b)): the package's first code re-hashes the bundle and itself
//     against the manifest this digest pins, in process, before any hook.
func Forced(agentDir, tmp, runtimeDir, manifestSHA string) map[string]string {
	return map[string]string{
		"PI_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1", "PI_TELEMETRY": "0",
		"PI_CODING_AGENT_DIR":               agentDir,
		"JITI_FS_CACHE":                     "false",
		"NODE_DISABLE_COMPILE_CACHE":        "1",
		"TMPDIR":                            tmp,
		"NODE_OPTIONS":                      "",
		"AMQ_SQUAD_RUNTIME_DIR":             runtimeDir,
		"AMQ_SQUAD_RUNTIME_MANIFEST_SHA256": manifestSHA,
	}
}

// loaderVar reports a variable that changes how node or jiti load code
// (NODE_OPTIONS, NODE_PATH, NODE_TLS_REJECT_UNAUTHORIZED, JITI_*): never
// passed from the parent or an overlay, only from the forced set.
func loaderVar(name string) bool {
	return strings.HasPrefix(name, "NODE_") || strings.HasPrefix(name, "JITI_")
}

// forcedEmpty are forced variables set even when empty, so an inherited
// value can never stand in for them.
var forcedEmpty = map[string]bool{"NODE_OPTIONS": true}

// TeamLeadTools is the team lead's whole tool set (amendment 26): the native
// AMQ tools, the task verbs through the scheduler, a read-only file read and
// directory list scoped to the repository root, and amq-squad status. No
// tool runs a command: that work is dispatched to a seat. The lead's two
// decisions over the scheduler's socket are tools too (3.0.1 t16): the
// widening of a refused task by the paths its refusal names, and the
// roster relaunch; each runs the CLI verb from the lead's own process, the
// descendant the scheduler proves.
var TeamLeadTools = []string{"amq_send", "amq_drain", "amq_reply", "amq_task_list", "amq_task_add", "amq_task_reset", "amq_task_widen", "amq_scheduler_relaunch", "amq_repo_read", "amq_repo_list", "amq_status"}

// ExitRuntimeMismatch is the exit code of a pi process whose in-process
// re-hash refused the runtime; RuntimeMismatchReason is its named reason in
// the launch status (review item 2 (b)).
const (
	ExitRuntimeMismatch   = 70
	RuntimeMismatchReason = "runtime_mismatch_in_process"
)

// ExitReason names a pi exit code the launcher knows, "" otherwise.
func ExitReason(code int) string {
	if code == ExitRuntimeMismatch {
		return RuntimeMismatchReason
	}
	return ""
}

// privateDir creates dir 0700 and refuses a symlink or a non-directory in
// its place; an existing directory is narrowed to 0700.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return errors.New("not a real directory")
	}
	return os.Chmod(dir, 0o700)
}

func modelRefs(ms []Model) string {
	refs := make([]string, len(ms))
	for i, m := range ms {
		refs[i] = m.Ref()
	}
	return strings.Join(refs, ",")
}

// dropped are variables a pi process never inherits: shell startup files
// (the pi tool shell runs without the owner's profile, R16 and amendment 17)
// and pi variables that would move its config or sessions out of the seat's
// agent dir.
var dropped = map[string]bool{
	"BASH_ENV": true, "ENV": true, "ZDOTDIR": true,
	"PI_CODING_AGENT_SESSION_DIR": true, "PI_PACKAGE_DIR": true,
}

// scrubbedNames mirror seatexec.Scrubbed (brief 2b-2): vendor credential
// names that the suffix rule misses, and the SSH agent.
var scrubbedNames = map[string]bool{
	"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true,
	"GOOGLE_APPLICATION_CREDENTIALS": true, "AZURE_OPENAI_KEY": true, "GITHUB_PAT": true,
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "DOCKER_AUTH_CONFIG": true, "NPM_CONFIG__AUTH": true,
}

// Scrubbed reports whether a credential variable is removed (plan 4.3, R16):
// *_API_KEY, *_TOKEN, *_SECRET, names with _SECRET_ inside or a _PASSWORD
// suffix, and the known vendor names. It is the same rule as seat-exec's.
func Scrubbed(name string) bool {
	upper := strings.ToUpper(name)
	switch {
	case scrubbedNames[upper]:
		return true
	case strings.HasSuffix(upper, "_API_KEY"), strings.HasSuffix(upper, "_TOKEN"), strings.HasSuffix(upper, "_SECRET"):
		return true
	case strings.Contains(upper, "_SECRET_"), strings.HasSuffix(upper, "_PASSWORD"):
		return true
	}
	return false
}

// ScrubEnv is the parent environment minus credentials (unless needed),
// minus the dropped names and anything the overlay sets, plus the overlay.
func ScrubEnv(parent, needs []string, overlay map[string]string) ([]string, []string) {
	keep := map[string]bool{}
	for _, n := range needs {
		keep[strings.TrimSpace(n)] = true
	}
	var out, removed []string
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if _, set := overlay[name]; set {
			continue
		}
		// JITI_* from the parent could change how pi loads the package
		// (alias, native loading, cache); NODE_* could make node load code
		// before pi (NODE_OPTIONS=--require=..., NODE_PATH): only the forced
		// values go in (review item 1).
		if dropped[name] || loaderVar(name) || (Scrubbed(name) && !keep[name]) {
			removed = append(removed, name)
			continue
		}
		out = append(out, kv)
	}
	names := make([]string, 0, len(overlay))
	for k := range overlay {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if overlay[k] != "" || forcedEmpty[k] {
			out = append(out, k+"="+overlay[k])
		}
	}
	sort.Strings(removed)
	return out, removed
}

// writeAgentConfig writes models.json with only the seat's models and a
// settings.json that pins the startup model, keeps telemetry off and sets
// the tool shell to /bin/bash (pi runs `bash -c`, a non-interactive shell
// that reads no profile once BASH_ENV is dropped). Files are rewritten only
// when their bytes change.
func writeAgentConfig(dir string, models []Model, thinking string) error {
	type piModel struct {
		ID            string `json:"id"`
		ContextWindow int    `json:"contextWindow,omitempty"`
		MaxTokens     int    `json:"maxTokens,omitempty"`
		Reasoning     bool   `json:"reasoning,omitempty"`
	}
	type provider struct {
		BaseURL string    `json:"baseUrl"`
		API     string    `json:"api"`
		APIKey  string    `json:"apiKey,omitempty"`
		Models  []piModel `json:"models"`
	}
	providers := map[string]*provider{}
	var order []string
	for _, m := range models {
		if m.BaseURL == "" {
			continue // a built-in provider: pi knows it, auth comes from the declared env
		}
		p := providers[m.Provider]
		if p == nil {
			p = &provider{BaseURL: m.BaseURL, API: m.API, APIKey: m.APIKey}
			providers[m.Provider] = p
			order = append(order, m.Provider)
		}
		p.Models = append(p.Models, piModel{ID: m.ID, ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, Reasoning: m.Reasoning})
	}
	cfg := map[string]any{"providers": providers}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := writeIfChanged(filepath.Join(dir, "models.json"), append(raw, '\n')); err != nil {
		return err
	}
	settings := map[string]any{
		"enableInstallTelemetry": false,
		"quietStartup":           true,
		"shellPath":              "/bin/bash",
	}
	if len(models) > 0 {
		settings["defaultProvider"] = models[0].Provider
		settings["defaultModel"] = models[0].ID
		refs := make([]string, len(models))
		for i, m := range models {
			refs[i] = m.Ref()
		}
		settings["enabledModels"] = refs
	}
	if thinking != "" {
		settings["defaultThinkingLevel"] = thinking
	}
	raw, _ = json.MarshalIndent(settings, "", "  ")
	return writeIfChanged(filepath.Join(dir, "settings.json"), append(raw, '\n'))
}

func writeIfChanged(p string, b []byte) error {
	if cur, err := os.ReadFile(p); err == nil && string(cur) == string(b) {
		return nil
	}
	tmp := p + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// LinkTools points <agentDir>/bin/<name> at the runtime's fd and rg, where pi
// looks before PATH and before its own download (S3). An existing symlink is
// re-pointed atomically; a regular file the owner put there is left alone.
func LinkTools(agentDir string, rt Runtime) error {
	if len(rt.Manifest.Tools) == 0 {
		return nil
	}
	bin := filepath.Join(agentDir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	names := make([]string, 0, len(rt.Manifest.Tools))
	for n := range rt.Manifest.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		want := rt.abs(rt.Manifest.Tools[name])
		dst := filepath.Join(bin, name)
		if cur, err := os.Readlink(dst); err == nil && cur == want {
			continue
		} else if err != nil {
			if fi, statErr := os.Lstat(dst); statErr == nil && fi.Mode()&fs.ModeSymlink == 0 {
				continue
			}
		}
		tmp := dst + ".amq-squad-tmp"
		_ = os.Remove(tmp)
		if err := os.Symlink(want, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
	}
	return nil
}
