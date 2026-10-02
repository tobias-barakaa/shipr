// Package report renders a Project for humans. It never touches the archive.
package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"shipr/internal/app"
)

type Printer struct {
	w io.Writer
}

func NewPrinter(w io.Writer) *Printer { return &Printer{w: w} }

func (p *Printer) Print(proj *app.Project) {
	p.linef("Project: %s", clean(proj.Name))
	if len(proj.Apps) == 0 {
		p.linef("No applications detected.")
	} else {
		p.linef("Applications found: %d", len(proj.Apps))
		for i := range proj.Apps {
			p.printApp(i+1, proj.Apps[i])
		}
	}

	warnings := append([]string(nil), proj.Warnings...)
	warnings = append(warnings, portClashes(proj.Apps)...)
	if len(warnings) > 0 {
		p.linef("")
		p.linef("Warnings:")
		for _, w := range warnings {
			p.linef("  ! %s", clean(w))
		}
	}
}

func (p *Printer) printApp(n int, a app.Application) {
	p.linef("")
	p.linef("[%d] %s", n, clean(a.Name))
	p.linef("")

	p.row("Location", a.Location())
	p.row("Runtime", string(a.Runtime))
	p.row("Framework", a.Framework)
	p.row("Role", string(a.Role))
	p.row("Confidence", a.Confidence.String())
	p.row("Package", string(a.PackageManager))

	if sel, ok := a.Port(); ok {
		text := fmt.Sprintf("%d (%s)", sel.Number, sel.Source)
		if sel.Source.IsDefault() {
			text = fmt.Sprintf("%d (%s, unconfirmed)", sel.Number, sel.Source)
		}
		p.row("Port", text)
		if conflicts := a.PortConflicts(); len(conflicts) > 0 {
			p.row("Also found", joinPorts(conflicts))
		}
	}
	p.row("Build", a.BuildCmd)
	p.row("Start", a.StartCmd)

	if len(a.Evidence) > 0 {
		p.linef("")
		p.linef("Evidence:")
		for _, e := range a.Evidence {
			mark := "✓"
			if e.Kind == app.EvidenceNote {
				mark = "~"
			}
			p.linef("  %s %s", mark, clean(e.String()))
		}
	}
}

func (p *Printer) linef(format string, args ...any) {
	fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *Printer) row(label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(p.w, "%-12s%s\n", label+":", clean(value))
}

func joinPorts(cs []app.PortCandidate) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%d (%s)", c.Number, c.Source)
	}
	return strings.Join(parts, ", ")
}

// portClashes reports applications whose selected ports collide.
func portClashes(apps []app.Application) []string {
	byPort := map[int][]string{}
	for _, a := range apps {
		if sel, ok := a.Port(); ok {
			byPort[sel.Number] = append(byPort[sel.Number], a.Name)
		}
	}
	var ports []int
	for n, names := range byPort {
		if len(names) > 1 {
			ports = append(ports, n)
		}
	}
	sort.Ints(ports)

	var out []string
	for _, n := range ports {
		out = append(out, fmt.Sprintf("port %d is used by %s", n, strings.Join(byPort[n], ", ")))
	}
	return out
}

// clean replaces control characters so archive-supplied text (package names,
// file names) cannot inject terminal escape sequences.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}