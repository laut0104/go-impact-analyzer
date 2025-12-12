package analyzer

import (
	"fmt"
	"strings"

	"golang.org/x/tools/go/packages"
)

// DependencyGraph manages the dependency graph between packages
type DependencyGraph struct {
	// Package path -> packages it depends on
	deps map[string][]string
	// Package path -> package info
	pkgInfo map[string]*packages.Package
	// Module path (project root path)
	modulePath string
}

// NewDependencyGraph creates a new dependency graph
func NewDependencyGraph(modulePath string) *DependencyGraph {
	return &DependencyGraph{
		deps:       make(map[string][]string),
		pkgInfo:    make(map[string]*packages.Package),
		modulePath: modulePath,
	}
}

// Build loads packages matching the patterns and builds the dependency graph
func (g *DependencyGraph) Build(dir string, patterns ...string) error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedFiles,
		Dir:  dir,
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}

	// Error check
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			for _, e := range pkg.Errors {
				// Treat build errors as warnings (not all packages may be buildable)
				fmt.Printf("warning: package %s: %v\n", pkg.PkgPath, e)
			}
		}
	}

	// Build dependency graph
	visited := make(map[string]bool)
	for _, pkg := range pkgs {
		g.buildRecursive(pkg, visited)
	}

	return nil
}

func (g *DependencyGraph) buildRecursive(pkg *packages.Package, visited map[string]bool) {
	if pkg == nil || visited[pkg.PkgPath] {
		return
	}
	visited[pkg.PkgPath] = true

	// Only track packages within the project
	if !g.isProjectPackage(pkg.PkgPath) {
		return
	}

	g.pkgInfo[pkg.PkgPath] = pkg

	deps := make([]string, 0)
	for importPath, importPkg := range pkg.Imports {
		// Only track packages within the project
		if g.isProjectPackage(importPath) {
			deps = append(deps, importPath)
			g.buildRecursive(importPkg, visited)
		}
	}
	g.deps[pkg.PkgPath] = deps
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

// GetPackageInfo returns package information
func (g *DependencyGraph) GetPackageInfo(pkgPath string) *packages.Package {
	return g.pkgInfo[pkgPath]
}

// HasPackage checks if a package exists in the graph
func (g *DependencyGraph) HasPackage(pkgPath string) bool {
	_, ok := g.deps[pkgPath]
	return ok
}
