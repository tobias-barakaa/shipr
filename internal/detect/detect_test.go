package detect

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"shipr/internal/app"
	"shipr/internal/archive"
	"shipr/internal/testutil"
)

const (
	astroPkg   = `{"name":"zelisline","scripts":{"dev":"astro dev","build":"astro build"},"dependencies":{"astro":"^4.0.0"}}`
	expressPkg = `{"name":"js-api","scripts":{"start":"node server.js"},"dependencies":{"express":"^4.18.0"}}`
)

func scan(t *testing.T, files map[string]string) Result {
	t.Helper()
	b := testutil.Zip(t, files)
	arc, err := archive.New(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	return Scan(arc, Default())
}

func single(t *testing.T, files map[string]string) app.Application {
	t.Helper()
	res := scan(t, files)
	if len(res.Apps) != 1 {
		t.Fatalf("want 1 app, got %d: %+v", len(res.Apps), res.Apps)
	}
	return res.Apps[0]
}

func hasEvidence(a app.Application, kind app.EvidenceKind, detail string) bool {
	return containsEvidence(a.Evidence, app.Evidence{Kind: kind, Detail: detail})
}

func containsEvidence(list []app.Evidence, want app.Evidence) bool {
	for _, e := range list {
		if e == want {
			return true
		}
	}
	return false
}

func roots(res Result) []string {
	var out []string
	for _, a := range res.Apps {
		out = append(out, a.Root)
	}
	return out
}

func TestAstro(t *testing.T) {
	a := single(t, map[string]string{"package.json": astroPkg, "package-lock.json": "{}"})

	if a.Runtime != app.RuntimeNode || a.Framework != "Astro" || a.Role != app.RoleFrontend {
		t.Errorf("identity wrong: %+v", a)
	}
	if a.PackageManager != app.PMNpm || a.Confidence != app.ConfidenceHigh || a.Root != "" {
		t.Errorf("metadata wrong: %+v", a)
	}
	if a.BuildCmd != "npm run build" || a.StartCmd != "" {
		t.Errorf("commands wrong: build=%q start=%q", a.BuildCmd, a.StartCmd)
	}
	if p, ok := a.Port(); !ok || p.Number != 4321 || p.Source != app.PortDefault {
		t.Errorf("port = %+v", p)
	}
	for _, e := range []app.Evidence{
		{Kind: app.EvidenceFile, Detail: "package.json"},
		{Kind: app.EvidenceFile, Detail: "package-lock.json"},
		{Kind: app.EvidenceDependency, Detail: "Astro"},
	} {
		if !containsEvidence(a.Evidence, e) {
			t.Errorf("missing evidence %+v", e)
		}
	}
}

func TestExpress(t *testing.T) {
	a := single(t, map[string]string{"package.json": expressPkg})
	if a.Framework != "Express" || a.Role != app.RoleBackend {
		t.Errorf("identity wrong: %+v", a)
	}
	if a.StartCmd != "npm start" {
		t.Errorf("start = %q", a.StartCmd)
	}
	if p, _ := a.Port(); p.Number != 3000 {
		t.Errorf("port = %+v", p)
	}
	if !hasEvidence(a, app.EvidenceNote, "no lockfile found; assuming npm") {
		t.Error("missing no-lockfile note")
	}
}

func TestNodePackageManagers(t *testing.T) {
	cases := []struct {
		lock      string
		pm        app.PackageManager
		wantBuild string
	}{
		{"pnpm-lock.yaml", app.PMPnpm, "pnpm run build"},
		{"yarn.lock", app.PMYarn, "yarn run build"},
		{"bun.lockb", app.PMBun, "bun run build"},
		{"package-lock.json", app.PMNpm, "npm run build"},
	}
	for _, c := range cases {
		t.Run(c.lock, func(t *testing.T) {
			a := single(t, map[string]string{"package.json": astroPkg, c.lock: ""})
			if a.PackageManager != c.pm || a.BuildCmd != c.wantBuild {
				t.Errorf("pm=%q build=%q", a.PackageManager, a.BuildCmd)
			}
			if !hasEvidence(a, app.EvidenceFile, c.lock) {
				t.Error("lockfile evidence missing")
			}
		})
	}
}

func TestPackageManagerField(t *testing.T) {
	pkg := `{"packageManager":"pnpm@9.1.0","scripts":{"build":"astro build"},"dependencies":{"astro":"4"}}`
	a := single(t, map[string]string{"package.json": pkg})
	if a.PackageManager != app.PMPnpm {
		t.Errorf("pm = %q", a.PackageManager)
	}
}

func TestFlask(t *testing.T) {
	a := single(t, map[string]string{"requirements.txt": "flask==3.0.0\nrequests\n"})
	if a.Runtime != app.RuntimePython || a.Framework != "Flask" || a.Role != app.RoleBackend {
		t.Errorf("identity wrong: %+v", a)
	}
	if a.PackageManager != app.PMPip || a.Confidence != app.ConfidenceHigh {
		t.Errorf("metadata wrong: %+v", a)
	}
	if p, _ := a.Port(); p.Number != 5000 || p.Source != app.PortDefault {
		t.Errorf("port = %+v", p)
	}
}

func TestDjangoViaManagePy(t *testing.T) {
	a := single(t, map[string]string{"manage.py": "#!/usr/bin/env python\n", "requirements.txt": "django>=4\n"})
	if a.Framework != "Django" {
		t.Errorf("framework = %q", a.Framework)
	}
	if p, _ := a.Port(); p.Number != 8000 {
		t.Errorf("port = %+v", p)
	}
}

func TestPythonPoetry(t *testing.T) {
	a := single(t, map[string]string{
		"pyproject.toml": "[tool.poetry]\nname=\"x\"\n[tool.poetry.dependencies]\nfastapi=\"^0.110\"\n",
	})
	if a.Framework != "FastAPI" || a.PackageManager != app.PMPoetry {
		t.Errorf("got framework=%q pm=%q", a.Framework, a.PackageManager)
	}
}

func TestPythonEntrypointWithoutFramework(t *testing.T) {
	a := single(t, map[string]string{"requirements.txt": "requests\n", "app.py": "print('hi')"})
	if a.Framework != "" || a.Confidence != app.ConfidenceLow {
		t.Errorf("got framework=%q confidence=%v", a.Framework, a.Confidence)
	}
	if _, ok := a.Port(); ok {
		t.Error("unknown framework should not guess a port")
	}
}

func TestGoApplication(t *testing.T) {
	a := single(t, map[string]string{
		"go.mod":  "module example.com/api\n\ngo 1.21\n\nrequire github.com/gin-gonic/gin v1.9.0\n",
		"go.sum":  "",
		"main.go": "package main\n",
	})
	if a.Name != "api" || a.Runtime != app.RuntimeGo || a.Framework != "Gin" || a.Role != app.RoleBackend {
		t.Errorf("identity wrong: %+v", a)
	}
	if a.PackageManager != app.PMGo || a.BuildCmd != "go build -o app ." {
		t.Errorf("pm=%q build=%q", a.PackageManager, a.BuildCmd)
	}
	if p, _ := a.Port(); p.Number != 8080 || p.Source != app.PortConvention {
		t.Errorf("port = %+v", p)
	}
}

func TestGoCmdLayout(t *testing.T) {
	a := single(t, map[string]string{"go.mod": "module x\n", "cmd/server/main.go": "package main\n"})
	if a.BuildCmd != "go build -o app ./cmd/server" {
		t.Errorf("build = %q", a.BuildCmd)
	}
	if !hasEvidence(a, app.EvidenceFile, "cmd/server/main.go") {
		t.Error("main package evidence missing")
	}
}

func TestGoMultipleMains(t *testing.T) {
	a := single(t, map[string]string{
		"go.mod": "module x\n", "main.go": "package main\n", "cmd/tool/main.go": "package main\n",
	})
	if !hasEvidence(a, app.EvidenceNote, "2 main packages found; using .") {
		t.Errorf("missing multi-main note: %+v", a.Evidence)
	}
}

func TestDockerfileOnly(t *testing.T) {
	a := single(t, map[string]string{"Dockerfile": "FROM nginx\nEXPOSE 8080\n"})
	if a.Runtime != app.RuntimeDocker || !a.HasDockerfile || a.Confidence != app.ConfidenceLow {
		t.Errorf("got %+v", a)
	}
	if p, _ := a.Port(); p.Number != 8080 || p.Source != app.PortDockerfile {
		t.Errorf("port = %+v", p)
	}
}

func TestDockerfileDoesNotReplaceIdentity(t *testing.T) {
	a := single(t, map[string]string{"package.json": expressPkg, "Dockerfile": "FROM node\n"})
	if a.Runtime != app.RuntimeNode || a.Framework != "Express" {
		t.Errorf("identity lost: %+v", a)
	}
	if !a.HasDockerfile || !hasEvidence(a, app.EvidenceFile, "Dockerfile") {
		t.Error("Dockerfile fact lost")
	}
}

func TestWeakMarkersAreNotApplications(t *testing.T) {
	cases := map[string]map[string]string{
		"devDependencies only": {"package.json": `{"devDependencies":{"prettier":"3"}}`},
		"empty package.json":   {"package.json": `{}`},
		"workspace root":       {"package.json": `{"workspaces":["packages/*"],"scripts":{"build":"turbo build"}}`},
		"go library":           {"go.mod": "module x\n", "lib.go": "package x\n"},
		"flask-cors only":      {"requirements.txt": "flask-cors\n"},
		"bare requirements":    {"requirements.txt": "requests\n"},
		"malformed package":    {"package.json": `{not json`},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if res := scan(t, files); len(res.Apps) != 0 {
				t.Errorf("unexpected apps: %+v", res.Apps)
			}
		})
	}
}

func TestMalformedManifestBecomesWarning(t *testing.T) {
	res := scan(t, map[string]string{"package.json": `{not json`})
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "package.json") {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestAuxiliaryDirectoriesIgnoredWhenRealAppExists(t *testing.T) {
	res := scan(t, map[string]string{
		"package.json":                    astroPkg,
		"examples/demo/package.json":      expressPkg,
		"tests/fixtures/requirements.txt": "flask\n",
	})
	if got, want := roots(res), []string{""}; !reflect.DeepEqual(got, want) {
		t.Errorf("roots = %v, want %v", got, want)
	}
}

func TestAuxiliaryFallback(t *testing.T) {
	res := scan(t, map[string]string{"examples/demo/package.json": expressPkg})
	if got, want := roots(res), []string{"examples/demo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("roots = %v, want %v", got, want)
	}
}

func TestWorkspacePackagesAreApplications(t *testing.T) {
	res := scan(t, map[string]string{
		"package.json":              `{"workspaces":["packages/*"],"scripts":{"build":"turbo build"}}`,
		"packages/web/package.json": astroPkg,
	})
	if got, want := roots(res), []string{"packages/web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("roots = %v, want %v", got, want)
	}
}

func TestFrontendPlusBackend(t *testing.T) {
	res := scan(t, map[string]string{
		"frontend/package.json":      astroPkg,
		"frontend/package-lock.json": "{}",
		"backend/go.mod":             "module example.com/api\n\ngo 1.21\n",
		"backend/main.go":            "package main\n",
	})
	if len(res.Apps) != 2 {
		t.Fatalf("want 2 apps, got %+v", res.Apps)
	}
	be, fe := res.Apps[0], res.Apps[1]
	if be.Root != "backend" || be.Runtime != app.RuntimeGo || be.Role != app.RoleBackend {
		t.Errorf("backend wrong: %+v", be)
	}
	if fe.Root != "frontend" || fe.Runtime != app.RuntimeNode || fe.Role != app.RoleFrontend {
		t.Errorf("frontend wrong: %+v", fe)
	}
}

func TestMultipleBackends(t *testing.T) {
	res := scan(t, map[string]string{
		"backend-js/package.json":     expressPkg,
		"backend-py/requirements.txt": "flask\n",
	})
	if got, want := roots(res), []string{"backend-js", "backend-py"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	if res.Apps[0].Runtime != app.RuntimeNode || res.Apps[1].Runtime != app.RuntimePython {
		t.Errorf("runtimes wrong: %+v", res.Apps)
	}
}

func TestWrapperFolderAndNestedApps(t *testing.T) {
	res := scan(t, map[string]string{
		"proj/package.json":              astroPkg,
		"proj/services/api/package.json": expressPkg,
	})
	if got, want := roots(res), []string{"proj", "proj/services/api"}; !reflect.DeepEqual(got, want) {
		t.Errorf("roots = %v, want %v", got, want)
	}
}

func TestDetectorConflictIsPreservedAsEvidence(t *testing.T) {
	a := single(t, map[string]string{"package.json": expressPkg, "requirements.txt": "flask\n"})
	if a.Runtime != app.RuntimePython {
		t.Errorf("tie should go to the earlier detector, got %s", a.Runtime)
	}
	if !hasEvidence(a, app.EvidenceNote, "also resembles Node.js") {
		t.Errorf("conflict not recorded: %+v", a.Evidence)
	}
}

func TestScanIsDeterministic(t *testing.T) {
	files := map[string]string{
		"b/package.json":     expressPkg,
		"a/requirements.txt": "flask\n",
		"c/go.mod":           "module c\n",
		"c/main.go":          "package main\n",
		"Dockerfile":         "FROM scratch\nEXPOSE 9000\n",
	}
	first := scan(t, files)
	for i := 0; i < 5; i++ {
		if again := scan(t, files); !reflect.DeepEqual(first, again) {
			t.Fatalf("scan %d differs:\n%+v\n%+v", i, first, again)
		}
	}
	if got, want := roots(first), []string{"", "a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("roots = %v, want %v", got, want)
	}
}
