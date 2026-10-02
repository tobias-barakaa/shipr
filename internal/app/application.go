// Package app defines the Phase 1 output contract: the Project model.
// Later phases consume these types and never need to reopen the archive.
package app

import (
	"fmt"
	"sort"
)

// Runtime is the technology an application runs on.
type Runtime string

const (
	RuntimeNode   Runtime = "Node.js"
	RuntimePython Runtime = "Python"
	RuntimeGo     Runtime = "Go"
	RuntimeDocker Runtime = "Docker"
)

// Role describes where an application sits in a project.
type Role string

const (
	RoleFrontend  Role = "frontend"
	RoleBackend   Role = "backend"
	RoleFullstack Role = "full-stack"
	RoleUnknown   Role = "unknown"
)

// PackageManager is the dependency tool an application uses.
type PackageManager string

const (
	PMNpm    PackageManager = "npm"
	PMPnpm   PackageManager = "pnpm"
	PMYarn   PackageManager = "yarn"
	PMBun    PackageManager = "bun"
	PMPip    PackageManager = "pip"
	PMPoetry PackageManager = "poetry"
	PMPipenv PackageManager = "pipenv"
	PMUv     PackageManager = "uv"
	PMGo     PackageManager = "go modules"
)

// Confidence says how sure a detector is that a directory is an application.
type Confidence int

const (
	ConfidenceLow Confidence = iota + 1
	ConfidenceMedium
	ConfidenceHigh
)

func (c Confidence) String() string {
	switch c {
	case ConfidenceLow:
		return "low"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceHigh:
		return "high"
	}
	return "unknown"
}

// EvidenceKind classifies why Shipr reached a conclusion.
type EvidenceKind string

const (
	EvidenceFile       EvidenceKind = "file"
	EvidenceDependency EvidenceKind = "dependency"
	EvidenceScript     EvidenceKind = "script"
	EvidenceNote       EvidenceKind = "note" // inference, assumption, or conflict
)

// Evidence is one reason behind a detection decision.
type Evidence struct {
	Kind   EvidenceKind
	Detail string
}

func (e Evidence) String() string {
	switch e.Kind {
	case EvidenceDependency:
		return e.Detail + " dependency"
	case EvidenceScript:
		return fmt.Sprintf("%q script", e.Detail)
	}
	return e.Detail
}

// PortSource says where a port candidate came from.
type PortSource string

const (
	PortDockerfile PortSource = "Dockerfile EXPOSE"
	PortScript     PortSource = "package script"
	PortEnv        PortSource = ".env"
	PortEnvExample PortSource = ".env.example"
	PortDefault    PortSource = "framework default"
	PortConvention PortSource = "runtime convention"
)

// rank defines precedence: lower wins.
func (s PortSource) rank() int {
	switch s {
	case PortDockerfile:
		return 0
	case PortScript:
		return 1
	case PortEnv:
		return 2
	case PortEnvExample:
		return 3
	case PortDefault:
		return 4
	}
	return 5
}

// IsDefault reports whether the port is a guess rather than found in the project.
func (s PortSource) IsDefault() bool {
	return s == PortDefault || s == PortConvention
}

// PortCandidate is one port found (or assumed) for an application.
type PortCandidate struct {
	Number int
	Source PortSource
}

// Application is one application discovered inside an archive.
type Application struct {
	Name           string
	Root           string // directory inside the archive; "" is the archive root
	Runtime        Runtime
	Framework      string
	Role           Role
	PackageManager PackageManager
	Confidence     Confidence
	HasDockerfile  bool
	Ports          []PortCandidate // precedence order; first is the selected port
	BuildCmd       string
	StartCmd       string
	Evidence       []Evidence
}

// Project is everything found in one archive.
type Project struct {
	Name     string
	Apps     []Application
	Warnings []string
}

// Location is a display path for the application's folder.
func (a Application) Location() string {
	if a.Root == "" {
		return "/ (archive root)"
	}
	return "/" + a.Root
}

// Port returns the selected port, if any.
func (a Application) Port() (PortCandidate, bool) {
	if len(a.Ports) == 0 {
		return PortCandidate{}, false
	}
	return a.Ports[0], true
}

// PortConflicts returns candidates whose number differs from the selected one.
func (a Application) PortConflicts() []PortCandidate {
	sel, ok := a.Port()
	if !ok {
		return nil
	}
	var out []PortCandidate
	for _, c := range a.Ports[1:] {
		if c.Number != sel.Number {
			out = append(out, c)
		}
	}
	return out
}

// AddEvidence records a reason, ignoring exact duplicates.
func (a *Application) AddEvidence(kind EvidenceKind, detail string) {
	e := Evidence{Kind: kind, Detail: detail}
	for _, x := range a.Evidence {
		if x == e {
			return
		}
	}
	a.Evidence = append(a.Evidence, e)
}

// AddPort records a port candidate, ignoring exact duplicates.
func (a *Application) AddPort(n int, src PortSource) {
	c := PortCandidate{Number: n, Source: src}
	for _, x := range a.Ports {
		if x == c {
			return
		}
	}
	a.Ports = append(a.Ports, c)
}

// NormalizePorts drops guessed ports when real evidence exists and sorts
// candidates by precedence. Call once after all candidates are added.
func (a *Application) NormalizePorts() {
	hasReal := false
	for _, c := range a.Ports {
		if !c.Source.IsDefault() {
			hasReal = true
			break
		}
	}
	var kept []PortCandidate
	for _, c := range a.Ports {
		if hasReal && c.Source.IsDefault() {
			continue
		}
		kept = append(kept, c)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return kept[i].Source.rank() < kept[j].Source.rank()
	})
	a.Ports = kept
}