package inspect

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"shipr/internal/app"
	"shipr/internal/testutil"
)

const (
	expressPkg = `{"name":"js-api","scripts":{"start":"node server.js"},"dependencies":{"express":"^4.18.0"}}`
)

func TestRunMultiApp(t *testing.T) {
	p, err := Run(testutil.WriteZip(t, map[string]string{
		"backend-js/package.json":     expressPkg,
		"backend-py/requirements.txt": "flask\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "sample" || len(p.Apps) != 2 {
		t.Fatalf("project = %+v", p)
	}
	if p.Apps[0].Name != "js-api" || p.Apps[0].Root != "backend-js" {
		t.Errorf("app 0 = %+v", p.Apps[0])
	}
	if p.Apps[1].Name != "backend-py" || p.Apps[1].Runtime != app.RuntimePython {
		t.Errorf("app 1 = %+v", p.Apps[1])
	}
}

func TestRunRootAppFallsBackToProjectName(t *testing.T) {
	p, err := Run(testutil.WriteZip(t, map[string]string{"requirements.txt": "flask\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Apps) != 1 || p.Apps[0].Name != "sample" || p.Apps[0].Root != "" {
		t.Errorf("apps = %+v", p.Apps)
	}
}

func TestRunMissingFile(t *testing.T) {
	_, err := Run(filepath.Join(t.TempDir(), "nope.zip"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "opening archive") || !strings.Contains(err.Error(), "nope.zip") {
		t.Errorf("error lacks context: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error chain lost the cause: %v", err)
	}
}

func TestRunInvalidArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(p, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(p); err == nil || !strings.Contains(err.Error(), "opening archive") {
		t.Errorf("err = %v", err)
	}
}

func TestRunSurfacesArchiveWarnings(t *testing.T) {
	p, err := Run(testutil.WriteEntries(t, []testutil.Entry{
		{Name: "../evil", Body: "x"},
		{Name: "package.json", Body: expressPkg},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Apps) != 1 || len(p.Warnings) != 1 {
		t.Errorf("apps=%d warnings=%v", len(p.Apps), p.Warnings)
	}
}

func TestRunIsDeterministic(t *testing.T) {
	zipPath := testutil.WriteZip(t, map[string]string{
		"a/package.json":     expressPkg,
		"b/requirements.txt": "flask\n",
		"c/Dockerfile":       "EXPOSE 8000\n",
	})
	first, err := Run(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Run(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs", i)
		}
	}
}
