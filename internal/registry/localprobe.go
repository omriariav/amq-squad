package registry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// MeasureTTFT is the direct time to first token against Ollama's
// OpenAI-compatible streaming API (amendment 10; ported from
// spike/piprobe/ttft.mjs). TextMS is the first non-empty visible content
// delta, ReasoningMS the first reasoning delta of a thinking model (0 when
// none). pi's first event on a tool-call turn is not a first token, because
// Ollama sends the whole call as one chunk, so the registry uses this number.
func MeasureTTFT(ctx context.Context, env Env, model string, maxTokens int) (Latency, error) {
	env = env.withDefaults()
	if !LoopbackURL(env.OllamaHost) {
		return Latency{}, fmt.Errorf(ErrOllamaNotLocal)
	}
	body, _ := json.Marshal(map[string]any{
		"model": model, "stream": true, "max_tokens": maxTokens,
		"messages": []map[string]string{{"role": "user", "content": "Name three Go standard library packages, one per line."}},
	})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(env.OllamaHost, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Latency{}, err
	}
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: 5 * time.Minute, Transport: env.HTTP.Transport, CheckRedirect: noRedirect}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Latency{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Latency{}, fmt.Errorf("ollama: %s", resp.Status)
	}
	var lat Latency
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") || line == "data: [DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		ms := int(time.Since(start).Milliseconds())
		if lat.ReasoningMS == 0 && (d.Reasoning != "" || d.ReasoningContent != "") {
			lat.ReasoningMS = max(ms, 1)
		}
		if d.Content != "" {
			lat.TextMS = max(ms, 1)
			return lat, nil
		}
	}
	if err := sc.Err(); err != nil {
		return lat, err
	}
	return lat, fmt.Errorf("no visible text within %d tokens", maxTokens)
}

// PiProbeEnv writes the isolated pi agent directory the probe runs with
// (~/.amq-squad/probe/pi-agent, never ~/.pi; the S5 setup) and returns the
// environment for pi: the Ollama provider with every local model, startup
// network operations off, no telemetry.
func PiProbeEnv(home string, env Env, models []OllamaModel) ([]string, error) {
	env = env.withDefaults()
	parts := []string{".amq-squad", "probe", "pi-agent"}
	dir := filepath.Join(append([]string{home}, parts...)...)
	type piModel struct {
		ID            string `json:"id"`
		ContextWindow int    `json:"contextWindow"`
		MaxTokens     int    `json:"maxTokens"`
		Reasoning     bool   `json:"reasoning,omitempty"`
	}
	var list []piModel
	for _, m := range models {
		list = append(list, piModel{ID: m.Name, ContextWindow: 131072, MaxTokens: 16384, Reasoning: m.Thinking})
	}
	cfg := map[string]any{"providers": map[string]any{"ollama": map[string]any{
		"baseUrl": strings.TrimRight(env.OllamaHost, "/") + "/v1", "api": "openai-completions", "apiKey": "ollama", "models": list,
	}}}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := writeAnchored(home, parts, "models.json", raw); err != nil {
		return nil, err
	}
	return []string{"PI_CODING_AGENT_DIR=" + dir, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0"}, nil
}

// PiCommand is the command that runs the built-in pi: the executable and
// entry arguments of the manifest RehashRuntime verified. It never reads a
// manifest by pathname, so a manifest swapped after the verification cannot
// name another executable. A PATH pi is never run (R2).
func PiCommand(res RehashResult) ([]string, error) {
	m := res.Manifest
	if res.Dir == "" || m.Executable == "" || !relClean(m.Executable) {
		return nil, fmt.Errorf("the verified runtime manifest names no clean executable")
	}
	cmd := []string{filepath.Join(res.Dir, filepath.FromSlash(m.Executable))}
	for _, a := range m.Args {
		if !strings.HasPrefix(a, "-") {
			if !relClean(a) {
				return nil, fmt.Errorf("runtime manifest argument %q is not a clean relative path", a)
			}
			a = filepath.Join(res.Dir, filepath.FromSlash(a))
		}
		cmd = append(cmd, a)
	}
	return cmd, nil
}

func relClean(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

// SafeDir creates home/parts... (mode 0700) through the anchored opener:
// every component is opened from its parent's descriptor with O_NOFOLLOW,
// so a symlinked component is refused. It returns the path for callers that
// pass it to a child process.
func SafeDir(home string, parts ...string) (string, error) {
	d, err := fsanchor.OpenAnchored(home, parts, true)
	if err != nil {
		return "", err
	}
	d.Close()
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// writeAnchored writes name under home/parts... through the descriptor of
// the directory: an O_EXCL temp file, fsync, rename, fsync of the
// directory. Swapping a component for a symlink after the open changes
// nothing: the write goes to the directory that was opened.
func writeAnchored(home string, parts []string, name string, data []byte) error {
	d, err := fsanchor.OpenAnchored(home, parts, true)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.WriteAtomic(name, data)
}
