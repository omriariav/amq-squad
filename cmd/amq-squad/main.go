package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/omriariav/amq-squad/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], effectiveVersion()); err != nil {
		if _, ok := err.(cli.UsageError); ok {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func effectiveVersion() string {
	return resolveVersion(version, buildInfoVersion())
}

func buildInfoVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}

func resolveVersion(ldflagVersion string, moduleVersion string) string {
	if ldflagVersion != "" && ldflagVersion != "dev" {
		return ldflagVersion
	}
	if moduleVersion == "" || moduleVersion == "(devel)" {
		return ldflagVersion
	}
	return moduleVersion
}
