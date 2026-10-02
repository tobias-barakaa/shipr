package cli

import (
	"io"

	"shipr/internal/inspect"
	"shipr/internal/report"
)

// Inspect implements `shipr inspect <zip>`.
type Inspect struct{}

func (Inspect) Name() string    { return "inspect" }
func (Inspect) Summary() string { return "Inspect a .zip and report the applications inside" }

func (Inspect) Run(args []string, out io.Writer) error {
	if len(args) != 1 {
		return newUsageError("usage: shipr inspect <path-to-zip>")
	}
	project, err := inspect.Run(args[0])
	if err != nil {
		return err
	}
	report.NewPrinter(out).Print(project)
	return nil
}