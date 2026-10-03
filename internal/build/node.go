package build

import (
	"fmt"

	"shipr/internal/app"
	"shipr/internal/spec"
)

// Node builds Node.js applications with the detected package manager.
type Node struct{}

func (Node) Name() string { return "node" }

func (Node) Supports(a app.Application) bool { return a.Runtime == app.RuntimeNode }

func (Node) Plan(a app.Application) ([]spec.Command, error) {
	install, err := nodeInstall(a)
	if err != nil {
		return nil, err
	}
	steps := []spec.Command{install}
	if a.BuildCmd != "" {
		c, err := spec.ParseCommand(a.BuildCmd, `"build" script found during inspection`)
		if err != nil {
			return nil, fmt.Errorf("build command: %w", err)
		}
		steps = append(steps, c)
	}
	return steps, nil
}

func nodeInstall(a app.Application) (spec.Command, error) {
	switch a.PackageManager {
	case app.PMNpm:
		if hasFile(a, "package-lock.json") {
			return spec.Command{Program: "npm", Args: []string{"ci"}, Why: "package-lock.json present: reproducible install"}, nil
		}
		return spec.Command{Program: "npm", Args: []string{"install"}, Why: "npm detected, no lockfile found"}, nil
	case app.PMPnpm:
		return spec.Command{Program: "pnpm", Args: []string{"install"}, Why: "pnpm detected (pnpm-lock.yaml or packageManager field)"}, nil
	case app.PMYarn:
		return spec.Command{Program: "yarn", Args: []string{"install"}, Why: "yarn detected (yarn.lock or packageManager field)"}, nil
	case app.PMBun:
		return spec.Command{Program: "bun", Args: []string{"install"}, Why: "bun detected (bun lockfile or packageManager field)"}, nil
	}
	return spec.Command{}, fmt.Errorf("unsupported Node.js package manager %q", a.PackageManager)
}

func hasFile(a app.Application, name string) bool {
	for _, e := range a.Evidence {
		if e.Kind == app.EvidenceFile && e.Detail == name {
			return true
		}
	}
	return false
}
