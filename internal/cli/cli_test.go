package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"shipr/internal/testutil"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoCommand(t *testing.T) {
	code, _, stderr := run()
	if code != ExitUsage || !strings.Contains(stderr, "Usage:") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, _ := run(arg)
		if code != ExitOK || !strings.Contains(stdout, "inspect") {
			t.Errorf("%s: code=%d stdout=%q", arg, code, stdout)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run("bogus")
	if code != ExitUsage || !strings.Contains(stderr, `unknown command "bogus"`) {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestInspectMissingArgument(t *testing.T) {
	code, _, stderr := run("inspect")
	if code != ExitUsage || !strings.Contains(stderr, "usage: shipr inspect") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestInspectMissingFile(t *testing.T) {
	code, _, stderr := run("inspect", filepath.Join(t.TempDir(), "nope.zip"))
	if code != ExitError || !strings.Contains(stderr, "opening archive") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestInspectInvalidArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.zip")
	if err := os.WriteFile(p, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run("inspect", p)
	if code != ExitError || !strings.Contains(stderr, "opening archive") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}

func TestInspectValidArchive(t *testing.T) {
	p := testutil.WriteZip(t, map[string]string{
		"package.json": `{"name":"api","scripts":{"start":"node ."},"dependencies":{"express":"4"}}`,
	})
	code, stdout, stderr := run("inspect", p)
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{"Applications found: 1", "Express", "Node.js", "npm start"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}
