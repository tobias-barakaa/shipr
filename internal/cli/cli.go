// Package cli dispatches commands, validates arguments, and maps errors to
// exit codes.
package cli

import (
	"errors"
	"fmt"
	"io"
)

const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Command is one CLI subcommand.
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

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func newUsageError(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// Run executes the CLI and returns a process exit code.
func Run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		usage(errOut)
		return ExitUsage
	}

	switch args[0] {
	case "help", "-h", "--help":
		usage(out)
		return ExitOK
	}

	cmd := find(args[0])
	if cmd == nil {
		fmt.Fprintf(errOut, "unknown command %q\n\n", args[0])
		usage(errOut)
		return ExitUsage
	}

	if err := cmd.Run(args[1:], out); err != nil {
		var ue *usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(errOut, ue.msg)
			return ExitUsage
		}
		fmt.Fprintf(errOut, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func find(name string) Command {
	for _, c := range commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "Usage:\n  shipr <command> [arguments]\n\nCommands:\n")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.Name(), c.Summary())
	}
}
