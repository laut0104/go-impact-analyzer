package analyzer

import (
	"fmt"
	"strings"
)

// DependencyGraph manages the dependency graph between packages
type DependencyGraph struct {
	// Package path -> packages it depends on
	deps map[string][]string
	// Module path (project root path)
	modulePath string
	// GoListClient for listing packages
	goListClient GoListClient
}

// NewDependencyGraph creates a new dependency graph
func NewDependencyGraph(modulePath string) *DependencyGraph {
	return &DependencyGraph{
		deps:         make(map[string][]string),
		modulePath:   modulePath,
		goListClient: NewGoListClient(),
	}
}

// NewDependencyGraphWithClient creates a new dependency graph with a custom GoListClient
func NewDependencyGraphWithClient(modulePath string, goListClient GoListClient) *DependencyGraph {
	return &DependencyGraph{
		deps:         make(map[string][]string),
		modulePath:   modulePath,
		goListClient: goListClient,
	}
}

// Build loads packages matching the patterns and builds the dependency graph
func (g *DependencyGraph) Build(dir string, patterns ...string) error {
	packages, err := g.goListClient.ListPackages(dir, patterns...)
	if err != nil {
		return fmt.Errorf("failed to run go list: %w", err)
	}

	for _, pkg := range packages {
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
