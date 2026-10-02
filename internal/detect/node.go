package detect

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"shipr/internal/app"
)

type Node struct{}

func (Node) Name() string { return "node" }

type packageJSON struct {
	Name            string            `json:"name"`
	PackageManager  string            `json:"packageManager"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

type nodeFramework struct {
	dep  string
	name string
	role app.Role
	port int
}

// Order decides the primary framework label when several match.
var nodeFrameworks = []nodeFramework{
	{"astro", "Astro", app.RoleFrontend, 4321},
	{"next", "Next.js", app.RoleFullstack, 3000},
	{"nuxt", "Nuxt", app.RoleFullstack, 3000},
	{"@sveltejs/kit", "SvelteKit", app.RoleFullstack, 5173},
	{"@nestjs/core", "NestJS", app.RoleBackend, 3000},
	{"express", "Express", app.RoleBackend, 3000},
	{"fastify", "Fastify", app.RoleBackend, 3000},
	{"koa", "Koa", app.RoleBackend, 3000},
	{"hono", "Hono", app.RoleBackend, 3000},
	{"react-scripts", "Create React App", app.RoleFrontend, 3000},
	{"vite", "Vite", app.RoleFrontend, 5173},
	{"react", "React", app.RoleFrontend, 3000},
	{"vue", "Vue", app.RoleFrontend, 5173},
	{"svelte", "Svelte", app.RoleFrontend, 5173},
}

func (Node) Detect(fsys FS, dir string) (*app.Application, error) {
	pkgPath := join(dir, "package.json")
	if !fsys.Has(pkgPath) {
		return nil, nil
	}
	data, err := fsys.Read(pkgPath)
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("package.json: invalid JSON: %w", err)
	}

	frameworks := matchNodeFrameworks(pkg)
	_, hasStart := pkg.Scripts["start"]
	_, hasBuild := pkg.Scripts["build"]

	// A package.json alone is a weak marker; require meaningful evidence.
	var conf app.Confidence
	switch {
	case len(frameworks) > 0:
		conf = app.ConfidenceHigh
	case hasStart:
		conf = app.ConfidenceMedium
	case hasBuild:
		conf = app.ConfidenceLow
	default:
		return nil, nil
	}
	// A workspace root only orchestrates; its packages are the applications.
	if hasWorkspaces(pkg) && len(frameworks) == 0 {
		return nil, nil
	}

	a := &app.Application{
		Runtime:    app.RuntimeNode,
		Role:       app.RoleUnknown,
		Confidence: conf,
	}
	a.AddEvidence(app.EvidenceFile, "package.json")
	if pkg.Name != "" {
		a.Name = path.Base(pkg.Name) // "@scope/name" -> "name"
	}

	pm := detectNodePM(fsys, dir, pkg, a)
	a.PackageManager = pm

	for _, fw := range frameworks {
		a.AddEvidence(app.EvidenceDependency, fw.name)
	}
	applyNodeFrameworks(a, frameworks)

	if hasBuild {
		a.BuildCmd = runScript(pm, "build")
		a.AddEvidence(app.EvidenceScript, "build")
	}
	if hasStart {
		a.StartCmd = runScript(pm, "start")
		a.AddEvidence(app.EvidenceScript, "start")
	}

	switch {
	case len(frameworks) > 0:
		a.AddPort(frameworks[0].port, app.PortDefault)
	case hasStart:
		a.AddPort(3000, app.PortConvention)
	}
	for _, name := range []string{"start", "dev", "preview", "serve"} {
		if n := firstValidPort(scriptPortRe, pkg.Scripts[name]); n != 0 {
			a.AddPort(n, app.PortScript)
			break
		}
	}
	return a, nil
}

func matchNodeFrameworks(pkg packageJSON) []nodeFramework {
	var out []nodeFramework
	for _, fw := range nodeFrameworks {
		_, inDeps := pkg.Dependencies[fw.dep]
		_, inDev := pkg.DevDependencies[fw.dep]
		if inDeps || inDev {
			out = append(out, fw)
		}
	}
	return out
}

func applyNodeFrameworks(a *app.Application, fws []nodeFramework) {
	if len(fws) == 0 {
		return
	}
	a.Framework = fws[0].name

	var frontend, backend bool
	for _, fw := range fws {
		switch fw.role {
		case app.RoleFrontend:
			frontend = true
		case app.RoleBackend:
			backend = true
		case app.RoleFullstack:
			frontend, backend = true, true
		}
	}
	switch {
	case frontend && backend:
		a.Role = app.RoleFullstack
	case frontend:
		a.Role = app.RoleFrontend
	case backend:
		a.Role = app.RoleBackend
	}
}

func hasWorkspaces(pkg packageJSON) bool {
	s := strings.TrimSpace(string(pkg.Workspaces))
	return s != "" && s != "null"
}

func detectNodePM(fsys FS, dir string, pkg packageJSON, a *app.Application) app.PackageManager {
	known := map[string]app.PackageManager{
		"npm": app.PMNpm, "pnpm": app.PMPnpm, "yarn": app.PMYarn, "bun": app.PMBun,
	}
	if name, _, _ := strings.Cut(pkg.PackageManager, "@"); name != "" {
		if pm, ok := known[name]; ok {
			a.AddEvidence(app.EvidenceNote, "packageManager field: "+name)
			return pm
		}
	}

	lockfiles := []struct {
		file string
		pm   app.PackageManager
	}{
		{"pnpm-lock.yaml", app.PMPnpm},
		{"yarn.lock", app.PMYarn},
		{"bun.lockb", app.PMBun},
		{"bun.lock", app.PMBun},
		{"package-lock.json", app.PMNpm},
	}
	for _, l := range lockfiles {
		if fsys.Has(join(dir, l.file)) {
			a.AddEvidence(app.EvidenceFile, l.file)
			return l.pm
		}
	}

	a.AddEvidence(app.EvidenceNote, "no lockfile found; assuming npm")
	return app.PMNpm
}

func runScript(pm app.PackageManager, script string) string {
	if pm == app.PMNpm && script == "start" {
		return "npm start"
	}
	return string(pm) + " run " + script
}