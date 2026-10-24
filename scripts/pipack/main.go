// Command pipack packs a staged pi runtime tree into the payload that
// internal/piruntime embeds under the amqpi build tag. It is a build step of
// scripts/build-pi-runtime.sh, not part of the shipped binary.
//
//	go run ./scripts/pipack -tree <stage> -out internal/piruntime/payload \
//	    -version pi-1.3.0-node-22.21.1-darwin-arm64 -platform darwin-arm64 \
//	    -pi 1.3.0 -node 22.21.1 -exe node/bin/node \
//	    -entry pi/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js \
//	    -package pi -tool fd=tools/fd -tool rg=tools/rg
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

type tools map[string]string

func (t tools) String() string { return fmt.Sprint(map[string]string(t)) }
func (t tools) Set(v string) error {
	name, path, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("want NAME=PATH, got %q", v)
	}
	t[name] = path
	return nil
}

func main() {
	tl := tools{}
	tree := flag.String("tree", "", "staged runtime tree")
	out := flag.String("out", "", "output directory (payload.tar.gz, manifest.json, manifest.sha256)")
	spec := piruntime.PackSpec{}
	flag.StringVar(&spec.Version, "version", "", "runtime version name")
	flag.StringVar(&spec.Platform, "platform", "darwin-arm64", "payload platform")
	flag.StringVar(&spec.PiVersion, "pi", "", "pi version")
	flag.StringVar(&spec.NodeVersion, "node", "", "Node version")
	flag.StringVar(&spec.Executable, "exe", "node/bin/node", "executable inside the tree")
	entry := flag.String("entry", "", "pi entry script inside the tree")
	flag.StringVar(&spec.Package, "package", "pi", "@amq-squad/pi package directory inside the tree")
	flag.StringVar(&spec.Preload, "preload", "", "runtime self-check module node loads with --require")
	flag.Var(tl, "tool", "NAME=PATH helper linked into <agent-dir>/bin (repeatable)")
	flag.Parse()
	if *tree == "" || *out == "" || spec.Version == "" || *entry == "" {
		flag.Usage()
		os.Exit(2)
	}
	spec.Args = []string{*entry}
	spec.Tools = tl
	p, err := piruntime.Pack(*tree, spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pipack:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "pipack:", err)
		os.Exit(1)
	}
	for name, b := range map[string][]byte{"payload.tar.gz": p.Payload, "manifest.json": p.Manifest, "manifest.sha256": p.Digest} {
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "pipack:", err)
			os.Exit(1)
		}
	}
	var unpacked int64
	for _, f := range p.Info.Files {
		unpacked += f.Size
	}
	summary, _ := json.Marshal(map[string]any{
		"version": spec.Version, "payload_bytes": len(p.Payload), "unpacked_bytes": unpacked,
		"files": len(p.Info.Files), "payload_sha256": p.Info.SHA256, "manifest_sha256": strings.Fields(string(p.Digest))[0],
	})
	fmt.Println(string(summary))
}
