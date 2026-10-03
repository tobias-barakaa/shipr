package detect

import "shipr/internal/app"

// Docker identifies a directory by its Dockerfile alone. It has low
// confidence, so any real runtime detector for the same directory wins.
type Docker struct{}

func (Docker) Name() string { return "docker" }

func (Docker) Detect(fsys FS, dir string) (*app.Application, error) {
	if !fsys.Has(join(dir, "Dockerfile")) {
		return nil, nil
	}
	a := &app.Application{
		Runtime:    app.RuntimeDocker,
		Role:       app.RoleUnknown,
		Confidence: app.ConfidenceLow,
	}
	a.AddEvidence(app.EvidenceFile, "Dockerfile")
	return a, nil
}
