// Package build turns a detected Application into build steps and runs them
// in the application's workspace. Runtime-specific logic lives only in the
// Builder implementations.
package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"shipr/internal/app"
	"shipr/internal/spec"
)

var ErrUnsupported = errors.New("unsupported application")

// Builder knows how to build one kind of application. Plan is pure: it only
// derives commands, so it is testable without running anything.
type Builder interface {
	Name() string
	Supports(a app.Application) bool
	Plan(a app.Application) ([]spec.Command, error)
}

// Default returns the built-in builders.
func Default() []Builder {
	return []Builder{Node{}, Python{}, Go{}}
}

// Invocation is one command execution request. Dir is always set.
type Invocation struct {
	Dir     string
	Command spec.Command
	Env     []string // NAME=value pairs added to the inherited environment
}

// Executor runs one command, streaming combined stdout/stderr to out. It
// returns the exit code (-1 if the command could not start) and a non-nil
// error for any failure. Tests replace it with a fake.
type Executor interface {
	Run(ctx context.Context, inv Invocation, out io.Writer) (exitCode int, err error)
}

// StepResult records one executed step.
type StepResult struct {
	Command   spec.Command
	Dir       string
	ExitCode  int
	Output    string // combined stdout+stderr (tail, capped)
	Truncated bool
	Duration  time.Duration
	Err       error
}

// Result is the outcome of building one application.
type Result struct {
	App      string
	Builder  string
	Dir      string
	Steps    []StepResult
	Duration time.Duration
}

// Success reports whether every executed step succeeded.
func (r Result) Success() bool {
	for _, s := range r.Steps {
		if s.Err != nil {
			return false
		}
	}
	return true
}

// StepError describes a failed build step.
type StepError struct {
	Command  spec.Command
	ExitCode int
	Err      error
}

func (e *StepError) Error() string {
	if e.ExitCode >= 0 {
		return fmt.Sprintf("build step %q failed with exit code %d: %v", e.Command.String(), e.ExitCode, e.Err)
	}
	return fmt.Sprintf("build step %q could not run: %v", e.Command.String(), e.Err)
}

func (e *StepError) Unwrap() error { return e.Err }

// Engine selects a builder and runs its steps.
type Engine struct {
	Builders  []Builder
	Executor  Executor
	Env       []string  // extra environment for every step
	Log       io.Writer // optional live output (commands and their output)
	MaxOutput int       // bytes of output kept per step; default 1 MiB
}

func NewEngine() *Engine {
	return &Engine{Builders: Default(), Executor: OSExecutor{}}
}

// Build builds a in its directory inside workspace. The error is nil only if
// every step succeeded; the Result is always populated as far as it got.
func (e *Engine) Build(ctx context.Context, a app.Application, workspace string) (Result, error) {
	res := Result{App: a.Name}

	b := e.pick(a)
	if b == nil {
		return res, fmt.Errorf("%w: no builder for %s runtime (%s)", ErrUnsupported, a.Runtime, a.Name)
	}
	res.Builder = b.Name()

	dir, err := spec.WorkDir(workspace, a.Root)
	if err != nil {
		return res, fmt.Errorf("building %s: %w", a.Name, err)
	}
	res.Dir = dir

	steps, err := b.Plan(a)
	if err != nil {
		return res, fmt.Errorf("planning build for %s: %w", a.Name, err)
	}

	maxOut := e.MaxOutput
	if maxOut <= 0 {
		maxOut = 1 << 20
	}

	started := time.Now()
	defer func() { res.Duration = time.Since(started) }()

	for _, cmd := range steps {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("building %s: %w", a.Name, err)
		}
		if e.Log != nil {
			fmt.Fprintf(e.Log, "$ %s  (in %s)\n", cmd.String(), dir)
		}

		capture := &tailBuffer{max: maxOut}
		var out io.Writer = capture
		if e.Log != nil {
			out = io.MultiWriter(capture, e.Log)
		}

		t0 := time.Now()
		code, runErr := e.Executor.Run(ctx, Invocation{Dir: dir, Command: cmd, Env: e.Env}, out)
		output, truncated := capture.result()
		res.Steps = append(res.Steps, StepResult{
			Command: cmd, Dir: dir, ExitCode: code, Output: output,
			Truncated: truncated, Duration: time.Since(t0), Err: runErr,
		})
		if runErr != nil {
			return res, &StepError{Command: cmd, ExitCode: code, Err: runErr}
		}
	}
	return res, nil
}

func (e *Engine) pick(a app.Application) Builder {
	for _, b := range e.Builders {
		if b.Supports(a) {
			return b
		}
	}
	return nil
}

// tailBuffer keeps the last max bytes written. It never fails a write, so a
// chatty build can't be killed by a full buffer.
type tailBuffer struct {
	mu        sync.Mutex
	max       int
	buf       []byte
	truncated bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > 2*b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
		b.truncated = true
	}
	return len(p), nil
}

func (b *tailBuffer) result() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, truncated := b.buf, b.truncated
	if len(data) > b.max {
		data, truncated = data[len(data)-b.max:], true
	}
	return string(bytes.Clone(data)), truncated
}
