package detect

import "deployer/internal/app"

// Docker is the fallback for directories with a Dockerfile but no
// recognised language. It must stay last in Default().
type Docker struct{}

func (Docker) Name() string { return "docker" }

func (Docker) Detect(fsys FS, dir string) (*app.Application, bool) {
	if !fsys.Has(join(dir, "Dockerfile")) {
		return nil, false
	}
	return &app.Application{
		Runtime: app.RuntimeDocker,
		Role:    app.RoleUnknown,
		Markers: []string{"Dockerfile"},
	}, true
}