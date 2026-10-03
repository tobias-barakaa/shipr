package build

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"shipr/internal/app"
)

// fakeExec records invocations instead of running anything.
type fakeExec struct {
	calls  []Invocation
	failOn string // command line that fails; "" means none
	output string
}

func (f *fakeExec) Run(_ context.Context, inv Invocation, out io.Writer) (int, error) {
	f.calls = append(f.calls, inv)
	io.WriteString(out, f.output)
	if f.failOn != "" && inv.Command.String() == f.failOn {
		return 2, errors.New("exit status 2")
	}
	return 0, nil
}

func (f *fakeExec) commands() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.Command.String())
	}
	return out
}

func engine(f *fakeExec) *Engine { return &Engine{Builders: Default(), Executor: f} }

func workspace(t *testing.T, roots ...string) string {
	t.Helper()
	ws := t.TempDir()
	for _, r := range roots {
		if err := os.MkdirAll(filepath.Join(ws, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func nodeApp(pm app.PackageManager, files ...string) app.Application {
	a := app.Application{
		Name: "web", Root: "web", Runtime: app.RuntimeNode,
		PackageManager: pm, BuildCmd: "npm run build",
	}
	for _, f := range files {
		a.AddEvidence(app.EvidenceFile, f)
	}
	return a
}

func TestNodeBuild(t *testing.T) {
	ws := workspace(t, "web")
	f := &fakeExec{}
	res, err := engine(f).Build(context.Background(), nodeApp(app.PMNpm, "package.json", "package-lock.json"), ws)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"npm ci", "npm run build"}; !reflect.DeepEqual(f.commands(), want) {
		t.Errorf("commands = %v, want %v", f.commands(), want)
	}
	if !res.Success() || res.Builder != "node" || len(res.Steps) != 2 {
		t.Errorf("result = %+v", res)
	}
	if res.Steps[0].Command.Why == "" {
		t.Error("install step has no provenance")
	}
}

func TestNodePackageManagers(t *testing.T) {
	cases := []struct {
		name  string
		pm    app.PackageManager
		files []string
		want  string
	}{
		{"npm with lockfile", app.PMNpm, []string{"package-lock.json"}, "npm ci"},
		{"npm without lockfile", app.PMNpm, nil, "npm install"},
		{"pnpm", app.PMPnpm, []string{"pnpm-lock.yaml"}, "pnpm install"},
		{"yarn", app.PMYarn, []string{"yarn.lock"}, "yarn install"},
		{"bun", app.PMBun, []string{"bun.lockb"}, "bun install"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeExec{}
			a := nodeApp(c.pm, c.files...)
			a.BuildCmd = ""
			if _, err := engine(f).Build(context.Background(), a, workspace(t, "web")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.commands(), []string{c.want}) {
				t.Errorf("commands = %v, want [%s]", f.commands(), c.want)
			}
		})
	}
}

func TestNodeUnknownPackageManager(t *testing.T) {
	_, err := engine(&fakeExec{}).Build(context.Background(), nodeApp("", "package.json"), workspace(t, "web"))
	if err == nil || !strings.Contains(err.Error(), "package manager") {
		t.Errorf("err = %v", err)
	}
}

func TestPythonBuilds(t *testing.T) {
	cases := []struct {
		name  string
		pm    app.PackageManager
		files []string
		want  []string
	}{
		{"pip requirements", app.PMPip, []string{"requirements.txt"},
			[]string{"python3 -m venv .venv", ".venv/bin/pip install -r requirements.txt"}},
		{"pip pyproject only", app.PMPip, []string{"pyproject.toml"},
			[]string{"python3 -m venv .venv", ".venv/bin/pip install ."}},
		{"poetry", app.PMPoetry, []string{"pyproject.toml"}, []string{"poetry install --no-root"}},
		{"pipenv", app.PMPipenv, []string{"Pipfile"}, []string{"pipenv install"}},
		{"uv", app.PMUv, []string{"uv.lock"}, []string{"uv sync"}},
		{"nothing to install", app.PMPip, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeExec{}
			a := app.Application{Name: "api", Runtime: app.RuntimePython, PackageManager: c.pm}
			for _, file := range c.files {
				a.AddEvidence(app.EvidenceFile, file)
			}
			res, err := engine(f).Build(context.Background(), a, workspace(t))
			if err != nil || !res.Success() {
				t.Fatalf("Build: %v", err)
			}
			if !reflect.DeepEqual(f.commands(), c.want) {
				t.Errorf("commands = %v, want %v", f.commands(), c.want)
			}
		})
	}
}

func TestGoBuild(t *testing.T) {
	f := &fakeExec{}
	a := app.Application{Name: "api", Runtime: app.RuntimeGo, BuildCmd: "go build -o app ./cmd/server"}
	res, err := engine(f).Build(context.Background(), a, workspace(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"go build -o app ./cmd/server"}; !reflect.DeepEqual(f.commands(), want) {
		t.Errorf("commands = %v", f.commands())
	}
	if res.Builder != "go" {
		t.Errorf("builder = %q", res.Builder)
	}

	a.BuildCmd = ""
	if _, err := engine(&fakeExec{}).Build(context.Background(), a, workspace(t)); err == nil {
		t.Error("Go app without build command should fail")
	}
}

func TestUnsupportedApplication(t *testing.T) {
	f := &fakeExec{}
	a := app.Application{Name: "d", Runtime: app.RuntimeDocker}
	_, err := engine(f).Build(context.Background(), a, workspace(t))
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	if len(f.calls) != 0 {
		t.Error("nothing should run for an unsupported application")
	}
}

func TestBuildFailure(t *testing.T) {
	f := &fakeExec{failOn: "npm run build", output: "boom\n"}
	res, err := engine(f).Build(context.Background(), nodeApp(app.PMNpm, "package-lock.json"), workspace(t, "web"))

	var se *StepError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *StepError", err)
	}
	if se.ExitCode != 2 || se.Command.String() != "npm run build" {
		t.Errorf("StepError = %+v", se)
	}
	if res.Success() || len(res.Steps) != 2 {
		t.Errorf("result = %+v", res)
	}
	if last := res.Steps[1]; last.Err == nil || !strings.Contains(last.Output, "boom") || last.ExitCode != 2 {
		t.Errorf("failing step = %+v", last)
	}
	if !strings.Contains(err.Error(), "exit code 2") {
		t.Errorf("error lacks exit code: %v", err)
	}
}

func TestStepsStopAfterFailure(t *testing.T) {
	f := &fakeExec{failOn: "npm ci"}
	_, err := engine(f).Build(context.Background(), nodeApp(app.PMNpm, "package-lock.json"), workspace(t, "web"))
	if err == nil || len(f.calls) != 1 {
		t.Errorf("err=%v calls=%v", err, f.commands())
	}
}

func TestStepsRunInApplicationDirectory(t *testing.T) {
	ws := workspace(t, "services/api")
	f := &fakeExec{}
	a := nodeApp(app.PMNpm, "package-lock.json")
	a.Root = "services/api"
	res, err := engine(f).Build(context.Background(), a, ws)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws, "services", "api")
	for _, c := range f.calls {
		if c.Dir != want {
			t.Errorf("step ran in %q, want %q", c.Dir, want)
		}
	}
	if res.Dir != want {
		t.Errorf("result dir = %q", res.Dir)
	}
}

func TestInvalidWorkingDirectoryRunsNothing(t *testing.T) {
	cases := map[string]string{"missing": "gone", "escape": "../outside"}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeExec{}
			a := nodeApp(app.PMNpm, "package-lock.json")
			a.Root = root
			if _, err := engine(f).Build(context.Background(), a, workspace(t)); err == nil {
				t.Error("expected an error")
			}
			if len(f.calls) != 0 {
				t.Error("nothing should run without a valid working directory")
			}
		})
	}
}

func TestEnvironmentIsForwarded(t *testing.T) {
	f := &fakeExec{}
	e := engine(f)
	e.Env = []string{"CI=1"}
	if _, err := e.Build(context.Background(), nodeApp(app.PMNpm), workspace(t, "web")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls[0].Env, []string{"CI=1"}) {
		t.Errorf("env = %v", f.calls[0].Env)
	}
}

func TestLiveLogShowsCommandsAndOutput(t *testing.T) {
	var log bytes.Buffer
	f := &fakeExec{output: "hello\n"}
	e := engine(f)
	e.Log = &log
	if _, err := e.Build(context.Background(), nodeApp(app.PMNpm, "package-lock.json"), workspace(t, "web")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$ npm ci", "hello"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("live log missing %q:\n%s", want, log.String())
		}
	}
}

func TestOutputIsCapped(t *testing.T) {
	f := &fakeExec{output: strings.Repeat("x", 3<<20)}
	e := engine(f)
	e.MaxOutput = 1 << 20
	a := nodeApp(app.PMNpm)
	a.BuildCmd = ""
	res, err := e.Build(context.Background(), a, workspace(t, "web"))
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Steps[0]; len(s.Output) > 1<<20 || !s.Truncated {
		t.Errorf("output len=%d truncated=%v", len(s.Output), s.Truncated)
	}
}

func TestCancelledContextRunsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fakeExec{}
	_, err := engine(f).Build(ctx, nodeApp(app.PMNpm), workspace(t, "web"))
	if !errors.Is(err, context.Canceled) || len(f.calls) != 0 {
		t.Errorf("err=%v calls=%d", err, len(f.calls))
	}
}
