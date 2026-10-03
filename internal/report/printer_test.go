package report

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"shipr/internal/app"
)

func row(label, value string) string { return fmt.Sprintf("%-12s%s\n", label+":", value) }

func astroApp() app.Application {
	a := app.Application{
		Name:           "zelisline",
		Root:           "zelisline",
		Runtime:        app.RuntimeNode,
		Framework:      "Astro",
		Role:           app.RoleFrontend,
		PackageManager: app.PMNpm,
		Confidence:     app.ConfidenceHigh,
		BuildCmd:       "npm run build",
		StartCmd:       "npm start",
	}
	a.AddEvidence(app.EvidenceFile, "package.json")
	a.AddEvidence(app.EvidenceFile, "package-lock.json")
	a.AddEvidence(app.EvidenceDependency, "Astro")
	a.AddPort(4321, app.PortDefault)
	return a
}

func render(p *app.Project) string {
	var buf bytes.Buffer
	NewPrinter(&buf).Print(p)
	return buf.String()
}

func TestSingleApplicationReport(t *testing.T) {
	got := render(&app.Project{Name: "zelisline-astro-project", Apps: []app.Application{astroApp()}})

	want := "Project: zelisline-astro-project\n" +
		"Applications found: 1\n\n" +
		"[1] zelisline\n\n" +
		row("Location", "/zelisline") +
		row("Runtime", "Node.js") +
		row("Framework", "Astro") +
		row("Role", "frontend") +
		row("Confidence", "high") +
		row("Package", "npm") +
		row("Port", "4321 (framework default, unconfirmed)") +
		row("Build", "npm run build") +
		row("Start", "npm start") +
		"\nEvidence:\n" +
		"  ✓ package.json\n" +
		"  ✓ package-lock.json\n" +
		"  ✓ Astro dependency\n"

	if got != want {
		t.Errorf("report mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMultiAppReportOrderAndNotes(t *testing.T) {
	second := app.Application{Name: "api", Runtime: app.RuntimeGo, Role: app.RoleBackend, Confidence: app.ConfidenceHigh}
	second.AddEvidence(app.EvidenceNote, "2 main packages found; using .")
	out := render(&app.Project{Name: "p", Apps: []app.Application{astroApp(), second}})

	if strings.Index(out, "[1] zelisline") > strings.Index(out, "[2] api") {
		t.Error("applications out of order")
	}
	if !strings.Contains(out, "~ 2 main packages found; using .") {
		t.Errorf("note not rendered with ~ marker:\n%s", out)
	}
	if !strings.Contains(out, "Applications found: 2") {
		t.Errorf("missing count:\n%s", out)
	}
}

func TestPortConflictsAreShown(t *testing.T) {
	a := astroApp()
	a.Ports = nil
	a.AddPort(7000, app.PortDockerfile)
	a.AddPort(9000, app.PortEnv)
	out := render(&app.Project{Name: "p", Apps: []app.Application{a}})

	if !strings.Contains(out, row("Port", "7000 (Dockerfile EXPOSE)")) {
		t.Errorf("selected port wrong:\n%s", out)
	}
	if !strings.Contains(out, row("Also found", "9000 (.env)")) {
		t.Errorf("conflict not shown:\n%s", out)
	}
}

func TestSharedPortWarning(t *testing.T) {
	a, b := astroApp(), astroApp()
	b.Name = "other"
	out := render(&app.Project{Name: "p", Apps: []app.Application{a, b}})
	if !strings.Contains(out, "port 4321 is used by zelisline, other") {
		t.Errorf("missing clash warning:\n%s", out)
	}
}

func TestNoApplicationsAndWarnings(t *testing.T) {
	out := render(&app.Project{Name: "empty", Warnings: []string{"skipped entry \"../x\": path traversal (..)"}})
	if !strings.Contains(out, "No applications detected.") || !strings.Contains(out, "Warnings:") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestControlCharactersAreSanitized(t *testing.T) {
	a := astroApp()
	a.Name = "evil\x1b[31mname"
	a.Evidence = append(a.Evidence, app.Evidence{Kind: app.EvidenceNote, Detail: "bad\x07note"})
	out := render(&app.Project{Name: "p\x1b[2J", Apps: []app.Application{a}, Warnings: []string{"w\x1b[0m"}})
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Errorf("control characters leaked into output: %q", out)
	}
}

func TestReportIsDeterministic(t *testing.T) {
	p := &app.Project{Name: "p", Apps: []app.Application{astroApp(), astroApp()}}
	first := render(p)
	for i := 0; i < 5; i++ {
		if render(p) != first {
			t.Fatal("output differs between runs")
		}
	}
}
