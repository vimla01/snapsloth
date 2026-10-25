// Command snapsloth sorts, searches and safety-checks a folder of screenshots
// using a local vision model. Nothing leaves the machine.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const usageText = `snapsloth 🦥  for every screenshot you said "baad mai" to

Usage:
  snapsloth <command> [flags] <dir> [args]

Commands:
  scan <dir>      analyse new screenshots with the local model
                  [--model gemma4:e4b] [--host URL] [--limit N] [--dry-run]
  help            show this help
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "scan":
		err = cmdScan(args)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "snapsloth: unknown command %q\n\n%s", cmd, usageText)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "snapsloth:", err)
		os.Exit(1)
	}
}

// parseFlags lets flags appear before or after positional arguments, so both
// "sort --dry-run dir" and "sort dir --dry-run" work.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// dirArg checks that exactly n positional args were given and the first is a directory.
func dirArg(pos []string, n int, usage string) (string, error) {
	if len(pos) != n {
		return "", errors.New("usage: snapsloth " + usage)
	}
	info, err := os.Stat(pos[0])
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New(pos[0] + " is not a directory")
	}
	return pos[0], nil
}
