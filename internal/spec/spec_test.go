package spec

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"shipr/internal/app"
)

func nodeApp() app.Application {
	a := app.Application{
		Name: "web", Root: "web", Runtime: app.RuntimeNode,
		PackageManager: app.PMNpm, StartCmd: "npm start", BuildCmd: "npm run build",
	}
	a.AddEvidence(app.EvidenceScript, "start")
	a.AddPort(3000, app.PortScript)
	return a
}

func workspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestResolveDetected(t *testing.T) {
	ws := workspace(t)
	w, err := Resolve(nodeApp(), ws, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Name() != "web" || w.Dir != filepath.Join(ws, "web") {
		t.Errorf("identity/dir wrong: %q %q", w.Name(), w.Dir)
	}
	want := Command{Program: "npm", Args: []string{"start"}, Why: `"start" script found during inspection`}
	if !reflect.DeepEqual(w.Start, want) {
		t.Errorf("Start = %+v, want %+v", w.Start, want)
	}
	if w.Port == nil || w.Port.Number != 3000 || w.Port.Why != "detected from package script" {
		t.Errorf("Port = %+v", w.Port)
	}
	if got := w.EnvList(); !reflect.DeepEqual(got, []string{"PORT=3000"}) {
		t.Errorf("EnvList = %v", got)
	}
	if w.Env[0].Why == "" {
		t.Error("env var has no provenance")
	}
}

func TestResolveMarksDefaultPortUnconfirmed(t *testing.T) {
	a := nodeApp()
	a.Ports = nil
	a.AddPort(4321, app.PortDefault)
	w, err := Resolve(a, workspace(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Port.Why, "unconfirmed") {
		t.Errorf("Why = %q", w.Port.Why)
	}
}

func TestResolveOverrides(t *testing.T) {
	w, err := Resolve(nodeApp(), workspace(t), Options{
		Start: "node server.js",
		Port:  9000,
		Env:   map[string]string{"ZED": "1", "A": "2", "PORT": "1234"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.Start.String() != "node server.js" || w.Start.Why != "explicit override" {
		t.Errorf("Start = %+v", w.Start)
	}
	if w.Port.Number != 9000 || w.Port.Why != "explicit override" {
		t.Errorf("Port = %+v", w.Port)
	}
	// Explicit env wins over the derived PORT, and output is sorted.
	if got := w.EnvList(); !reflect.DeepEqual(got, []string{"A=2", "PORT=1234", "ZED=1"}) {
		t.Errorf("EnvList = %v", got)
	}
}

func TestResolveNoStartCommand(t *testing.T) {
	a := nodeApp()
	a.StartCmd = ""
	if _, err := Resolve(a, workspace(t), Options{}); !errors.Is(err, ErrNoStartCommand) {
		t.Errorf("err = %v, want ErrNoStartCommand", err)
	}
}

func TestResolveRejectsBadInput(t *testing.T) {
	ws := workspace(t)
	if err := os.WriteFile(filepath.Join(ws, "afile"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(a *app.Application, o *Options){
		"root escapes":      func(a *app.Application, _ *Options) { a.Root = "../outside" },
		"root missing":      func(a *app.Application, _ *Options) { a.Root = "nope" },
		"root is a file":    func(a *app.Application, _ *Options) { a.Root = "afile" },
		"shell syntax":      func(_ *app.Application, o *Options) { o.Start = "npm start && rm -rf /" },
		"port out of range": func(_ *app.Application, o *Options) { o.Port = 70000 },
		"bad env name":      func(_ *app.Application, o *Options) { o.Env = map[string]string{"A=B": "x"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a, o := nodeApp(), Options{}
			mutate(&a, &o)
			if _, err := Resolve(a, ws, o); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestWorkDirRootIsWorkspace(t *testing.T) {
	ws := t.TempDir()
	got, err := WorkDir(ws, "")
	if err != nil || got != ws {
		t.Errorf("WorkDir = %q, %v", got, err)
	}
}

func TestParseCommand(t *testing.T) {
	c, err := ParseCommand("  go build -o app . ", "why")
	if err != nil {
		t.Fatal(err)
	}
	if c.Program != "go" || !reflect.DeepEqual(c.Args, []string{"build", "-o", "app", "."}) || c.Why != "why" {
		t.Errorf("c = %+v", c)
	}
	if c.String() != "go build -o app ." {
		t.Errorf("String = %q", c.String())
	}
	for _, bad := range []string{"", "   ", `echo "hi"`, "a | b", "echo $HOME", "a; b"} {
		if _, err := ParseCommand(bad, ""); err == nil {
			t.Errorf("ParseCommand(%q) should fail", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	good := Workload{
		Source: app.Application{Name: "x"},
		Dir:    dir,
		Start:  Command{Program: "sh"},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("good workload rejected: %v", err)
	}
	cases := map[string]func(w *Workload){
		"no name":      func(w *Workload) { w.Source.Name = "" },
		"relative dir": func(w *Workload) { w.Dir = "rel" },
		"missing dir":  func(w *Workload) { w.Dir = filepath.Join(dir, "gone") },
		"no start":     func(w *Workload) { w.Start = Command{} },
		"bad port":     func(w *Workload) { w.Port = &Port{Number: 0} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := good
			mutate(&w)
			if err := w.Validate(); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	ws := workspace(t)
	opts := Options{Env: map[string]string{"B": "1", "A": "2", "C": "3"}}
	first, err := Resolve(nodeApp(), ws, opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		again, _ := Resolve(nodeApp(), ws, opts)
		if !reflect.DeepEqual(first, again) {
			t.Fatal("Resolve is not deterministic")
		}
	}
}
