// Package detect discovers applications inside a file tree.
package detect

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"shipr/internal/app"
)

// FS is the minimal read-only file tree detectors need.
// archive.Archive satisfies it.
type FS interface {
	Has(path string) bool
	Read(path string) ([]byte, error)
	Children(dir string) []string
	Dirs() []string
}

// Detector recognises one kind of application in one directory.
//
// Detect returns (nil, nil) when dir is not an application of this kind.
// A non-nil error means a file could not be read or parsed; it is reported
// as a warning and never aborts the scan. A detector may return both an
// application and an error.
//
// Detectors must set Runtime, Confidence and Evidence, and may add default
// port candidates. Cross-cutting facts (Dockerfile, .env ports) are added
// afterwards by enrich, not by detectors.
type Detector interface {
	Name() string
	Detect(fsys FS, dir string) (*app.Application, error)
}

// Default returns detectors in priority order. When several detectors match
// the same directory, the highest confidence wins and ties go to the earlier
// detector. Docker is last: it only wins when nothing else is known.
func Default() []Detector {
	return []Detector{
		Go{},
		Python{},
		Node{},
		Docker{},
	}
}

// Result is the outcome of scanning a file tree.
type Result struct {
	Apps     []app.Application
	Warnings []string
}

// Directories whose manifests are usually samples, not the real application.
// They are only scanned when nothing else is found.
var auxiliaryDirs = map[string]bool{
	"docs": true, "doc": true,
	"examples": true, "example": true,
	"samples": true, "sample": true,
	"test": true, "tests": true, "testdata": true,
	"fixtures": true, "fixture": true,
	"__tests__": true, "__fixtures__": true,
	"e2e": true, "benchmarks": true,
}

// Scan finds every application in fsys. Output order is deterministic.
func Scan(fsys FS, detectors []Detector) Result {
	dirs := append([]string(nil), fsys.Dirs()...)
	sort.Strings(dirs)

	var regular, auxiliary []string
	for _, d := range dirs {
		if isAuxiliary(d) {
			auxiliary = append(auxiliary, d)
		} else {
			regular = append(regular, d)
		}
	}

	var res Result
	res.Apps = scanDirs(fsys, regular, detectors, &res.Warnings)
	if len(res.Apps) == 0 {
		res.Apps = scanDirs(fsys, auxiliary, detectors, &res.Warnings)
	}
	return res
}

func scanDirs(fsys FS, dirs []string, detectors []Detector, warnings *[]string) []app.Application {
	var apps []app.Application
	for _, dir := range dirs {
		if a := detectDir(fsys, dir, detectors, warnings); a != nil {
			apps = append(apps, *a)
		}
	}
	return apps
}

func detectDir(fsys FS, dir string, detectors []Detector, warnings *[]string) *app.Application {
	var candidates []*app.Application
	for _, d := range detectors {
		a, err := d.Detect(fsys, dir)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("%s: %s detector: %v", displayDir(dir), d.Name(), err))
		}
		if a != nil {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	// Highest confidence wins; strict ">" keeps detector order on ties.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.Confidence > best.Confidence {
			best = c
		}
	}
	for _, c := range candidates {
		if c != best && c.Runtime != app.RuntimeDocker {
			best.AddEvidence(app.EvidenceNote, "also resembles "+string(c.Runtime))
		}
	}

	best.Root = dir
	enrich(fsys, best, warnings)
	return best
}

// enrich adds facts that apply to any application, whatever its runtime.
func enrich(fsys FS, a *app.Application, warnings *[]string) {
	if fsys.Has(join(a.Root, "Dockerfile")) {
		a.HasDockerfile = true
		a.AddEvidence(app.EvidenceFile, "Dockerfile")
		addPortFrom(fsys, a, "Dockerfile", exposeRe, app.PortDockerfile, warnings)
	}
	addPortFrom(fsys, a, ".env", envPortRe, app.PortEnv, warnings)
	addPortFrom(fsys, a, ".env.example", envPortRe, app.PortEnvExample, warnings)
	a.NormalizePorts()
}

func isAuxiliary(dir string) bool {
	for _, seg := range strings.Split(dir, "/") {
		if auxiliaryDirs[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

func join(dir, name string) string { return path.Join(dir, name) }

func displayDir(dir string) string {
	if dir == "" {
		return "/"
	}
	return "/" + dir
}