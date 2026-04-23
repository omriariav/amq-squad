package main

import (
	"fmt"
	"os"

	"github.com/omriariav/amq-squad/internal/cli"
)

var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], version); err != nil {
		if _, ok := err.(cli.UsageError); ok {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
