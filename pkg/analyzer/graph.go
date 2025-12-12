package analyzer

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DependencyGraph manages the dependency graph between packages
type DependencyGraph struct {
	// Package path -> packages it depends on
	deps map[string][]string
	// Module path (project root path)
	modulePath string
}

// goListPackage represents the output of go list -json
type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
}

// NewDependencyGraph creates a new dependency graph
func NewDependencyGraph(modulePath string) *DependencyGraph {
	return &DependencyGraph{
		deps:       make(map[string][]string),
		modulePath: modulePath,
	}
}

// Build loads packages matching the patterns and builds the dependency graph
func (g *DependencyGraph) Build(dir string, patterns ...string) error {
	// Use go list -json which is faster than go/packages
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to run go list: %w", err)
	}

	// go list -json outputs multiple JSON objects (not an array)
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			// Skip invalid JSON
			continue
		}

		// Only track packages within the project
		if !g.isProjectPackage(pkg.ImportPath) {
			continue
		}

		// Filter imports to only project packages
		var projectImports []string
		for _, imp := range pkg.Imports {
			if g.isProjectPackage(imp) {
				projectImports = append(projectImports, imp)
			}
		}
		g.deps[pkg.ImportPath] = projectImports
	}

	return nil
}

// isProjectPackage determines if a package belongs to the project
func (g *DependencyGraph) isProjectPackage(pkgPath string) bool {
	return strings.HasPrefix(pkgPath, g.modulePath)
}

// GetDirectDeps returns direct dependencies of a package
func (g *DependencyGraph) GetDirectDeps(pkgPath string) []string {
	return g.deps[pkgPath]
}

// GetAllDeps returns all dependencies (including transitive) of a package
func (g *DependencyGraph) GetAllDeps(pkgPath string) []string {
	visited := make(map[string]bool)
	result := make([]string, 0)

	g.collectAllDeps(pkgPath, visited, &result)

	return result
}

func (g *DependencyGraph) collectAllDeps(pkgPath string, visited map[string]bool, result *[]string) {
	if visited[pkgPath] {
		return
	}
	visited[pkgPath] = true

	for _, dep := range g.deps[pkgPath] {
		*result = append(*result, dep)
		g.collectAllDeps(dep, visited, result)
	}
}

// GetAllPackages returns all package paths in the graph
func (g *DependencyGraph) GetAllPackages() []string {
	result := make([]string, 0, len(g.deps))
	for pkgPath := range g.deps {
		result = append(result, pkgPath)
	}
	return result
}

// HasPackage checks if a package exists in the graph
func (g *DependencyGraph) HasPackage(pkgPath string) bool {
	_, ok := g.deps[pkgPath]
	return ok
}
