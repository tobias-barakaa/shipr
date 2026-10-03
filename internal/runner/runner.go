// Package runner owns the local lifecycle of applications: start, stop and
// restart by application name, with no duplicate instances.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"shipr/internal/logs"
	"shipr/internal/process"
	"shipr/internal/spec"
)

var (
	ErrAlreadyRunning = errors.New("application is already running")
	ErrNotFound       = errors.New("application is not managed by this runner")
)

// Options configure a LocalRunner. Zero values pick defaults.
type Options struct {
	StopGrace    time.Duration // SIGTERM to SIGKILL grace period (default 10s)
	LogLines     int           // lines of log kept per application
	DrainTimeout time.Duration // wait for trailing output after exit
}

func (o Options) withDefaults() Options {
	if o.StopGrace <= 0 {
		o.StopGrace = 10 * time.Second
	}
	return o
}

// LocalRunner manages applications on this machine, keyed by application name.
type LocalRunner struct {
	opts Options

	mu   sync.Mutex
	apps map[string]*entry
}

type entry struct {
	opMu sync.Mutex // serialises lifecycle operations for this application

	mu       sync.Mutex // guards the fields below
	workload spec.Workload
	logs     *logs.Buffer
	proc     *process.Process
	starting bool
	failed   process.Info // last failed start attempt
}

func New(opts Options) *LocalRunner {
	return &LocalRunner{opts: opts.withDefaults(), apps: map[string]*entry{}}
}

// Start launches w. It fails with ErrAlreadyRunning if that application is
// running. ctx only bounds the start itself; the process outlives it. Use
// Stop or StopAll to end processes.
func (r *LocalRunner) Start(ctx context.Context, w spec.Workload) error {
	name := w.Name()
	if name == "" {
		return errors.New("starting application: workload has no name")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	e := r.entryFor(name)
	e.opMu.Lock()
	defer e.opMu.Unlock()
	return r.launch(e, w)
}

// Stop terminates the application's whole process group and waits for it.
// Stopping an application that is already stopped is not an error.
func (r *LocalRunner) Stop(ctx context.Context, name string) error {
	e, ok := r.lookup(name)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	return r.stop(ctx, e)
}

// Restart stops the application if it is running, waits for it to exit and
// starts it again with the same workload.
func (r *LocalRunner) Restart(ctx context.Context, name string) error {
	e, ok := r.lookup(name)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()

	if err := r.stop(ctx, e); err != nil {
		return fmt.Errorf("restarting %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("restarting %s: %w", name, err)
	}
	e.mu.Lock()
	w := e.workload
	e.mu.Unlock()
	return r.launch(e, w)
}

// StopAll stops every managed application and reports all failures.
func (r *LocalRunner) StopAll(ctx context.Context) error {
	var errs []error
	for _, name := range r.names() {
		if err := r.Stop(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Info returns the current state of an application.
func (r *LocalRunner) Info(name string) (process.Info, bool) {
	e, ok := r.lookup(name)
	if !ok {
		return process.Info{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.starting:
		return process.Info{App: name, State: process.Starting}, true
	case e.proc != nil:
		return e.proc.Info(), true
	}
	return e.failed, true
}

// Logs returns the application's log buffer. It persists across restarts.
func (r *LocalRunner) Logs(name string) (*logs.Buffer, bool) {
	e, ok := r.lookup(name)
	if !ok {
		return nil, false
	}
	return e.logs, true
}

// List returns the state of every managed application, sorted by name.
func (r *LocalRunner) List() []process.Info {
	var out []process.Info
	for _, name := range r.names() {
		if info, ok := r.Info(name); ok {
			out = append(out, info)
		}
	}
	return out
}

func (r *LocalRunner) launch(e *entry, w spec.Workload) error {
	name := w.Name()

	e.mu.Lock()
	if e.proc != nil && !e.proc.Info().State.Terminal() {
		e.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, name)
	}
	e.workload = w
	e.starting = true
	e.mu.Unlock()

	p, err := process.Start(context.Background(), w, process.Options{
		Logs:         e.logs,
		StopGrace:    r.opts.StopGrace,
		DrainTimeout: r.opts.DrainTimeout,
	})

	e.mu.Lock()
	defer e.mu.Unlock()
	e.starting = false
	if err != nil {
		e.proc = nil
		e.failed = process.Info{App: name, State: process.Failed, StoppedAt: time.Now(), Err: err.Error()}
		return err
	}
	e.proc = p
	e.failed = process.Info{}
	return nil
}

func (r *LocalRunner) stop(ctx context.Context, e *entry) error {
	e.mu.Lock()
	p := e.proc
	e.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Stop(ctx, r.opts.StopGrace)
}

func (r *LocalRunner) entryFor(name string) *entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.apps[name]
	if !ok {
		e = &entry{logs: logs.NewBuffer(r.opts.LogLines)}
		r.apps[name] = e
	}
	return e
}

func (r *LocalRunner) lookup(name string) (*entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.apps[name]
	return e, ok
}

func (r *LocalRunner) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.apps))
	for n := range r.apps {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
