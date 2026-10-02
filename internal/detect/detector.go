package detect

import (
	"path"
	"regexp"
	"strconv"

	"deployer/internal/app"
)

const (
	sourceDefault = "framework default"
	sourceScript  = "package.json script"
)

// FS is the minimal file-tree view detectors need.
// archive.Archive satisfies it; tests can use a fake.
type FS interface {
	Has(path string) bool
	IsDir(path string) bool
	Read(path string) ([]byte, error)
	Children(dir string) []string
}

// Detector recognises one kind of application in one directory.
type Detector interface {
	Name() string
	Detect(fsys FS, dir string) (*app.Application, bool)
}

// Default returns detectors in priority order: the first match per directory wins.
func Default() []Detector {
	return []Detector{
		Go{},
		Python{},
		Node{},
		Docker{}, // fallback: a Dockerfile with no recognised runtime
	}
}

// Scan runs detectors over every directory and returns all apps found.
func Scan(fsys FS, dirs []string, detectors []Detector) []app.Application {
	var found []app.Application

	for _, dir := range dirs {
		for _, d := range detectors {
			a, ok := d.Detect(fsys, dir)
			if !ok {
				continue
			}
			a.Root = dir
			a.HasDockerfile = fsys.Has(join(dir, "Dockerfile"))
			if a.HasDockerfile && a.Runtime != app.RuntimeDocker {
				a.Markers = append(a.Markers, "Dockerfile")
			}
			refinePort(fsys, a)
			found = append(found, *a)
			break // one app per directory
		}
	}
	return found
}

// ---- shared helpers ----

func join(dir, name string) string { return path.Join(dir, name) }

var (
	exposeRe     = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d{2,5})`)
	envPortRe    = regexp.MustCompile(`(?m)^\s*PORT\s*=\s*["']?(\d{2,5})`)
	scriptPortRe = regexp.MustCompile(`(?:^|\s)(?:--port|-p|PORT=)[ =]?(\d{2,5})`)
)

// refinePort upgrades a guessed port using stronger evidence from the project.
func refinePort(fsys FS, a *app.Application) {
	if p := portFromFile(fsys, join(a.Root, "Dockerfile"), exposeRe); p != 0 {
		a.Port, a.PortSource = p, "Dockerfile EXPOSE"
		return
	}
	if a.PortSource == sourceScript {
		return
	}
	for _, env := range []string{".env", ".env.example"} {
		if p := portFromFile(fsys, join(a.Root, env), envPortRe); p != 0 {
			a.Port, a.PortSource = p, env+" PORT"
			return
		}
	}
}

func portFromFile(fsys FS, p string, re *regexp.Regexp) int {
	if !fsys.Has(p) {
		return 0
	}
	data, err := fsys.Read(p)
	if err != nil {
		return 0
	}
	return firstPort(re, string(data))
}

func firstPort(re *regexp.Regexp, text string) int {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}