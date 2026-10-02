package cli

import (
	"errors"
	"io"

	"deployer/internal/inspect"
	"deployer/internal/report"
)

type Inspect struct{}

func (Inspect) Name() string    { return "inspect" }
func (Inspect) Summary() string { return "Analyze a .zip and report the applications inside" }

func (Inspect) Run(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: deployer inspect <path-to-zip>")
	}

	project, err := inspect.Run(args[0])
	if err != nil {
		return err
	}

	report.NewPrinter(out).Print(project)
	return nil
}

