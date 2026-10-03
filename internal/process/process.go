// Package process runs one local OS process in its own process group and
// makes it observable (state, PID, timestamps, exit info, logs) and stoppable
// without callers knowing anything about OS processes.
package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"shipr/internal/logs"
	"shipr/internal/procgroup"
	"shipr/internal/spec"
)

// State is the lifecycle state of a process.
type State string

const (
	Starting State = "starting"
	Running  State = "running"
	Stopping State = "stopping"
	Stopped  State = "stopped" // exited after a stop request, or on its own with code 0
	Failed   State = "failed"  // failed to start, crashed, or could not be stopped
)

func (s State) Terminal() bool { return s == Stopped || s == Failed }

// Exit describes how the process ended.
type Exit struct {
	Code   int    // exit code; -1 when killed by a signal
	Signal string // e.g. "terminated", "killed"; empty for a normal exit
	Forced bool   // SIGKILL was needed to clear the process group
}

// Info is a plain-value snapshot, safe to hand to a UI.
type Info struct {
	App       string
	PID       int
	State     State
	StartedAt time.Time
	StoppedAt time.Time // zero until terminal
	Exit      *Exit     // nil until the process has exited
	Err       string    // reason when State is Failed
}

// Options tune a process. Zero values pick sensible defaults.
type Options struct {
	Logs         *logs.Buffer  // nil creates a new buffer
	StopGrace    time.Duration // default grace for stops and context cancellation (5s)
	DrainTimeout time.Duration // how long to wait for trailing output (2s)
}

func (o Options) withDefaults() Options {
	if o.Logs == nil {
		o.Logs = logs.NewBuffer(0)
	}
	if o.StopGrace <= 0 {
		o.StopGrace = 5 * time.Second
	}
	if o.DrainTimeout <= 0 {
		o.DrainTimeout = 2 * time.Second
	}
	return o
}

const (
	pollInterval = 10 * time.Millisecond
	killTimeout  = 5 * time.Second
	readBufSize  = 16 * 1024
)

// Process is one running (or finished) application process.
type Process struct {
	w      spec.Workload
	opts   Options
	cmd    *exec.Cmd
	pgid   int
	logs   *logs.Buffer
	stopCh chan time.Duration
	done   chan struct{}

	mu          sync.Mutex
	info        Info
	stopFailure string
}

// Start launches the workload as the leader of a new process group. If ctx is
// cancelled the process is stopped gracefully, like Stop.
func Start(ctx context.Context, w spec.Workload, opts Options) (*Process, error) {
	opts = opts.withDefaults()
	name := w.Name()

	if err := w.Validate(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}

	// Own the pipes so Wait never depends on grandchildren closing them.
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("starting %s: creating stdout pipe: %w", name, err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, fmt.Errorf("starting %s: creating stderr pipe: %w", name, err)
	}

	cmd := exec.Command(w.Start.Program, w.Start.Args...)
	cmd.Dir = w.Dir
	cmd.Env = append(os.Environ(), w.EnvList()...)
	cmd.Stdout = outW
	cmd.Stderr = errW
	procgroup.Configure(cmd)

	startErr := cmd.Start()
	outW.Close() // the child holds its own copies now (or never will)
	errW.Close()
	if startErr != nil {
		outR.Close()
		errR.Close()
		return nil, fmt.Errorf("starting %s: %w", name, startErr)
	}

	p := &Process{
		w:      w,
		opts:   opts,
		cmd:    cmd,
		pgid:   cmd.Process.Pid,
		logs:   opts.Logs,
		stopCh: make(chan time.Duration, 1),
		done:   make(chan struct{}),
		info: Info{
			App:       name,
			PID:       cmd.Process.Pid,
			State:     Running,
			StartedAt: time.Now(),
		},
	}
	go p.supervise(ctx, outR, errR)
	return p, nil
}

// Info returns a snapshot of the process state.
func (p *Process) Info() Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.info
	if i.Exit != nil {
		e := *i.Exit
		i.Exit = &e
	}
	return i
}

// Logs returns the captured output. It is separate from process state.
func (p *Process) Logs() *logs.Buffer { return p.logs }

// Done is closed once the process, its children and its output are finished.
func (p *Process) Done() <-chan struct{} { return p.done }

// Stop terminates the whole process group: SIGTERM first, SIGKILL after grace
// (grace <= 0 uses the configured default). It returns once the process has
// finished, or when ctx ends; termination continues in the background either
// way. Stopping a finished process is a no-op. Safe for concurrent use.
func (p *Process) Stop(ctx context.Context, grace time.Duration) error {
	select {
	case p.stopCh <- grace:
	default: // a stop is already pending or in progress
	}
	select {
	case <-p.done:
		p.mu.Lock()
		failure := p.stopFailure
		p.mu.Unlock()
		if failure != "" {
			return fmt.Errorf("stopping %s: %s", p.w.Name(), failure)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for %s to stop: %w", p.w.Name(), ctx.Err())
	}
}

type outcome struct {
	exited bool
	forced bool
	note   string
}

// supervise is the single owner of the process lifecycle after Start.
func (p *Process) supervise(ctx context.Context, outR, errR *os.File) {
	defer close(p.done)

	var pumps sync.WaitGroup
	pumps.Add(2)
	go func() { defer pumps.Done(); pump(outR, p.logs, logs.Stdout) }()
	go func() { defer pumps.Done(); pump(errR, p.logs, logs.Stderr) }()

	waitCh := make(chan error, 1)
	go func() { waitCh <- p.cmd.Wait() }()

	var (
		exited    bool
		requested bool
		grace     = p.opts.StopGrace
	)
	select {
	case <-waitCh:
		exited = true
		select { // a stop request that raced with a natural exit still counts
		case g := <-p.stopCh:
			requested, grace = true, p.graceOr(g)
		default:
		}
	case g := <-p.stopCh:
		requested, grace = true, p.graceOr(g)
	case <-ctx.Done():
		requested = true
	}

	if requested {
		p.markStopping()
		_ = procgroup.Signal(p.pgid, syscall.SIGTERM)
	} else {
		// The leader ended by itself. Anything it left behind is not allowed
		// to outlive it, so there is no grace for leftovers.
		grace = 0
	}

	out := p.awaitGroup(waitCh, exited, grace)
	p.drain(&pumps, outR, errR)

	var exit *Exit
	if out.exited {
		exit = exitInfo(p.cmd.ProcessState, out.forced)
	}
	p.finish(exit, requested, out.note)
}

// awaitGroup waits for the leader to exit and the whole group to vanish. When
// grace expires first it SIGKILLs the group.
func (p *Process) awaitGroup(waitCh <-chan error, exited bool, grace time.Duration) outcome {
	out := outcome{exited: exited}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()

	for {
		if out.exited && !procgroup.Alive(p.pgid) {
			return out
		}
		select {
		case <-waitCh:
			out.exited = true
			waitCh = nil
		case <-tick.C:
		case <-timer.C:
			out.forced = true
			_ = procgroup.Signal(p.pgid, syscall.SIGKILL)
			if !out.exited {
				select {
				case <-waitCh:
					out.exited = true
				case <-time.After(killTimeout):
					out.note = "process did not exit after SIGKILL"
					return out
				}
			}
			deadline := time.Now().Add(killTimeout)
			for procgroup.Alive(p.pgid) && time.Now().Before(deadline) {
				time.Sleep(pollInterval)
			}
			if procgroup.Alive(p.pgid) {
				out.note = "child processes survived SIGKILL"
			}
			return out
		}
	}
}

// drain lets the readers finish trailing output, then closes the read ends so
// no goroutine can leak.
func (p *Process) drain(pumps *sync.WaitGroup, outR, errR *os.File) {
	finished := make(chan struct{})
	go func() { pumps.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(p.opts.DrainTimeout):
	}
	outR.Close()
	errR.Close()
	<-finished
}

func (p *Process) markStopping() {
	p.mu.Lock()
	if p.info.State == Running {
		p.info.State = Stopping
	}
	p.mu.Unlock()
}

func (p *Process) finish(exit *Exit, requested bool, note string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.info.StoppedAt = time.Now()
	p.info.Exit = exit
	switch {
	case note != "":
		p.info.State = Failed
		p.info.Err = note
		if requested {
			p.stopFailure = note
		}
	case requested:
		p.info.State = Stopped
	case exit != nil && exit.Code == 0 && exit.Signal == "":
		p.info.State = Stopped
	default:
		p.info.State = Failed
		p.info.Err = describeExit(exit)
	}
}

func (p *Process) graceOr(g time.Duration) time.Duration {
	if g <= 0 {
		return p.opts.StopGrace
	}
	return g
}

func exitInfo(ps *os.ProcessState, forced bool) *Exit {
	if ps == nil {
		return nil
	}
	e := &Exit{Code: ps.ExitCode(), Forced: forced}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		e.Signal = ws.Signal().String()
	}
	return e
}

func describeExit(e *Exit) string {
	switch {
	case e == nil:
		return "process ended unexpectedly"
	case e.Signal != "":
		return "terminated by signal: " + e.Signal
	default:
		return fmt.Sprintf("exited with code %d", e.Code)
	}
}

// pump copies lines from r into buf until EOF or the reader is closed. Lines
// longer than the read buffer are split into several entries so a reader can
// never stall (and block the child) on an oversized line.
func pump(r io.Reader, buf *logs.Buffer, stream logs.Stream) {
	br := bufio.NewReaderSize(r, readBufSize)
	for {
		line, _, err := br.ReadLine()
		if err != nil {
			return
		}
		buf.Append(stream, string(line))
	}
}
