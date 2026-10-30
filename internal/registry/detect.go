package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ToolOrder is the detection order of plan 5 plus the built-in pi runtime.
var ToolOrder = []string{"claude", "codex", "pi", "grok", "cursor-agent", "ollama", "amq", "amq-remote", "tmux", "gh"}

// Env is the detection seam. Zero fields take the real host.
type Env struct {
	Home     string
	GOOS     string
	LookPath func(name string) (string, error)
	// Run runs a command with a deadline and returns its combined output.
	Run    func(ctx context.Context, name string, args ...string) (string, error)
	Getenv func(name string) string
	// HTTP talks to the Ollama server.
	HTTP       *http.Client
	OllamaHost string
	// AMQModule is the pinned AMQ library version (PinnedAMQModule by default).
	AMQModule string
}

func (e Env) withDefaults() Env {
	if e.Home == "" {
		e.Home, _ = os.UserHomeDir()
	}
	if e.GOOS == "" {
		e.GOOS = runtime.GOOS
	}
	if e.LookPath == nil {
		e.LookPath = exec.LookPath
	}
	if e.Run == nil {
		e.Run = runCommand
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.HTTP == nil {
		e.HTTP = &http.Client{Timeout: time.Second}
	}
	// Any client, supplied or default, follows no redirect (a redirect could
	// leave loopback). The supplied client is copied, never mutated.
	client := *e.HTTP
	client.CheckRedirect = noRedirect
	e.HTTP = &client
	if e.OllamaHost == "" {
		e.OllamaHost = e.Getenv("OLLAMA_HOST")
	}
	if e.OllamaHost == "" {
		e.OllamaHost = "http://127.0.0.1:11434"
	}
	if !strings.Contains(e.OllamaHost, "://") {
		e.OllamaHost = "http://" + e.OllamaHost
	}
	if e.AMQModule == "" {
		e.AMQModule = PinnedAMQModule
	}
	return e
}

// runCommand runs one probe command. AMQ_NO_UPDATE_CHECK keeps amq and
// amq-remote from printing (or fetching) an update notice.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "AMQ_NO_UPDATE_CHECK=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// OllamaModel is one model the local Ollama server lists.
type OllamaModel struct {
	Name      string
	CapsKnown bool // /api/show answered with a capabilities list
	Tools     bool
	Thinking  bool
}

// Detection is one probe of the host.
type Detection struct {
	Host         Host
	Tools        []Tool
	Ollama       []OllamaModel
	OllamaErr    string          // why the model list is empty, "" when the server answered
	APIKeys      map[string]bool // provider API key names present in the environment (never values)
	PiRuntimeDir string          // the embedded pi runtime directory, "" when none is installed
}

// Tool returns the detected tool by name.
func (d Detection) Tool(name string) Tool {
	for _, t := range d.Tools {
		if t.Name == name {
			return t
		}
	}
	return Tool{Name: name}
}

var versionCmd = map[string][]string{
	"claude": {"--version"}, "codex": {"--version"}, "grok": {"--version"},
	"cursor-agent": {"--version"}, "ollama": {"--version"}, "amq": {"--version"},
	"amq-remote": {"--version"}, "tmux": {"-V"}, "gh": {"--version"},
}

var versionRE = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:[-+][0-9A-Za-z.-]+)?`)

// apiKeyNames are the provider key names detection reports (names only).
var apiKeyNames = []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "XAI_API_KEY", "CURSOR_API_KEY"}

// Detect probes PATH, versions, login state, Ollama, RAM and GPU. A missing
// tool is a Tool with Found false, never an error. Version commands run in
// parallel, each under a two-second deadline.
func Detect(ctx context.Context, env Env) Detection {
	env = env.withDefaults()
	d := Detection{APIKeys: map[string]bool{}}
	for _, k := range apiKeyNames {
		if strings.TrimSpace(env.Getenv(k)) != "" {
			d.APIKeys[k] = true
		}
	}
	d.PiRuntimeDir = PiRuntimeDir(env.Home)
	// Each goroutine writes only its own result slot; d is assembled after Wait.
	piDir := d.PiRuntimeDir
	tools := make([]Tool, len(ToolOrder))
	var wg sync.WaitGroup
	for i, name := range ToolOrder {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			tools[i] = detectTool(ctx, env, name, piDir)
		}(i, name)
	}
	var host Host
	wg.Add(1)
	go func() { defer wg.Done(); host = detectHost(ctx, env) }()
	wg.Wait()
	d.Host, d.Tools = host, tools
	// The Ollama server is contacted only when ollama is on PATH and the
	// endpoint is a loopback address (review P2 (8)).
	switch {
	case !d.Tool("ollama").Found:
		d.OllamaErr = "ollama not on PATH"
	case !LoopbackURL(env.OllamaHost):
		d.OllamaErr = ErrOllamaNotLocal
	default:
		d.Ollama, d.OllamaErr = detectOllama(ctx, env)
	}
	return d
}

// ErrOllamaNotLocal is the status line for a non-loopback OLLAMA_HOST.
const ErrOllamaNotLocal = "ollama host not local, skipped"

// LoopbackURL reports whether a URL names a loopback host (localhost,
// 127.0.0.0/8, ::1). Local models are local: detection never calls out.
func LoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// noRedirect refuses every redirect: an Ollama endpoint never needs one,
// and a redirect could leave loopback.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func detectTool(ctx context.Context, env Env, name, piRuntimeDir string) Tool {
	t := Tool{Name: name, Login: "n/a"}
	if name == "pi" {
		// pi is the built-in runtime (R2). Detection reads no runtime file:
		// the version comes from the verified manifest after the re-hash
		// (doctor fills it in), and a pi on PATH is never executed.
		if piRuntimeDir != "" {
			t.Found, t.Path = true, piRuntimeDir
		} else if path, err := env.LookPath(name); err == nil {
			t.Found, t.Path, t.LoginSource = true, path, ""
		} else {
			t.Login = ""
		}
		return t
	}
	path, err := env.LookPath(name)
	if err != nil {
		t.Login, t.VersionCheck = "", ""
		return t
	}
	t.Found, t.Path = true, path
	if t.Version == "" {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		out, _ := env.Run(cctx, path, versionCmd[name]...)
		cancel()
		t.Version = versionRE.FindString(out)
	}
	t.VerifiedVersion = LaunchVerifiedVersion(env.AMQModule, name)
	t.VersionCheck = compareVersions(t.Version, t.VerifiedVersion)
	t.Login, t.LoginSource = detectLogin(ctx, env, name)
	return t
}

// compareVersions reports how the installed version relates to the verified one.
func compareVersions(installed, verified string) string {
	switch {
	case verified == "":
		return "unverified"
	case installed == "":
		return "unknown"
	case installed == verified:
		return "match"
	}
	a, okA := semver(installed)
	b, okB := semver(verified)
	if !okA || !okB {
		return "unknown"
	}
	for i := range a {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return "newer"
			}
			return "older"
		}
	}
	return "match"
}

func semver(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// detectLogin reads the login state without a login: file presence (never
// contents), the macOS keychain item's existence, or an API key name.
func detectLogin(ctx context.Context, env Env, name string) (string, string) {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	dirOr := func(envName, def string) string {
		if v := strings.TrimSpace(env.Getenv(envName)); v != "" {
			return v
		}
		return filepath.Join(env.Home, def)
	}
	switch name {
	case "claude":
		if exists(filepath.Join(dirOr("CLAUDE_CONFIG_DIR", ".claude"), ".credentials.json")) {
			return "logged_in", "file"
		}
		if env.GOOS == "darwin" {
			if sec, err := env.LookPath("security"); err == nil {
				cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
				// Without -g or -w the item's secret is not printed.
				_, err := env.Run(cctx, sec, "find-generic-password", "-s", "Claude Code-credentials")
				cancel()
				if err == nil {
					return "logged_in", "keychain"
				}
			}
		}
		if env.Getenv("ANTHROPIC_API_KEY") != "" {
			return "logged_in", "env"
		}
		return "logged_out", ""
	case "codex":
		if exists(filepath.Join(dirOr("CODEX_HOME", ".codex"), "auth.json")) {
			return "logged_in", "file"
		}
		if env.Getenv("OPENAI_API_KEY") != "" {
			return "logged_in", "env"
		}
		return "logged_out", ""
	case "gh":
		if env.Getenv("GH_TOKEN") != "" || env.Getenv("GITHUB_TOKEN") != "" {
			return "logged_in", "env"
		}
		if exists(filepath.Join(dirOr("GH_CONFIG_DIR", filepath.Join(".config", "gh")), "hosts.yml")) {
			return "logged_in", "file"
		}
		return "logged_out", ""
	case "grok":
		if env.Getenv("XAI_API_KEY") != "" || env.Getenv("GROK_API_KEY") != "" {
			return "logged_in", "env"
		}
		return "unknown", ""
	case "cursor-agent":
		if env.Getenv("CURSOR_API_KEY") != "" {
			return "logged_in", "env"
		}
		return "unknown", ""
	}
	return "n/a", ""
}

func detectHost(ctx context.Context, env Env) Host {
	h := Host{}
	run := func(name string, args ...string) string {
		path, err := env.LookPath(name)
		if err != nil {
			return ""
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		out, err := env.Run(cctx, path, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	switch env.GOOS {
	case "darwin":
		if n, err := strconv.ParseInt(run("sysctl", "-n", "hw.memsize"), 10, 64); err == nil {
			h.RAMGB = int(n >> 30)
		}
		h.CPU = run("sysctl", "-n", "machdep.cpu.brand_string")
		if strings.HasPrefix(h.CPU, "Apple") {
			h.GPU = h.CPU + " (unified memory)"
		}
	case "linux":
		if raw, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
					if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
						h.RAMGB = int((kb + 1<<19) >> 20)
					}
				}
			}
		}
		h.GPU = firstLine(run("nvidia-smi", "--query-gpu=name", "--format=csv,noheader"))
	}
	if h.GPU == "" {
		h.GPU = "unknown"
	}
	return h
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// detectOllama lists the local models and their capabilities.
func detectOllama(ctx context.Context, env Env) ([]OllamaModel, string) {
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := ollamaCall(ctx, env, "GET", "/api/tags", nil, &tags); err != nil {
		return nil, "ollama server: " + err.Error()
	}
	out := make([]OllamaModel, len(tags.Models))
	var wg sync.WaitGroup
	for i, m := range tags.Models {
		out[i].Name = m.Name
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var show struct {
				Capabilities []string `json:"capabilities"`
			}
			body, _ := json.Marshal(map[string]string{"model": out[i].Name})
			if err := ollamaCall(ctx, env, "POST", "/api/show", body, &show); err != nil || show.Capabilities == nil {
				return
			}
			out[i].CapsKnown = true
			for _, c := range show.Capabilities {
				out[i].Tools = out[i].Tools || c == "tools"
				out[i].Thinking = out[i].Thinking || c == "thinking"
			}
		}(i)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ""
}

func ollamaCall(ctx context.Context, env Env, method, path string, body []byte, into any) error {
	if !LoopbackURL(env.OllamaHost) {
		return errors.New(ErrOllamaNotLocal)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(env.OllamaHost, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := env.HTTP.Do(req)
	if err != nil {
		return errors.New("unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// PiRuntimeDir is the newest embedded pi runtime under
// ~/.amq-squad/runtime/<version>-<sha>/ that carries an on-disk manifest.
func PiRuntimeDir(home string) string {
	// The build that embeds pi (4-1) names its own runtime directory: that
	// one, not the lexically newest, is the runtime this binary trusts.
	if EmbeddedRuntimeDirName != "" {
		dir := filepath.Join(home, ".amq-squad", "runtime", EmbeddedRuntimeDirName)
		if _, err := os.Lstat(filepath.Join(dir, RuntimeManifestName)); err == nil {
			return dir
		}
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".amq-squad", "runtime", "*", RuntimeManifestName))
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return filepath.Dir(matches[len(matches)-1])
}
