package registry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	dir, err := SafeDir(home, ".amq-squad", "probe", "pi-agent")
	if err != nil {
		return nil, err
	}
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
	if err := writeFileAtomic(dir, "models.json", raw); err != nil {
		return nil, err
	}
	return []string{"PI_CODING_AGENT_DIR=" + dir, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0"}, nil
}

// PiCommand is the command that runs pi: the PATH pi, or the embedded
// runtime's executable and entry arguments from its manifest.
func PiCommand(t Tool) ([]string, error) {
	if !t.Found {
		return nil, fmt.Errorf("pi is not installed")
	}
	info, err := os.Stat(t.Path)
	if err != nil || !info.IsDir() {
		return []string{t.Path}, nil
	}
	raw, err := os.ReadFile(filepath.Join(t.Path, RuntimeManifestName))
	if err != nil {
		return nil, err
	}
	var m struct {
		Executable string   `json:"executable"`
		Args       []string `json:"args"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Executable == "" {
		return nil, fmt.Errorf("runtime manifest names no executable")
	}
	cmd := []string{filepath.Join(t.Path, filepath.FromSlash(m.Executable))}
	for _, a := range m.Args {
		if !strings.HasPrefix(a, "-") {
			a = filepath.Join(t.Path, filepath.FromSlash(a))
		}
		cmd = append(cmd, a)
	}
	return cmd, nil
}

// SafeDir creates home/parts... (mode 0700) and refuses a symlink or a
// non-directory at any component below home, so a write under it cannot be
// redirected outside the trusted directory.
func SafeDir(home string, parts ...string) (string, error) {
	cur := home
	for _, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			if err := os.Mkdir(cur, 0o700); err != nil {
				return "", err
			}
			st, err = os.Lstat(cur)
		}
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return "", fmt.Errorf("%s is a symlink or not a directory; refusing to write under it", cur)
		}
	}
	return cur, nil
}

// writeFileAtomic writes through a temp file in dir and renames it over
// name, so an existing name that is a symlink is replaced, never followed.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, name))
}
