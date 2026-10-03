package detect

import (
	"regexp"
	"strings"

	"shipr/internal/app"
)

type Python struct{}

func (Python) Name() string { return "python" }

var (
	pythonManifests    = []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"}
	pythonEntrypoints  = []string{"main.py", "app.py", "wsgi.py", "asgi.py", "server.py"}
	pythonTokenPattern = regexp.MustCompile(`[a-z0-9][a-z0-9._-]*`)
)

type pythonFramework struct {
	dep  string
	name string
	role app.Role
	port int
}

var pythonFrameworks = []pythonFramework{
	{"django", "Django", app.RoleBackend, 8000},
	{"fastapi", "FastAPI", app.RoleBackend, 8000},
	{"flask", "Flask", app.RoleBackend, 5000},
	{"streamlit", "Streamlit", app.RoleFullstack, 8501},
}

func (Python) Detect(fsys FS, dir string) (*app.Application, error) {
	var (
		manifests []string
		pyproject string
		readErr   error
	)
	tokens := map[string]struct{}{}

	for _, m := range pythonManifests {
		p := join(dir, m)
		if !fsys.Has(p) {
			continue
		}
		manifests = append(manifests, m)
		data, err := fsys.Read(p)
		if err != nil {
			if readErr == nil {
				readErr = err
			}
			continue
		}
		text := string(data)
		if m == "pyproject.toml" {
			pyproject = text
		}
		addTokens(tokens, text)
	}
	hasManage := fsys.Has(join(dir, "manage.py"))
	if len(manifests) == 0 && !hasManage {
		return nil, nil
	}

	fw := matchPythonFramework(tokens, hasManage)
	entry := firstExisting(fsys, dir, pythonEntrypoints)

	// A manifest alone is weak; require a framework or an entrypoint.
	var conf app.Confidence
	switch {
	case fw != nil && len(manifests) > 0:
		conf = app.ConfidenceHigh
	case fw != nil:
		conf = app.ConfidenceMedium
	case entry != "" && len(manifests) > 0:
		conf = app.ConfidenceLow
	default:
		return nil, readErr
	}

	a := &app.Application{
		Runtime:    app.RuntimePython,
		Role:       app.RoleUnknown,
		Confidence: conf,
	}
	for _, m := range manifests {
		a.AddEvidence(app.EvidenceFile, m)
	}
	if hasManage {
		a.AddEvidence(app.EvidenceFile, "manage.py")
	}
	if fw != nil {
		if _, ok := tokens[fw.dep]; ok {
			a.AddEvidence(app.EvidenceDependency, fw.name)
		}
		a.Framework = fw.name
		a.Role = fw.role
		a.AddPort(fw.port, app.PortDefault)
	} else {
		a.AddEvidence(app.EvidenceFile, entry)
	}
	a.PackageManager = detectPythonPM(fsys, dir, a, manifests, pyproject)
	return a, readErr
}

// addTokens collects lowercase identifier-like words from a manifest, so
// "flask>=2" yields "flask" but "flask-cors" does not.
func addTokens(set map[string]struct{}, text string) {
	for _, line := range strings.Split(strings.ToLower(text), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		for _, tok := range pythonTokenPattern.FindAllString(line, -1) {
			set[tok] = struct{}{}
		}
	}
}

func matchPythonFramework(tokens map[string]struct{}, hasManage bool) *pythonFramework {
	for i := range pythonFrameworks {
		fw := &pythonFrameworks[i]
		if _, ok := tokens[fw.dep]; ok || (fw.dep == "django" && hasManage) {
			return fw
		}
	}
	return nil
}

func firstExisting(fsys FS, dir string, names []string) string {
	for _, n := range names {
		if fsys.Has(join(dir, n)) {
			return n
		}
	}
	return ""
}

func detectPythonPM(fsys FS, dir string, a *app.Application, manifests []string, pyproject string) app.PackageManager {
	switch {
	case fsys.Has(join(dir, "uv.lock")):
		a.AddEvidence(app.EvidenceFile, "uv.lock")
		return app.PMUv
	case fsys.Has(join(dir, "poetry.lock")):
		a.AddEvidence(app.EvidenceFile, "poetry.lock")
		return app.PMPoetry
	case strings.Contains(pyproject, "[tool.poetry]"):
		return app.PMPoetry
	case contains(manifests, "Pipfile"):
		return app.PMPipenv
	case len(manifests) > 0:
		return app.PMPip
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
