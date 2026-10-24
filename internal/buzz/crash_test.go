package buzz

import (
	"encoding/hex"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// 4-5 review item 1: the signer holds the owner key while it publishes the
// policy. The owner script must run it with no core limit and no traceback,
// even when the caller's environment asks for crash dumps. The test raises
// the soft core limit and sets GOTRACEBACK=crash around the script (the
// hazard), makes the policy publication hang on a relay that never answers,
// kills the signer with SIGABRT mid-publish, then looks for a core file (the
// system core directory, HOME, the working directory) and greps every
// output and file for the throwaway key.
func TestOwnerSignerCrashLeavesNoKey(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, bin := range []string{"amq", "amq-remote", "amq-owner-sign", "script", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("the owner script is bash")
	}
	start := time.Now()
	home := shortTempDir(t)
	pm, shimDir := realTempDir(t), realTempDir(t)
	bin := filepath.Join(home, "bin")
	amqSquad := filepath.Join(bin, "amq-squad")
	if out, err := exec.Command("go", "build", "-o", amqSquad, "../../cmd/amq-squad").CombinedOutput(); err != nil {
		t.Fatalf("build amq-squad: %v\n%s", err, out)
	}
	script, _ := filepath.Abs("../../docs/v3-buzz/enroll.sh")

	// a relay that accepts and never answers: the policy publication hangs
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		for _, c := range held {
			_ = c.Close()
		}
	})
	relayURL := "ws://" + ln.Addr().String()

	// the shim records the signer's core limit, GOTRACEBACK and pid, then
	// becomes the real signer (same pid, same stdin pipe)
	realSigner, _ := exec.LookPath("amq-owner-sign")
	shimOut, shimPID := filepath.Join(shimDir, "limits"), filepath.Join(shimDir, "pid")
	shim := "#!/bin/bash\necho \"core=$(ulimit -c) gotraceback=${GOTRACEBACK-unset}\" >'" + shimOut + "'\necho $$ >'" + shimPID + "'\nexec '" + realSigner + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "amq-owner-sign"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	// the hazard: a caller that allows cores and asks Go for crash dumps
	wrapper := filepath.Join(shimDir, "hazard.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/bash\nulimit -c unlimited 2>/dev/null || ulimit -c \"$(ulimit -H -c)\"\nexport GOTRACEBACK=crash\nexec bash '"+script+"' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	owner := newThrowaway(t)
	raw, _ := hex.DecodeString(owner.hex())
	canaries := []string{bech32("nsec", raw), owner.hex(), strings.ToUpper(owner.hex())}
	keyFile := filepath.Join(pm, "owner-throwaway.key")
	if err := os.WriteFile(keyFile, []byte(canaries[0]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	npub, _ := nostr.EncodeNpub(owner.pubHex())
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "BUZZ_") && !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "AM_") && !strings.HasPrefix(kv, "GOTRACEBACK=") {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home, "PATH="+shimDir+string(os.PathListSeparator)+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "AMQ_SQUAD="+amqSquad,
		"BUZZ_NO_CLIPBOARD=1", "BUZZ_UP_SETTLE=1", "AMQ_NO_UPDATE_CHECK=1")
	t.Cleanup(func() { stopPID(filepath.Join(home, ".amq-squad", "buzz", "enroll-up.pid")) })

	first := exec.Command("bash", script, npub, relayURL)
	first.Env = env
	out1, _ := first.CombinedOutput()
	shareTxt, _ := os.ReadFile(filepath.Join(home, ".amq-squad", "buzz", "share.txt"))
	body := ""
	for _, line := range strings.Split(string(shareTxt), "\n") {
		if v, ok := strings.CutPrefix(line, "body-pubkey:"); ok {
			body = strings.TrimSpace(v)
		}
	}
	if len(body) != 64 {
		t.Fatalf("phase A wrote no body:\n%s", out1)
	}

	done := make(chan run, 1)
	go func() {
		done <- ptyRun(t, "signer killed mid-publish", wrapper, []string{npub, relayURL}, append(slices.Clone(env), "BUZZ_OWNER_KEY_CMD=cat '"+keyFile+"'"), body[:8]+"\n")
	}()
	// the shim's pid file appears when the signer starts; the body prefix is
	// typed at 4 s, the key read and the grants signed, then the publish hangs
	waitFor(t, 30*time.Second, "the signer to start", func() bool { _, err := os.Stat(shimPID); return err == nil })
	waitFor(t, 30*time.Second, "the bundle (the signer is now publishing, key in memory)", func() bool {
		_, err := os.Stat(filepath.Join(home, ".amq-squad", "buzz", "bundle.json"))
		return err == nil
	})
	pidRaw, _ := os.ReadFile(shimPID)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
	if err := syscall.Kill(pid, syscall.SIGABRT); err != nil {
		t.Fatalf("SIGABRT the signer (pid %d): %v", pid, err)
	}
	r := <-done
	t.Logf("signer killed with SIGABRT, pid %d; script rc %d", pid, r.rc)

	limits, _ := os.ReadFile(shimOut)
	if strings.TrimSpace(string(limits)) != "core=0 gotraceback=none" {
		t.Errorf("the signer ran with %q, want core=0 gotraceback=none", strings.TrimSpace(string(limits)))
	}
	// Go prints a fatal-signal trace whatever GOTRACEBACK says; the script
	// must drop it (it can carry key words in its registers and arguments)
	if strings.Contains(r.out, "goroutine ") || strings.Contains(r.out, "SIGABRT: abort") || strings.Contains(r.out, "PC=0x") {
		t.Errorf("the signer's crash trace reached the terminal:\n%s", r.out)
	}
	if !strings.Contains(r.out, "its crash trace is not shown") || !strings.Contains(r.out, "type its first 8 characters to sign") {
		t.Errorf("the prompt or the crash notice is missing:\n%s", r.out)
	}

	// core files: the system core directory, HOME, the working directory
	var cores []string
	coreDirs := []string{home}
	if runtime.GOOS == "darwin" {
		coreDirs = append(coreDirs, "/cores")
	}
	for _, d := range coreDirs {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			info, ierr := e.Info()
			if ierr == nil && strings.HasPrefix(e.Name(), "core") && info.ModTime().After(start) {
				cores = append(cores, p)
			}
			return nil
		})
	}
	if len(cores) > 0 {
		t.Errorf("core files written during the run: %v", cores)
	}

	hits := func(text string) bool {
		for _, c := range canaries {
			if strings.Contains(text, c) {
				return true
			}
		}
		return false
	}
	if pmKey, _ := os.ReadFile(keyFile); !hits(string(pmKey)) {
		t.Fatal("the canary scan does not detect the key")
	}
	scanned := 0
	for name, text := range map[string]string{"phase A": string(out1), "pty": r.out, "environment": strings.Join(env, "\n")} {
		scanned++
		if hits(text) {
			t.Errorf("CANARY FOUND in %s", name)
		}
	}
	for _, d := range append(coreDirs, shimDir) {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !e.Type().IsRegular() || strings.HasPrefix(p, bin+string(os.PathSeparator)) {
				return nil
			}
			if info, ierr := e.Info(); ierr != nil || info.Size() > 64<<20 || (d == "/cores" && !info.ModTime().After(start)) {
				return nil
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			scanned++
			if hits(string(data)) {
				t.Errorf("CANARY FOUND in %s", p)
			}
			return nil
		})
	}
	t.Logf("CRASH-PATH CANARY: signer limits %q, %d core files, %d outputs and files scanned (HOME, the shim dir, %v): %s",
		strings.TrimSpace(string(limits)), len(cores), scanned, coreDirs, map[bool]string{true: "FAILED", false: "no key found"}[t.Failed()])
}
