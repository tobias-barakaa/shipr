package build

import (
	"fmt"

	"shipr/internal/app"
	"shipr/internal/spec"
)

// Python installs dependencies using the project's own conventions.
type Python struct{}

func (Python) Name() string { return "python" }

func (Python) Supports(a app.Application) bool { return a.Runtime == app.RuntimePython }

func (Python) Plan(a app.Application) ([]spec.Command, error) {
	switch a.PackageManager {
	case app.PMPoetry:
		return []spec.Command{{Program: "poetry", Args: []string{"install", "--no-root"}, Why: "poetry detected (pyproject.toml/poetry.lock)"}}, nil
	case app.PMPipenv:
		return []spec.Command{{Program: "pipenv", Args: []string{"install"}, Why: "Pipfile present"}}, nil
	case app.PMUv:
		return []spec.Command{{Program: "uv", Args: []string{"sync"}, Why: "uv.lock present"}}, nil
	case app.PMPip:
		return pipPlan(a), nil
	}
	return nil, fmt.Errorf("unsupported Python package manager %q", a.PackageManager)
}

// pipPlan installs into an isolated .venv inside the workspace. With nothing
// to install there is nothing to build.
func pipPlan(a app.Application) []spec.Command {
	var install spec.Command
	switch {
	case hasFile(a, "requirements.txt"):
		install = spec.Command{
			Program: ".venv/bin/pip", Args: []string{"install", "-r", "requirements.txt"},
			Why: "requirements.txt present",
		}
	case hasFile(a, "pyproject.toml"), hasFile(a, "setup.py"):
		install = spec.Command{
			Program: ".venv/bin/pip", Args: []string{"install", "."},
			Why: "installable project (pyproject.toml/setup.py) without requirements.txt",
		}
	default:
		return nil
	}
	venv := spec.Command{
		Program: "python3", Args: []string{"-m", "venv", ".venv"},
		Why: "isolate dependencies from the system Python",
	}
	return []spec.Command{venv, install}
}
