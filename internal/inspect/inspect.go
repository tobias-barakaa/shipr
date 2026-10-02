package inspect

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"deployer/internal/app"
	"deployer/internal/archive"
	"deployer/internal/detect"
)

// Run opens a zip and returns everything deployable found inside it.
func Run(zipPath string) (*app.Project, error) {
	arc, err := archive.Open(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", zipPath, err)
	}
	defer arc.Close()

	project := &app.Project{Name: projectName(zipPath)}
	project.Apps = detect.Scan(arc, arc.Dirs(), detect.Default())

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