package report

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"deployer/internal/app"
)

type Printer struct {
	w io.Writer
}

func NewPrinter(w io.Writer) *Printer { return &Printer{w: w} }

func (p *Printer) Print(proj *app.Project) {
	fmt.Fprintf(p.w, "Project: %s\n", proj.Name)

	if len(proj.Apps) == 0 {
		fmt.Fprintln(p.w, "\nNo deployable application detected.")
		return
	}

	fmt.Fprintf(p.w, "Applications found: %d (%s)\n", len(proj.Apps), describeLayout(proj.Apps))

	for i, a := range proj.Apps {
		p.printApp(i+1, a)
	}

	if conflicts := portConflicts(proj.Apps); len(conflicts) > 0 {
		fmt.Fprintln(p.w, "\nWarnings")
		fmt.Fprintln(p.w, "────────")
		for _, c := range conflicts {
			fmt.Fprintf(p.w, "  ! %s\n", c)
		}
	}
}

func (p *Printer) printApp(n int, a app.Application) {
	title := fmt.Sprintf("[%d] %s", n, a.Name)
	fmt.Fprintf(p.w, "\n%s\n%s\n", title, strings.Repeat("─", len([]rune(title))))

	p.row("Location", a.Location())
	p.row("Runtime", string(a.Runtime))
	p.row("Framework", a.Framework)
	p.row("Role", string(a.Role))
	p.row("Port", portText(a))
	p.row("Build", a.BuildCmd)
	p.row("Start", a.StartCmd)
	p.row("Deploy", a.DeployMethod())

	if len(a.Markers) > 0 {
		fmt.Fprintln(p.w, "Detected:")
		for _, m := range a.Markers {
			fmt.Fprintf(p.w, "  ✓ %s\n", m)
		}
	}
}

func (p *Printer) row(label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(p.w, "%-10s %s\n", label+":", value)
}

func portText(a app.Application) string {
	if a.Port == 0 {
		return ""
	}
	if a.PortSource == "" {
		return strconv.Itoa(a.Port)
	}
	return fmt.Sprintf("%d (%s)", a.Port, a.PortSource)
}

func describeLayout(apps []app.Application) string {
	if len(apps) == 1 {
		return "single application"
	}
	var frontend, backend bool
	for _, a := range apps {
		switch a.Role {
		case app.RoleFrontend:
			frontend = true
		case app.RoleBackend:
			backend = true
		case app.RoleFullstack:
			frontend, backend = true, true
		}
	}
	if frontend && backend {
		return "multi-app: frontend + backend"
	}
	return "multi-app"
}

func portConflicts(apps []app.Application) []string {
	byPort := map[int][]string{}
	for _, a := range apps {
		if a.Port != 0 {
			byPort[a.Port] = append(byPort[a.Port], a.Name)
		}
	}

	var out []string
	for port, names := range byPort {
		if len(names) > 1 {
			out = append(out, fmt.Sprintf("port %d is used by %s", port, strings.Join(names, ", ")))
		}
	}
	sort.Strings(out)
	return out
}