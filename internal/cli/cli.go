package cli

import (
	"fmt"
	"io"
)

// Command is one CLI subcommand (inspect, and later deploy, plan, ...).
type Command interface {
	Name() string
	Summary() string
	Run(args []string, out io.Writer) error
}

// commands is the registry. Add new commands here.
func commands() []Command {
	return []Command{
		Inspect{},
	}
}

// Run dispatches args to a command and returns a process exit code.
func Run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		usage(errOut)
		return 1
	}

	switch args[0] {
	case "help", "-h", "--help":
		usage(out)
		return 0
	}

	for _, c := range commands() {
		if c.Name() != args[0] {
			continue
		}
		if err := c.Run(args[1:], out); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(errOut, "unknown command %q\n\n", args[0])
	usage(errOut)
	return 1
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "deployer - deployment toolkit\n\nUsage:\n  deployer <command> [arguments]\n\nCommands:\n")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.Name(), c.Summary())
	}
}