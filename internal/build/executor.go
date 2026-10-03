package build

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"shipr/internal/procgroup"
)

// OSExecutor runs commands as real local processes, each in its own process
// group so a cancelled build takes its children with it.
type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, inv Invocation, out io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, inv.Command.Program, inv.Command.Args...)
	cmd.Dir = inv.Dir
	cmd.Env = append(os.Environ(), inv.Env...)
	// The same writer for both streams keeps their interleaving and makes
	// os/exec serialise writes.
	cmd.Stdout = out
	cmd.Stderr = out
	procgroup.Configure(cmd)
	cmd.Cancel = func() error {
		return procgroup.Signal(cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second // don't hang on pipes held by orphans

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), err
	}
	return -1, err
}
