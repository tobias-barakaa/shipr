package detect

import (
	"fmt"
	"path"
	"strings"

	"shipr/internal/app"
)

type Go struct{}

func (Go) Name() string { return "go" }

var goFrameworks = []struct{ module, name string }{
	{"github.com/gin-gonic/gin", "Gin"},
	{"github.com/labstack/echo", "Echo"},
	{"github.com/gofiber/fiber", "Fiber"},
	{"github.com/go-chi/chi", "Chi"},
}

type goMain struct {
	target string // go build target, e.g. "." or "./cmd/server"
	file   string // path relative to the module root
}

func (Go) Detect(fsys FS, dir string) (*app.Application, error) {
	modPath := join(dir, "go.mod")
	if !fsys.Has(modPath) {
		return nil, nil
	}
	// A module without a main package is a library, not an application.
	mains := goMainPackages(fsys, dir)
	if len(mains) == 0 {
		return nil, nil
	}
	data, err := fsys.Read(modPath)
	if err != nil {
		return nil, err
	}
	text := string(data)

	a := &app.Application{
		Name:           goModuleName(text),
		Runtime:        app.RuntimeGo,
		Role:           app.RoleBackend,
		PackageManager: app.PMGo,
		Confidence:     app.ConfidenceHigh,
	}
	a.AddEvidence(app.EvidenceFile, "go.mod")
	if fsys.Has(join(dir, "go.sum")) {
		a.AddEvidence(app.EvidenceFile, "go.sum")
	}

	primary := mains[0]
	a.AddEvidence(app.EvidenceFile, primary.file)
	if len(mains) > 1 {
		a.AddEvidence(app.EvidenceNote, fmt.Sprintf("%d main packages found; using %s", len(mains), primary.target))
	}
	for _, fw := range goFrameworks {
		if strings.Contains(text, fw.module) {
			a.Framework = fw.name
			a.AddEvidence(app.EvidenceDependency, fw.name)
			break
		}
	}

	// Go has no manifest-declared commands, so these are inferred conventions.
	a.BuildCmd = "go build -o app " + primary.target
	a.StartCmd = "./app"
	a.AddPort(8080, app.PortConvention)
	return a, nil
}

func goMainPackages(fsys FS, dir string) []goMain {
	var out []goMain
	if fsys.Has(join(dir, "main.go")) {
		out = append(out, goMain{target: ".", file: "main.go"})
	}
	for _, c := range fsys.Children(join(dir, "cmd")) {
		f := path.Join("cmd", c, "main.go")
		if fsys.Has(join(dir, f)) {
			out = append(out, goMain{target: "./cmd/" + c, file: f})
		}
	}
	return out
}

func goModuleName(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), `"`)
			if mod == "" {
				return ""
			}
			return path.Base(mod)
		}
	}
	return ""
}