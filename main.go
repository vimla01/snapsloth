// Command snapsloth sorts, searches and safety-checks a folder of screenshots
// using a local vision model. Nothing leaves the machine.
package main

import (
	"fmt"
	"os"
)

const usageText = `snapsloth 🦥  for every screenshot you said "baad mai" to

Usage:
  snapsloth <command> [flags] <dir> [args]

Commands:
  help      show this help
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "snapsloth: unknown command %q\n\n%s", cmd, usageText)
		os.Exit(2)
	}
}
