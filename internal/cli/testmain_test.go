package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	tempDir := ""
	modelDir, err := os.MkdirTemp("", "amq-squad-test-models-*")
	if err != nil {
		panic(err)
	}
	modelUserConfigDir = func() (string, error) { return filepath.Join(modelDir, "config"), nil }
	modelUserHomeDir = func() (string, error) { return filepath.Join(modelDir, "home"), nil }
	if err := os.Setenv("CODEX_HOME", filepath.Join(modelDir, "codex")); err != nil {
		panic(err)
	}
	if err := os.Setenv("AMQ_SQUAD_CONFIG", filepath.Join(modelDir, "missing.json")); err != nil {
		panic(err)
	}
	if _, err := exec.LookPath("amq"); err != nil {
		var setupErr error
		tempDir, setupErr = installPackageTestAMQ()
		if setupErr != nil {
			panic(setupErr)
		}
	}
	code := m.Run()
	if tempDir != "" {
		_ = os.RemoveAll(tempDir)
	}
	_ = os.RemoveAll(modelDir)
	os.Exit(code)
}

func installPackageTestAMQ() (string, error) {
	dir, err := os.MkdirTemp("", "amq-squad-test-amq-*")
	if err != nil {
		return "", err
	}
	script := `#!/bin/sh
if [ "$1" = "env" ]; then
  shift
  root=""
  session=""
  me=""
  project=""
  root_source=""
  if [ -f .amqrc ]; then
    project=$(sed -n 's/.*"project":"\([^"]*\)".*/\1/p' .amqrc)
    configured_root=$(sed -n 's/.*"root":"\([^"]*\)".*/\1/p' .amqrc)
    if [ -n "$project" ]; then
      root_source="project_amqrc"
    fi
  fi
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --root)
        root="$2"
        shift 2
        ;;
      --session)
        session="$2"
        shift 2
        ;;
      --me)
        me="$2"
        shift 2
        ;;
      --json)
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  if [ -z "$root" ]; then
    if [ -n "$configured_root" ]; then
      root="$configured_root"
    fi
  fi
  if [ -z "$root" ]; then
    if [ -n "$session" ]; then
      root=".agent-mail/$session"
    else
      root=".agent-mail"
    fi
  elif [ -n "$session" ] && [ "$(basename "$root")" = ".agent-mail" ]; then
    root="$root/$session"
  fi
  base_root="$root"
  if [ -n "$session" ]; then
    base_root=$(dirname "$root")
  fi
  printf '{"schema_version":1,"amq_version":"0.38.0","root":"%s","base_root":"%s","session_name":"%s","me":"%s","project":"%s","root_source":"%s"}\n' "$root" "$base_root" "$session" "$me" "$project" "$root_source"
  exit 0
fi
if [ "$1" = "route" ] && [ "$2" = "explain" ]; then
  shift 2
  from_root=""
  me=""
  to=""
  project=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --from-root)
        from_root="$2"
        shift 2
        ;;
      --me)
        me="$2"
        shift 2
        ;;
      --to)
        to="$2"
        shift 2
        ;;
      --project)
        project="$2"
        shift 2
        ;;
      --session|--target-session)
        shift 2
        ;;
      --json)
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  project_args=""
  if [ -n "$project" ]; then
    project_args=",\"--project\",\"$project\""
  fi
  printf '{"routable":true,"argv":["amq","send","--root","%s","--me","%s","--to","%s"%s]}\n' "$from_root" "$me" "$to" "$project_args"
  exit 0
fi
if [ "$1" = "ops" ]; then
  printf '{}\n'
  exit 0
fi
if [ "$1" = "version" ]; then
  printf '0.38.0\n'
  exit 0
fi
echo "fake amq: unsupported command: $*" >&2
exit 2
`
	path := filepath.Join(dir, "amq")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	oldPath := os.Getenv("PATH")
	if oldPath == "" {
		os.Setenv("PATH", dir)
	} else {
		os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	}
	return dir, nil
}
