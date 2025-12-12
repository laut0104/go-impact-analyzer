package analyzer

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Config holds the analyzer configuration
type Config struct {
	// ModulePath is the Go module path (e.g., "github.com/org/repo")
	ModulePath string
	// ProjectRoot is the root directory of the project
	ProjectRoot string
	// CmdDir is the directory containing CLI command definitions (default: "cli/cmd")
	CmdDir string
	// PathPrefix is removed from file paths when converting to package paths (default: "")
	PathPrefix string
	// ExtractorOptions are options passed to ResourceExtractor
	ExtractorOptions []ExtractorOption
}

// Analyzer analyzes dependencies and identifies affected resources
type Analyzer struct {
	config    Config
	graph     *DependencyGraph
	extractor *ResourceExtractor
	resources []Resource
	// Package path -> resource names that depend on it
	reverseDeps map[string][]string
}

// NewAnalyzer creates a new Analyzer with the given configuration
func NewAnalyzer(cfg Config) *Analyzer {
	if cfg.CmdDir == "" {
		cfg.CmdDir = "cli/cmd"
	}

	return &Analyzer{
		config:      cfg,
		graph:       NewDependencyGraph(cfg.ModulePath),
		extractor:   NewResourceExtractor(cfg.ModulePath, cfg.ExtractorOptions...),
		reverseDeps: make(map[string][]string),
	}
}

// NewAnalyzerSimple creates a new Analyzer with minimal configuration (for backward compatibility)
func NewAnalyzerSimple(modulePath, projectRoot string) *Analyzer {
	return NewAnalyzer(Config{
		ModulePath:  modulePath,
		ProjectRoot: projectRoot,
	})
}

// Analyze analyzes the project and builds resources and dependencies
func (a *Analyzer) Analyze() error {
	// 1. Extract resources from cli/cmd
	cmdDir := filepath.Join(a.config.ProjectRoot, a.config.CmdDir)
	resources, err := a.extractor.ExtractFromDir(cmdDir)
	if err != nil {
		return fmt.Errorf("failed to extract resources: %w", err)
	}
	a.resources = resources

	// 2. Build dependency graph for all packages
	if err := a.graph.Build(a.config.ProjectRoot, "./..."); err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}

	// 3. Build reverse dependency map
	a.buildReverseDependencies()

	return nil
}

// buildReverseDependencies builds the reverse dependency map
func (a *Analyzer) buildReverseDependencies() {
	for _, resource := range a.resources {
		if resource.Package == "" {
			continue
		}

		// Add the package that the resource directly depends on
		a.reverseDeps[resource.Package] = append(a.reverseDeps[resource.Package], resource.Name)

		// Get all packages that the resource depends on
		allDeps := a.graph.GetAllDeps(resource.Package)
		for _, dep := range allDeps {
			a.reverseDeps[dep] = append(a.reverseDeps[dep], resource.Name)
		}
	}

	// Remove duplicates
	for pkg, resources := range a.reverseDeps {
		a.reverseDeps[pkg] = uniqueStrings(resources)
	}
}

// GetResources returns the extracted resource list
func (a *Analyzer) GetResources() []Resource {
	return a.resources
}

// GetAffectedResources identifies resources affected by changed files
func (a *Analyzer) GetAffectedResources(changedFiles []string) []AffectedResource {
	affectedMap := make(map[string]*AffectedResource)

	for _, file := range changedFiles {
		// Infer package path from file path
		pkgPath := a.fileToPackage(file)
		if pkgPath == "" {
			continue
		}

		// Get resources that depend on this package
		resourceNames := a.reverseDeps[pkgPath]
		for _, name := range resourceNames {
			if _, exists := affectedMap[name]; !exists {
				resource := a.getResourceByName(name)
				if resource != nil {
					affectedMap[name] = &AffectedResource{
						Resource:        *resource,
						Reason:          fmt.Sprintf("depends on %s", pkgPath),
						AffectedPackage: pkgPath,
						DependencyChain: a.getDependencyChain(resource.Package, pkgPath),
					}
				}
			}
		}
	}

	result := make([]AffectedResource, 0, len(affectedMap))
	for _, r := range affectedMap {
		result = append(result, *r)
	}
	return result
}

// GetAffectedResourcesByPackage identifies resources affected by a package path
func (a *Analyzer) GetAffectedResourcesByPackage(pkgPath string) []AffectedResource {
	var result []AffectedResource

	resourceNames := a.reverseDeps[pkgPath]
	for _, name := range resourceNames {
		resource := a.getResourceByName(name)
		if resource != nil {
			result = append(result, AffectedResource{
				Resource:        *resource,
				Reason:          fmt.Sprintf("depends on %s", pkgPath),
				AffectedPackage: pkgPath,
				DependencyChain: a.getDependencyChain(resource.Package, pkgPath),
			})
		}
	}

	return result
}

// fileToPackage infers package path from file path
func (a *Analyzer) fileToPackage(filePath string) string {
	// Convert to relative path
	relPath := filePath
	if filepath.IsAbs(filePath) {
		var err error
		relPath, err = filepath.Rel(a.config.ProjectRoot, filePath)
		if err != nil {
			return ""
		}
	}

	// Remove path prefix (e.g., "go/" if git diff returns paths from repo root)
	if a.config.PathPrefix != "" {
		relPath = strings.TrimPrefix(relPath, a.config.PathPrefix)
	}

	// Ignore non-Go files
	if !strings.HasSuffix(relPath, ".go") {
		return ""
	}

	// Get directory path
	dir := filepath.Dir(relPath)
	if dir == "." {
		return a.config.ModulePath
	}

	// Build package path
	pkgPath := a.config.ModulePath + "/" + filepath.ToSlash(dir)
	return pkgPath
}

// getResourceByName gets a resource by name
func (a *Analyzer) getResourceByName(name string) *Resource {
	for i := range a.resources {
		if a.resources[i].Name == name {
			return &a.resources[i]
		}
	}
	return nil
}

// getDependencyChain gets the dependency chain
func (a *Analyzer) getDependencyChain(fromPkg, toPkg string) []string {
	// Simple implementation: find shortest path with BFS
	if fromPkg == toPkg {
		return []string{fromPkg}
	}

	type node struct {
		pkg  string
		path []string
	}

	visited := make(map[string]bool)
	queue := []node{{pkg: fromPkg, path: []string{fromPkg}}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if visited[current.pkg] {
			continue
		}
		visited[current.pkg] = true

		deps := a.graph.GetDirectDeps(current.pkg)
		for _, dep := range deps {
			newPath := append([]string{}, current.path...)
			newPath = append(newPath, dep)

			if dep == toPkg {
				return newPath
			}

			queue = append(queue, node{pkg: dep, path: newPath})
		}
	}

	return nil
}

// GetReverseDeps returns resource names that depend on the specified package
func (a *Analyzer) GetReverseDeps(pkgPath string) []string {
	return a.reverseDeps[pkgPath]
}

// GetAllReverseDeps returns all reverse dependency mappings
func (a *Analyzer) GetAllReverseDeps() map[string][]string {
	return a.reverseDeps
}

// GetDependencyGraph returns the dependency graph
func (a *Analyzer) GetDependencyGraph() *DependencyGraph {
	return a.graph
}

// uniqueStrings removes duplicates from a slice
func uniqueStrings(s []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
