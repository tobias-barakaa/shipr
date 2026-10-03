package build

import (
	"errors"
	"fmt"

	"shipr/internal/app"
	"shipr/internal/spec"
)

// Go builds Go applications with the normal `go build` workflow, using the
// build command inferred during inspection.
type Go struct{}

func (Go) Name() string { return "go" }

func (Go) Supports(a app.Application) bool { return a.Runtime == app.RuntimeGo }

func (Go) Plan(a app.Application) ([]spec.Command, error) {
	if a.BuildCmd == "" {
		return nil, errors.New("no build command detected for Go application")
	}
	c, err := spec.ParseCommand(a.BuildCmd, "main package found during inspection")
	if err != nil {
		return nil, fmt.Errorf("build command: %w", err)
	}
	return []spec.Command{c}, nil
}
