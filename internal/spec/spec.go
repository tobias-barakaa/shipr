// Package spec is the execution vocabulary shared by the build engine and the
// local runner. It describes what is needed to BUILD and RUN an application,
// while package app describes what the application IS.
package spec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"shipr/internal/app"
)

var ErrNoStartCommand = errors.New("no start command detected; provide one explicitly")

// Command is a program plus arguments. It is never run through a shell.
type Command struct {
	Program string
	Args    []string
	Why     string // provenance: why Shipr chose this command
}

const shellSyntax = "|&;<>`$\"'\\"

// ParseCommand splits a simple command line. Shell syntax is rejected rather
// than guessed at.
func ParseCommand(line, why string) (Command, error) {
	if strings.ContainsAny(line, shellSyntax) {
		return Command{}, fmt.Errorf("command %q uses shell syntax, which is not supported", line)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Command{}, errors.New("empty command")
	}
	return Command{
		Program: fields[0],
		Args:    append([]string(nil), fields[1:]...),
		Why:     why,
	}, nil
}

func (c Command) String() string {
	return strings.Join(append([]string{c.Program}, c.Args...), " ")
}

func (c Command) IsZero() bool { return c.Program == "" }

// EnvVar is one environment variable for local execution.
type EnvVar struct {
	Name  string
	Value string
	Why   string
}

// Port is the port the application is expected to listen on.
type Port struct {
	Number int
	Why    string
}

// Workload is a fully resolved, runtime-independent description of how to
// start one application locally. The runner needs nothing else.
type Workload struct {
	Source app.Application // detection facts: identity, runtime, framework, package manager
	Dir    string          // absolute working directory
	Start  Command
	Env    []EnvVar
	Port   *Port // nil when unknown
}

// Name is the application identity.
func (w Workload) Name() string { return w.Source.Name }

// EnvList returns NAME=value pairs sorted by name.
func (w Workload) EnvList() []string {
	vars := append([]EnvVar(nil), w.Env...)
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
	out := make([]string, len(vars))
	for i, v := range vars {
		out[i] = v.Name + "=" + v.Value
	}
	return out
}

// Validate checks that the workload is runnable.
func (w Workload) Validate() error {
	if w.Source.Name == "" {
		return errors.New("workload has no application name")
	}
	if !filepath.IsAbs(w.Dir) {
		return fmt.Errorf("working directory %q is not absolute", w.Dir)
	}
	info, err := os.Stat(w.Dir)
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory %q is not a directory", w.Dir)
	}
	if w.Start.IsZero() {
		return errors.New("workload has no start command")
	}
	for _, e := range w.Env {
		if !validEnvName(e.Name) {
			return fmt.Errorf("invalid environment variable name %q", e.Name)
		}
	}
	if w.Port != nil && (w.Port.Number < 1 || w.Port.Number > 65535) {
		return fmt.Errorf("invalid port %d", w.Port.Number)
	}
	return nil
}

func validEnvName(n string) bool {
	return n != "" && !strings.ContainsAny(n, "=\x00")
}

// WorkDir maps an application root (relative to the archive) onto an unpacked
// workspace, refusing roots that escape it.
func WorkDir(workspace, root string) (string, error) {
	if workspace == "" {
		return "", errors.New("workspace directory is empty")
	}
	ws, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	rel := filepath.Clean(filepath.FromSlash(root))
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("application root %q escapes the workspace", root)
	}
	dir := filepath.Join(ws, rel)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("application directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("application directory %q is not a directory", dir)
	}
	return dir, nil
}

// Options are explicit overrides applied on top of detected values.
type Options struct {
	Start string            // replaces the detected start command
	Port  int               // replaces the detected port
	Env   map[string]string // extra environment variables
}

// Resolve turns a detected Application into a runnable Workload. Every
// decision records why it was made.
func Resolve(a app.Application, workspace string, opts Options) (Workload, error) {
	dir, err := WorkDir(workspace, a.Root)
	if err != nil {
		return Workload{}, fmt.Errorf("resolving %s: %w", a.Name, err)
	}

	var start Command
	switch {
	case opts.Start != "":
		start, err = ParseCommand(opts.Start, "explicit override")
	case a.StartCmd != "":
		start, err = ParseCommand(a.StartCmd, startWhy(a))
	default:
		return Workload{}, fmt.Errorf("resolving %s: %w", a.Name, ErrNoStartCommand)
	}
	if err != nil {
		return Workload{}, fmt.Errorf("resolving %s: start command: %w", a.Name, err)
	}

	w := Workload{Source: a, Dir: dir, Start: start, Port: resolvePort(a, opts)}

	env := map[string]EnvVar{}
	if w.Port != nil {
		env["PORT"] = EnvVar{
			Name:  "PORT",
			Value: strconv.Itoa(w.Port.Number),
			Why:   "selected port: " + w.Port.Why,
		}
	}
	for name, value := range opts.Env {
		env[name] = EnvVar{Name: name, Value: value, Why: "explicit override"}
	}
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w.Env = append(w.Env, env[n])
	}

	if err := w.Validate(); err != nil {
		return Workload{}, fmt.Errorf("resolving %s: %w", a.Name, err)
	}
	return w, nil
}

func startWhy(a app.Application) string {
	for _, e := range a.Evidence {
		if e.Kind == app.EvidenceScript && e.Detail == "start" {
			return `"start" script found during inspection`
		}
	}
	return "start command inferred during inspection"
}

func resolvePort(a app.Application, opts Options) *Port {
	if opts.Port != 0 {
		return &Port{Number: opts.Port, Why: "explicit override"}
	}
	sel, ok := a.Port()
	if !ok {
		return nil
	}
	why := "detected from " + string(sel.Source)
	if sel.Source.IsDefault() {
		why += " (unconfirmed)"
	}
	return &Port{Number: sel.Number, Why: why}
}
