// Package inspect orchestrates Phase 1: archive in, Project out.
package inspect

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"shipr/internal/app"
	"shipr/internal/archive"
	"shipr/internal/detect"
)

var _ detect.FS = (*archive.Archive)(nil)

// Run inspects a zip file and returns everything discovered inside it.
func Run(zipPath string) (*app.Project, error) {
	arc, err := archive.Open(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening archive %q: %w", zipPath, err)
	}
	defer arc.Close()

	res := detect.Scan(arc, detect.Default())
	project := &app.Project{
		Name:     projectName(zipPath),
		Apps:     res.Apps,
		Warnings: append(arc.Warnings(), res.Warnings...),
	}
	for i := range project.Apps {
		a := &project.Apps[i]
		if a.Name == "" {
			a.Name = fallbackName(a.Root, project.Name)
		}
	}
	return project, nil
}

func projectName(zipPath string) string {
	base := filepath.Base(zipPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func fallbackName(root, projectName string) string {
	if root == "" {
		return projectName
	}
	return path.Base(root)
}