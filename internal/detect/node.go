package detect

import (
	"encoding/json"
	"path"

	"deployer/internal/app"
)

type Node struct{}

func (Node) Name() string { return "node" }

type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type nodeFramework struct {
	dep  string
	name string
	role app.Role
	port int
}

// Order = priority for the "primary" framework label.
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

func (Node) Detect(fsys FS, dir string) (*app.Application, bool) {
	pkgPath := join(dir, "package.json")
	if !fsys.Has(pkgPath) {
		return nil, false
	}

	a := &app.Application{
		Runtime: app.RuntimeNode,
		Role:    app.RoleUnknown,
		Markers: []string{"package.json"},
	}

	pm, lock := packageManager(fsys, dir)
	if lock != "" {
		a.Markers = append(a.Markers, lock)
	}

	data, err := fsys.Read(pkgPath)
	if err != nil {
		return a, true
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return a, true // still a Node project, just unreadable manifest
	}

	if pkg.Name != "" {
		a.Name = path.Base(pkg.Name) // "@scope/name" -> "name"
	}

	applyNodeFramework(a, mergeDeps(pkg))
	if a.Framework == "" {
		a.Port, a.PortSource = 3000, sourceDefault
	}

	if _, ok := pkg.Scripts["build"]; ok {
		a.BuildCmd = runScript(pm, "build")
	}
	if _, ok := pkg.Scripts["start"]; ok {
		a.StartCmd = runScript(pm, "start")
	}
	if p := scriptPort(pkg.Scripts); p != 0 {
		a.Port, a.PortSource = p, sourceScript
	}
	return a, true
}

func applyNodeFramework(a *app.Application, deps map[string]struct{}) {
	var frontend, backend bool

	for _, fw := range nodeFrameworks {
		if _, ok := deps[fw.dep]; !ok {
			continue
		}
		if a.Framework == "" {
			a.Framework = fw.name
			a.Port = fw.port
			a.PortSource = sourceDefault
		}
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

func mergeDeps(pkg packageJSON) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range pkg.Dependencies {
		out[k] = struct{}{}
	}
	for k := range pkg.DevDependencies {
		out[k] = struct{}{}
	}
	return out
}

func scriptPort(scripts map[string]string) int {
	for _, name := range []string{"start", "dev", "preview", "serve"} {
		if p := firstPort(scriptPortRe, scripts[name]); p != 0 {
			return p
		}
	}
	return 0
}

func packageManager(fsys FS, dir string) (pm, lockfile string) {
	switch {
	case fsys.Has(join(dir, "pnpm-lock.yaml")):
		return "pnpm", "pnpm-lock.yaml"
	case fsys.Has(join(dir, "yarn.lock")):
		return "yarn", "yarn.lock"
	case fsys.Has(join(dir, "bun.lockb")):
		return "bun", "bun.lockb"
	case fsys.Has(join(dir, "package-lock.json")):
		return "npm", "package-lock.json"
	}
	return "npm", ""
}

func runScript(pm, script string) string {
	if pm == "npm" && script == "start" {
		return "npm start"
	}
	return pm + " run " + script
}