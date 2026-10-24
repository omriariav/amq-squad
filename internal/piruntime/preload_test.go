package piruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Review 2, item 2 (b): node loads the one-purpose preload with --require
// before the pi entry, and the preload is part of the launch set CheckLaunch
// re-hashes by content.
func TestPreloadRunsBeforeTheEntry(t *testing.T) {
	tree := t.TempDir()
	for rel, body := range map[string]string{
		"node/bin/node":                "#!/bin/sh\n",
		"pi/node_modules/pi/cli.js":    "entry\n",
		"pi/package.json":              "{}",
		"pi/preload/runtime-check.cjs": "// check\n",
	} {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if rel == "node/bin/node" {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	packed, err := Pack(tree, PackSpec{Version: "pi-pre", Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: "node/bin/node",
		Args: []string{"pi/node_modules/pi/cli.js"}, Package: "pi", Preload: "pi/preload/runtime-check.cjs"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseEmbedded(packed.Manifest, packed.Digest, func() ([]byte, error) { return packed.Payload, nil })
	if err != nil {
		t.Fatal(err)
	}
	rt, _, err := Ensure(tempHome(t), e)
	if err != nil {
		t.Fatal(err)
	}
	argv := rt.PiArgv()
	if len(argv) != 4 || argv[1] != "--require" || argv[2] != filepath.Join(rt.Dir, "pi/preload/runtime-check.cjs") || argv[3] != filepath.Join(rt.Dir, "pi/node_modules/pi/cli.js") {
		t.Fatalf("argv %v: the preload must come before the entry", argv)
	}
	if !rt.contentChecked()["pi/preload/runtime-check.cjs"] {
		t.Fatal("the preload is not re-hashed by content before exec")
	}
	flipByte(t, argv[2])
	if err := rt.CheckLaunch(); err == nil {
		t.Fatal("a changed preload passed the launch check")
	}
	if _, err := ParseEmbedded(packed.Manifest, packed.Digest, nil); err != nil {
		t.Fatal(err)
	}
	bad := e.Manifest
	bad.Preload = "pi/preload/missing.cjs"
	if err := bad.Validate(); err == nil {
		t.Fatal("a preload outside the payload validated")
	}
}
