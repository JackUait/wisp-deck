package models

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Project represents a project entry
type Project struct {
	Name      string
	Path      string
	Worktrees []Worktree
	Stale     bool
}

// ParseProjectName extracts the project name from a "name:path" line.
// Returns everything before the first colon. If no colon is present,
// returns the entire line (matches bash ${1%%:*} behavior).
func ParseProjectName(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return line
	}
	return line[:idx]
}

// ParseProjectPath extracts the project path from a "name:path" line.
// Returns everything after the first colon. If no colon is present,
// returns the entire line (matches bash ${1#*:} behavior).
func ParseProjectPath(line string) string {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return line
	}
	return line[idx+1:]
}

// LoadProjects reads projects from file (name:path format)
func LoadProjects(filepath string) ([]Project, error) {
	file, err := os.Open(filepath)
	if err != nil {
		// A fresh install has no projects file until the first project is added
		// from the main menu — missing means "no projects yet" (matching
		// load_projects in lib/projects.sh), not a startup failure.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open projects file: %w", err)
	}
	defer file.Close()

	var projects []Project
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse name:path format
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue // Skip malformed lines
		}

		path := strings.TrimSpace(parts[1])
		_, statErr := os.Stat(path)
		projects = append(projects, Project{
			Name:  strings.TrimSpace(parts[0]),
			Path:  path,
			Stale: statErr != nil,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read projects file: %w", err)
	}

	return projects, nil
}
