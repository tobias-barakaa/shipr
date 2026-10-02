package detect

import (
	"strings"

	"deployer/internal/app"
)

type Python struct{}

func (Python) Name() string { return "python" }

var pythonMarkers = []string{
	"requirements.txt", "pyproject.toml", "Pipfile", "setup.py", "manage.py",
}

func (Python) Detect(fsys FS, dir string) (*app.Application, bool) {
	var markers []string
	var text strings.Builder

	for _, m := range pythonMarkers {
		p := join(dir, m)
		if !fsys.Has(p) {
			continue
		}
		markers = append(markers, m)
		if data, err := fsys.Read(p); err == nil {
			text.WriteString(strings.ToLower(string(data)))
			text.WriteByte('\n')
		}
	}
	if len(markers) == 0 {
		return nil, false
	}

	a := &app.Application{
		Runtime: app.RuntimePython,
		Role:    app.RoleUnknown,
		Markers: markers,
	}
	deps := text.String()

	set := func(name string, role app.Role, port int) {
		a.Framework, a.Role, a.Port, a.PortSource = name, role, port, sourceDefault
	}

	switch {
	case strings.Contains(deps, "django") || fsys.Has(join(dir, "manage.py")):
		set("Django", app.RoleBackend, 8000)
	case strings.Contains(deps, "fastapi"):
		set("FastAPI", app.RoleBackend, 8000)
	case strings.Contains(deps, "flask"):
		set("Flask", app.RoleBackend, 5000)
	case strings.Contains(deps, "streamlit"):
		set("Streamlit", app.RoleFullstack, 8501)
	}
	return a, true
}