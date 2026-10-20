// seat-exec is the S4 standalone wrapper prototype, not an installed squad verb.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const namespace = "io.github.omriariav.amq-squad"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seat-exec:", err)
		os.Exit(1)
	}
}

func run() error {
	seat := flag.String("seat", os.Getenv("AM_ME"), "AMQ seat handle")
	root := flag.String("root", os.Getenv("AM_ROOT"), "AMQ session root")
	flag.Parse()
	// Go flags stop at the first positional argument. This supports both the
	// manual '-- provider argv...' form and AMQ's '--seat H provider argv...'.
	argv := flag.Args()
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`).MatchString(*seat) {
		return fmt.Errorf("a safe, nonempty seat handle is required")
	}
	if *root == "" || len(argv) == 0 {
		return fmt.Errorf("root and provider argv are required")
	}
	if me := os.Getenv("AM_ME"); me != "" && me != *seat {
		return fmt.Errorf("--seat disagrees with AM_ME")
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return err
	}
	if ambient := os.Getenv("AM_ROOT"); ambient != "" {
		ambient, err = filepath.Abs(ambient)
		if err != nil {
			return err
		}
		ambient, err = filepath.EvalSymlinks(ambient)
		if err != nil || ambient != rootPath {
			return fmt.Errorf("--root disagrees with AM_ROOT")
		}
	}
	provider, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	// Require AMQ's existing seat; the wrapper does not provision mailboxes.
	seatPath := filepath.Join(rootPath, "agents", *seat)
	seatRoot, err := os.OpenRoot(seatPath)
	if err != nil {
		return err
	}
	defer seatRoot.Close()
	dir := filepath.Join("extensions", namespace)
	if err := seatRoot.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// O_EXCL avoids following an existing file symlink; rename publishes atomically.
	write := func(name string, data []byte) error {
		tmp := filepath.Join(dir, fmt.Sprintf(".%s.%d", name, os.Getpid()))
		f, err := seatRoot.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer seatRoot.Remove(tmp)
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return seatRoot.Rename(tmp, filepath.Join(dir, name))
	}
	runtime := filepath.Base(argv[0])
	role := fmt.Sprintf("# S4 launch probe\n\nSeat: %s\nRuntime: %s\n\nRead this role and report S4_ROLE_VISIBLE with your seat handle. Perform only the requested harmless probe in the scratch project. Do not modify owner configuration or contact external services.\n", *seat, runtime)
	if err := write("role.md", []byte(role)); err != nil {
		return err
	}
	record := struct {
		SeatHandle   string   `json:"seat_handle"`
		Runtime      string   `json:"runtime"`
		ProviderArgv []string `json:"provider_argv"`
		Timestamp    string   `json:"timestamp"`
	}{*seat, runtime, argv, time.Now().UTC().Format(time.RFC3339Nano)}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := write("launch.json", append(data, '\n')); err != nil {
		return err
	}
	for key, value := range map[string]string{
		"AMQ_SQUAD_SEAT":      *seat,
		"AMQ_SQUAD_ROOT":      rootPath,
		"AMQ_SQUAD_ROLE_PATH": filepath.Join(seatPath, dir, "role.md"),
	} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return syscall.Exec(provider, argv, os.Environ())
}
