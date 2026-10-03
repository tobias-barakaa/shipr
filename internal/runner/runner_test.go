//go:build linux

package runner

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"shipr/internal/app"
	"shipr/internal/logs"
	"shipr/internal/process"
	"shipr/internal/spec"
	"shipr/internal/testutil"
)

const stubbornChild = `(trap '' TERM; exec sleep 300) & echo $!;`

func workload(t *testing.T, name, script string) spec.Workload {
	t.Helper()
	return spec.Workload{
		Source: app.Application{Name: name},
		Dir:    t.TempDir(),
		Start:  spec.Command{Program: "sh", Args: []string{"-c", script}},
	}
}

func newRunner(t *testing.T) *LocalRunner {
	t.Helper()
	r := New(Options{StopGrace: 300 * time.Millisecond})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = r.StopAll(ctx)
	})
	return r
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func state(r *LocalRunner, name string) process.State {
	info, _ := r.Info(name)
	return info.State
}

func stdoutLines(r *LocalRunner, name string) []string {
	buf, ok := r.Logs(name)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range buf.Snapshot() {
		if e.Stream == logs.Stdout {
			out = append(out, e.Line)
		}
	}
	return out
}

func mustPID(t *testing.T, line string) int {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("%q is not a pid", line)
	}
	return pid
}

func TestStartAndInfo(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", `echo hello; sleep 300`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "log output", func() bool { return len(stdoutLines(r, "svc")) == 1 })

	info, ok := r.Info("svc")
	if !ok || info.State != process.Running || info.PID <= 0 || info.App != "svc" {
		t.Errorf("info = %+v ok=%v", info, ok)
	}
	if got := stdoutLines(r, "svc"); got[0] != "hello" {
		t.Errorf("logs = %v", got)
	}
	if _, ok := r.Info("other"); ok {
		t.Error("unknown application should not be found")
	}
}

func TestDuplicateStartIsRejected(t *testing.T) {
	r := newRunner(t)
	w := workload(t, "svc", `sleep 300`)
	if err := r.Start(testCtx(t), w); err != nil {
		t.Fatal(err)
	}
	before, _ := r.Info("svc")

	err := r.Start(testCtx(t), w)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
	if after, _ := r.Info("svc"); after.PID != before.PID {
		t.Error("the running instance must not be replaced")
	}
}

func TestConcurrentStartsCreateOneInstance(t *testing.T) {
	r := newRunner(t)
	w := workload(t, "svc", `sleep 300`)

	var wg sync.WaitGroup
	results := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- r.Start(testCtx(t), w)
		}()
	}
	wg.Wait()
	close(results)

	ok, dup := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyRunning):
			dup++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || dup != 9 {
		t.Errorf("ok=%d duplicates=%d, want 1 and 9", ok, dup)
	}
}

func TestStopRunningProcess(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", `sleep 300`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(testCtx(t), "svc"); err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info("svc")
	if info.State != process.Stopped || info.StoppedAt.IsZero() || info.Exit == nil {
		t.Errorf("info = %+v", info)
	}
}

func TestStopAlreadyStoppedAndUnknown(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", `sleep 300`)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := r.Stop(testCtx(t), "svc"); err != nil {
			t.Fatalf("Stop #%d: %v", i+1, err)
		}
	}
	if err := r.Stop(testCtx(t), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRestartRunsANewProcessAndKeepsLogs(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", `echo run; sleep 300`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first run output", func() bool { return len(stdoutLines(r, "svc")) == 1 })
	before, _ := r.Info("svc")

	if err := r.Restart(testCtx(t), "svc"); err != nil {
		t.Fatal(err)
	}
	after, _ := r.Info("svc")
	if after.State != process.Running || after.PID == before.PID {
		t.Errorf("before=%+v after=%+v", before, after)
	}
	waitFor(t, "second run output", func() bool { return len(stdoutLines(r, "svc")) == 2 })
}

func TestRestartTerminatesOldChildProcesses(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", stubbornChild+` wait`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "child pid", func() bool { return len(stdoutLines(r, "svc")) == 1 })
	oldChild := mustPID(t, stdoutLines(r, "svc")[0])

	if err := r.Restart(testCtx(t), "svc"); err != nil {
		t.Fatal(err)
	}
	if testutil.ProcessAlive(oldChild) {
		t.Errorf("old child %d survived the restart", oldChild)
	}
	waitFor(t, "new child pid", func() bool { return len(stdoutLines(r, "svc")) == 2 })
	if newChild := mustPID(t, stdoutLines(r, "svc")[1]); !testutil.ProcessAlive(newChild) {
		t.Error("the restarted application's child should be running")
	}
}

func TestRestartStoppedAndUnknown(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", `sleep 300`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(testCtx(t), "svc"); err != nil {
		t.Fatal(err)
	}
	if err := r.Restart(testCtx(t), "svc"); err != nil {
		t.Fatalf("Restart of a stopped app: %v", err)
	}
	if got := state(r, "svc"); got != process.Running {
		t.Errorf("state = %s", got)
	}
	if err := r.Restart(testCtx(t), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFailedStartThenRecovery(t *testing.T) {
	r := newRunner(t)
	bad := workload(t, "svc", "")
	bad.Start = spec.Command{Program: "shipr-no-such-binary"}

	if err := r.Start(testCtx(t), bad); err == nil {
		t.Fatal("expected a start error")
	}
	info, ok := r.Info("svc")
	if !ok || info.State != process.Failed || info.Err == "" {
		t.Errorf("info = %+v ok=%v", info, ok)
	}

	if err := r.Start(testCtx(t), workload(t, "svc", `sleep 300`)); err != nil {
		t.Fatalf("a failed start must not block a later start: %v", err)
	}
	if got := state(r, "svc"); got != process.Running {
		t.Errorf("state = %s", got)
	}
}

func TestCrashIsReportedAsFailed(t *testing.T) {
	r := newRunner(t)
	if err := r.Start(testCtx(t), workload(t, "svc", `exit 2`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "crash", func() bool { return state(r, "svc") == process.Failed })
	if info, _ := r.Info("svc"); info.Exit == nil || info.Exit.Code != 2 {
		t.Errorf("info = %+v", info)
	}
	// A crashed application can be started again without a stop.
	if err := r.Start(testCtx(t), workload(t, "svc", `sleep 300`)); err != nil {
		t.Errorf("start after crash: %v", err)
	}
}

func TestStopAllAndList(t *testing.T) {
	r := newRunner(t)
	for _, name := range []string{"b", "a"} {
		if err := r.Start(testCtx(t), workload(t, name, `sleep 300`)); err != nil {
			t.Fatal(err)
		}
	}
	list := r.List()
	if len(list) != 2 || list[0].App != "a" || list[1].App != "b" {
		t.Errorf("List = %+v", list)
	}
	if err := r.StopAll(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if got := state(r, name); got != process.Stopped {
			t.Errorf("%s state = %s", name, got)
		}
	}
}

func TestLifecycleStateTransitions(t *testing.T) {
	r := newRunner(t)
	w := workload(t, "svc", `sleep 300`)
	if err := r.Start(testCtx(t), w); err != nil {
		t.Fatal(err)
	}
	if got := state(r, "svc"); got != process.Running {
		t.Fatalf("after start: %s", got)
	}
	if err := r.Stop(testCtx(t), "svc"); err != nil {
		t.Fatal(err)
	}
	if got := state(r, "svc"); got != process.Stopped {
		t.Fatalf("after stop: %s", got)
	}
	if err := r.Restart(testCtx(t), "svc"); err != nil {
		t.Fatal(err)
	}
	if got := state(r, "svc"); got != process.Running {
		t.Fatalf("after restart: %s", got)
	}
}
