//go:build linux

package process

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"shipr/internal/app"
	"shipr/internal/logs"
	"shipr/internal/spec"
	"shipr/internal/testutil"
)

func workload(t *testing.T, script string) spec.Workload {
	t.Helper()
	return spec.Workload{
		Source: app.Application{Name: "test"},
		Dir:    t.TempDir(),
		Start:  spec.Command{Program: "sh", Args: []string{"-c", script}},
	}
}

func startProc(t *testing.T, script string, opts Options) *Process {
	t.Helper()
	p, err := Start(context.Background(), workload(t, script), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = p.Stop(ctx, 100*time.Millisecond)
	})
	return p
}

func waitDone(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("process did not finish")
	}
}

func stop(t *testing.T, p *Process, grace time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.Stop(ctx, grace); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func waitForLine(t *testing.T, p *Process, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range p.Logs().Snapshot() {
			if e.Line == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log line %q never appeared", want)
}

func lines(p *Process, s logs.Stream) []string {
	var out []string
	for _, e := range p.Logs().Snapshot() {
		if e.Stream == s {
			out = append(out, e.Line)
		}
	}
	return out
}

func firstStdoutPID(t *testing.T, p *Process) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if l := lines(p, logs.Stdout); len(l) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(l[0]))
			if err != nil {
				t.Fatalf("first stdout line %q is not a pid", l[0])
			}
			return pid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pid was printed")
	return 0
}

// A child that ignores SIGTERM, so only SIGKILL on the group can remove it.
const stubbornChild = `(trap '' TERM; exec sleep 300) & echo $!;`

func TestCapturesStdoutStderrAndCleanExit(t *testing.T) {
	p := startProc(t, `echo out1; echo err1 >&2; echo out2`, Options{})
	waitDone(t, p)

	if got := lines(p, logs.Stdout); len(got) != 2 || got[0] != "out1" || got[1] != "out2" {
		t.Errorf("stdout = %v", got)
	}
	if got := lines(p, logs.Stderr); len(got) != 1 || got[0] != "err1" {
		t.Errorf("stderr = %v", got)
	}
	info := p.Info()
	if info.State != Stopped || info.Exit == nil || info.Exit.Code != 0 || info.Exit.Signal != "" {
		t.Errorf("info = %+v", info)
	}
	if info.PID <= 0 || info.App != "test" || info.StartedAt.IsZero() || info.StoppedAt.Before(info.StartedAt) {
		t.Errorf("info = %+v", info)
	}
}

func TestNonZeroExitIsFailed(t *testing.T) {
	p := startProc(t, `echo oops >&2; exit 3`, Options{})
	waitDone(t, p)
	info := p.Info()
	if info.State != Failed || info.Exit == nil || info.Exit.Code != 3 || !strings.Contains(info.Err, "code 3") {
		t.Errorf("info = %+v", info)
	}
	if got := lines(p, logs.Stderr); len(got) != 1 || got[0] != "oops" {
		t.Errorf("stderr = %v", got)
	}
}

func TestOutputWithoutTrailingNewlineIsKept(t *testing.T) {
	p := startProc(t, `printf 'no-newline'`, Options{})
	waitDone(t, p)
	if got := lines(p, logs.Stdout); len(got) != 1 || got[0] != "no-newline" {
		t.Errorf("stdout = %v", got)
	}
}

func TestConcurrentOutputIsComplete(t *testing.T) {
	script := `i=0; while [ $i -lt 2000 ]; do echo out$i; echo err$i >&2; i=$((i+1)); done`
	p := startProc(t, script, Options{Logs: logs.NewBuffer(10000)})
	waitDone(t, p)

	out, errs := lines(p, logs.Stdout), lines(p, logs.Stderr)
	if len(out) != 2000 || len(errs) != 2000 {
		t.Fatalf("stdout=%d stderr=%d lines, want 2000 each", len(out), len(errs))
	}
	for i := 0; i < 2000; i++ {
		if out[i] != "out"+strconv.Itoa(i) || errs[i] != "err"+strconv.Itoa(i) {
			t.Fatalf("order broken at %d: %q %q", i, out[i], errs[i])
		}
	}
}

func TestHugeStderrDoesNotBlockStdout(t *testing.T) {
	script := `head -c 1048576 /dev/zero | tr '\0' 'x' >&2; echo done`
	p := startProc(t, script, Options{Logs: logs.NewBuffer(10000)})
	waitDone(t, p)

	total := 0
	for _, l := range lines(p, logs.Stderr) {
		total += len(l)
	}
	if total != 1048576 {
		t.Errorf("stderr bytes = %d, want 1048576", total)
	}
	if got := lines(p, logs.Stdout); len(got) != 1 || got[0] != "done" {
		t.Errorf("stdout = %v", got)
	}
	if p.Info().State != Stopped {
		t.Errorf("state = %s", p.Info().State)
	}
}

func TestStopGraceful(t *testing.T) {
	p := startProc(t, `trap 'echo got-term; exit 0' TERM; echo ready; while true; do sleep 0.1; done`, Options{})
	waitForLine(t, p, "ready")

	stop(t, p, 5*time.Second)

	info := p.Info()
	if info.State != Stopped || info.Exit == nil || info.Exit.Forced || info.Exit.Code != 0 {
		t.Errorf("info = %+v", info)
	}
	if got := lines(p, logs.Stdout); got[len(got)-1] != "got-term" {
		t.Errorf("handler output missing: %v", got)
	}
}

func TestStopForcedWhenSIGTERMIsIgnored(t *testing.T) {
	p := startProc(t, `trap '' TERM; echo ready; while true; do sleep 0.1; done`, Options{})
	waitForLine(t, p, "ready")

	stop(t, p, 300*time.Millisecond)

	info := p.Info()
	if info.State != Stopped || info.Exit == nil || !info.Exit.Forced || info.Exit.Signal != "killed" {
		t.Errorf("info = %+v exit=%+v", info, info.Exit)
	}
}

func TestStopKillsChildProcesses(t *testing.T) {
	p := startProc(t, stubbornChild+` wait`, Options{})
	child := firstStdoutPID(t, p)
	if !testutil.ProcessAlive(child) {
		t.Fatal("child should be alive before Stop")
	}

	stop(t, p, 300*time.Millisecond)

	if testutil.ProcessAlive(child) {
		t.Errorf("child %d survived Stop", child)
	}
	if !p.Info().Exit.Forced {
		t.Error("a SIGTERM-proof child should have required SIGKILL")
	}
}

func TestLeaderCrashKillsLeftoverChildren(t *testing.T) {
	p := startProc(t, stubbornChild+` exit 1`, Options{})
	child := firstStdoutPID(t, p)
	waitDone(t, p)

	if testutil.ProcessAlive(child) {
		t.Errorf("leftover child %d outlived its crashed parent", child)
	}
	if p.Info().State != Failed {
		t.Errorf("state = %s", p.Info().State)
	}
}

func TestContextCancellationStopsProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := Start(ctx, workload(t, `sleep 300`), Options{StopGrace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitDone(t, p)
	if p.Info().State != Stopped {
		t.Errorf("state = %s", p.Info().State)
	}
}

func TestStopIsIdempotentAndConcurrentSafe(t *testing.T) {
	p := startProc(t, `sleep 300`, Options{})

	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			errs <- p.Stop(ctx, time.Second)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Stop: %v", err)
		}
	}
	if p.Info().State != Stopped {
		t.Errorf("state = %s", p.Info().State)
	}
	stop(t, p, time.Second) // already stopped: no error
}

func TestStopHonoursCallerContext(t *testing.T) {
	p := startProc(t, `trap '' TERM; echo ready; while true; do sleep 0.1; done`, Options{})
	waitForLine(t, p, "ready")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.Stop(ctx, 10*time.Second); err == nil {
		t.Error("expected the caller's deadline to surface")
	}
	// Termination continues in the background and the cleanup finishes it.
}

func TestStateTransitions(t *testing.T) {
	p := startProc(t, `trap 'sleep 0.5; exit 0' TERM; echo ready; while true; do sleep 0.1; done`, Options{})
	waitForLine(t, p, "ready")
	if got := p.Info().State; got != Running {
		t.Fatalf("state = %s, want running", got)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = p.Stop(ctx, 10*time.Second)
	}()

	sawStopping := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := p.Info().State
		if st == Stopping {
			sawStopping = true
		}
		if st.Terminal() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawStopping {
		t.Error("never observed the stopping state")
	}
	waitDone(t, p)
	if info := p.Info(); info.State != Stopped || info.StoppedAt.IsZero() {
		t.Errorf("final info = %+v", info)
	}
}

func TestStartFailures(t *testing.T) {
	good := workload(t, "true")

	badProgram := good
	badProgram.Start = spec.Command{Program: "shipr-no-such-binary"}
	badDir := good
	badDir.Dir = good.Dir + "/missing"
	noStart := good
	noStart.Start = spec.Command{}

	for name, w := range map[string]spec.Workload{"unknown program": badProgram, "missing dir": badDir, "no command": noStart} {
		t.Run(name, func(t *testing.T) {
			p, err := Start(context.Background(), w, Options{})
			if err == nil || p != nil {
				t.Fatalf("p=%v err=%v, want an error", p, err)
			}
			if !strings.Contains(err.Error(), "starting test") {
				t.Errorf("error lacks context: %v", err)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Start(ctx, good, Options{}); err == nil {
		t.Error("a cancelled context should not start a process")
	}
}
