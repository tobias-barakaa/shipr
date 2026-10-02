package detect

import (
	"path"
	"strings"

	"deployer/internal/app"
)

type Go struct{}

func (Go) Name() string { return "go" }

var goFrameworks = []struct{ module, name string }{
	{"github.com/gin-gonic/gin", "Gin"},
	{"github.com/labstack/echo", "Echo"},
	{"github.com/gofiber/fiber", "Fiber"},
	{"github.com/go-chi/chi", "Chi"},
}

func (Go) Detect(fsys FS, dir string) (*app.Application, bool) {
	modFile := join(dir, "go.mod")
	if !fsys.Has(modFile) {
		return nil, false
	}
	data, err := fsys.Read(modFile)
	if err != nil {
		return nil, false
	}
	text := string(data)

	a := &app.Application{
		Name:       goModuleName(text),
		Runtime:    app.RuntimeGo,
		Role:       app.RoleBackend,
		Port:       8080,
		PortSource: sourceDefault,
		Markers:    []string{"go.mod"},
	}
	if fsys.Has(join(dir, "go.sum")) {
		a.Markers = append(a.Markers, "go.sum")
	}

	for _, fw := range goFrameworks {
		if strings.Contains(text, fw.module) {
			a.Framework = fw.name
			break
		}
	}

	target := goMainTarget(fsys, dir)
	if target == "" {
		// No main package: probably a library, so no port or start command.
		a.BuildCmd = "go build ./..."
		a.Port, a.PortSource = 0, ""
	} else {
		a.BuildCmd = "go build -o app " + target
		a.StartCmd = "./app"
	}
	return a, true
}

func goModuleName(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "module ")), `"`)
			return path.Base(mod)
		}
	}
	return ""
}

// goMainTarget finds where the main package lives: "." or "./cmd/<name>".
func goMainTarget(fsys FS, dir string) string {
	if fsys.Has(join(dir, "main.go")) {
		return "."
	}
	cmdDir := join(dir, "cmd")
	for _, child := range fsys.Children(cmdDir) {
		if fsys.IsDir(join(cmdDir, child)) {
			return "./cmd/" + child
		}
	}
	return ""
}