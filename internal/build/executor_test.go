//go:build linux

package build

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shipr/internal/spec"
)

func sh(script string) spec.Command {
	return spec.Command{Program: "sh", Args: []string{"-c", script}}
}

func TestOSExecutorRunsInDirectoryAndCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	code, err := OSExecutor{}.Run(context.Background(),
		Invocation{Dir: dir, Command: sh("pwd; echo out; echo err >&2; echo $SHIPR_TEST"), Env: []string{"SHIPR_TEST=42"}}, &out)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	got := out.String()
	for _, want := range []string{real, "out", "err", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestOSExecutorReportsExitCode(t *testing.T) {
	code, err := OSExecutor{}.Run(context.Background(), Invocation{Dir: t.TempDir(), Command: sh("exit 3")}, &bytes.Buffer{})
	if code != 3 || err == nil {
		t.Errorf("code=%d err=%v", code, err)
	}
}

func TestOSExecutorStartFailure(t *testing.T) {
	code, err := OSExecutor{}.Run(context.Background(),
		Invocation{Dir: t.TempDir(), Command: spec.Command{Program: "shipr-no-such-binary"}}, &bytes.Buffer{})
	if code != -1 || err == nil {
		t.Errorf("code=%d err=%v", code, err)
	}
}

func TestOSExecutorCancelKillsTheProcessTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := OSExecutor{}.Run(ctx, Invocation{Dir: t.TempDir(), Command: sh("sleep 300 & sleep 300")}, &bytes.Buffer{})
	if err == nil {
		t.Error("expected an error after cancellation")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("cancellation did not stop the build promptly")
	}
}
