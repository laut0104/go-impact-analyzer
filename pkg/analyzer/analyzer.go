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
	// BaseBranch is the base branch for git diff (e.g., "main", "origin/main")
	BaseBranch string
	// ExtractorOptions are options passed to ResourceExtractor
	ExtractorOptions []ExtractorOption
}

// Analyzer analyzes dependencies and identifies affected resources
type Analyzer struct {
	config         Config
	graph          *DependencyGraph
	extractor      *ResourceExtractor
	symbolAnalyzer *SymbolAnalyzer
	diffAnalyzer   *DiffAnalyzer
	resources      []Resource
	// Package path -> resource names that depend on it
	reverseDeps map[string][]string
}

// NewAnalyzer creates a new Analyzer with the given configuration
func NewAnalyzer(cfg Config) *Analyzer {
	if cfg.CmdDir == "" {
		cfg.CmdDir = "cli/cmd"
	}
	if cfg.BaseBranch == "" {
		cfg.BaseBranch = "origin/main"
	}

	return &Analyzer{
		config:         cfg,
		graph:          NewDependencyGraph(cfg.ModulePath),
		extractor:      NewResourceExtractor(cfg.ModulePath, cfg.ExtractorOptions...),
		symbolAnalyzer: NewSymbolAnalyzer(cfg.ModulePath, cfg.ProjectRoot),
		diffAnalyzer:   NewDiffAnalyzer(cfg.ProjectRoot, cfg.BaseBranch),
		reverseDeps:    make(map[string][]string),
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

// changedSymbolsInfo holds detailed information about changed symbols per package
type changedSymbolsInfo struct {
	symbols          []string
	interfaceMethods []InterfaceMethodRange
}

// GetAffectedResources identifies resources affected by changed files
func (a *Analyzer) GetAffectedResources(changedFiles []string) []AffectedResource {
	affectedMap := make(map[string]*AffectedResource)

	// Group changed files by package with absolute paths
	type fileInfo struct {
		absPath  string
		origPath string
	}
	filesByPackage := make(map[string][]fileInfo)

	for _, file := range changedFiles {
		pkgPath := a.fileToPackage(file)
		if pkgPath == "" {
			continue
		}
		// Convert to absolute path for symbol extraction
		absPath := file
		origPath := file
		if !filepath.IsAbs(file) {
			pathWithoutPrefix := file
			if a.config.PathPrefix != "" {
				pathWithoutPrefix = strings.TrimPrefix(file, a.config.PathPrefix)
			}
			absPath = filepath.Join(a.config.ProjectRoot, pathWithoutPrefix)
		}
		filesByPackage[pkgPath] = append(filesByPackage[pkgPath], fileInfo{absPath: absPath, origPath: origPath})
	}

	for pkgPath, files := range filesByPackage {
		// Extract only the symbols that were actually changed (function-level)
		var changedSymbols []string
		var changedInterfaceMethods []InterfaceMethodRange

		for _, fi := range files {
			// Get changed line numbers from git diff
			changedLines, err := a.diffAnalyzer.GetChangedLines(fi.origPath)
			if err != nil || len(changedLines) == 0 {
				// Fallback: if we can't get diff info, use all exported symbols
				symbols, err := a.symbolAnalyzer.ExtractExportedSymbols(fi.absPath)
				if err == nil {
					changedSymbols = append(changedSymbols, symbols...)
				}
				continue
			}

			// Get detailed symbol info including interface methods
			symbolInfo, err := a.symbolAnalyzer.GetChangedSymbolsDetailed(fi.absPath, changedLines)
			if err != nil {
				// Fallback to all symbols on error
				allSymbols, _ := a.symbolAnalyzer.ExtractExportedSymbols(fi.absPath)
				changedSymbols = append(changedSymbols, allSymbols...)
				continue
			}
			changedSymbols = append(changedSymbols, symbolInfo.Symbols...)
			changedInterfaceMethods = append(changedInterfaceMethods, symbolInfo.InterfaceMethods...)
		}

		// Remove duplicates from changedSymbols
		changedSymbols = uniqueStrings(changedSymbols)
		changedInterfaceMethods = uniqueInterfaceMethods(changedInterfaceMethods)

		// Get resources that depend on this package
		resourceNames := a.reverseDeps[pkgPath]
		for _, name := range resourceNames {
			if _, exists := affectedMap[name]; exists {
				continue
			}

			resource := a.getResourceByName(name)
			if resource == nil {
				continue
			}

			// Check if the resource actually uses the changed symbols or methods
			symbolsInfo := changedSymbolsInfo{
				symbols:          changedSymbols,
				interfaceMethods: changedInterfaceMethods,
			}
			isAffected := a.isResourceAffectedBySymbols(resource, pkgPath, symbolsInfo)
			if isAffected {
				affectedMap[name] = &AffectedResource{
					Resource:        *resource,
					Reason:          fmt.Sprintf("depends on %s", pkgPath),
					AffectedPackage: pkgPath,
					DependencyChain: a.getDependencyChain(resource.Package, pkgPath),
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

// uniqueInterfaceMethods removes duplicate interface methods
func uniqueInterfaceMethods(methods []InterfaceMethodRange) []InterfaceMethodRange {
	seen := make(map[string]bool)
	result := make([]InterfaceMethodRange, 0, len(methods))
	for _, m := range methods {
		key := m.InterfaceName + "." + m.MethodName
		if !seen[key] {
			seen[key] = true
			result = append(result, m)
		}
	}
	return result
}

// isResourceAffectedBySymbols checks if a resource is actually affected by the changed symbols
func (a *Analyzer) isResourceAffectedBySymbols(resource *Resource, changedPkgPath string, info changedSymbolsInfo) bool {
	// If there are no exported symbols or interface methods changed, consider it not affected
	// (internal changes that don't affect the public API)
	if len(info.symbols) == 0 && len(info.interfaceMethods) == 0 {
		return false
	}

	// Get all packages that the resource depends on (including subpackages of the resource)
	allDeps := a.graph.GetAllDeps(resource.Package)

	// Collect packages to check: resource package itself + all its dependencies that import the changed package
	packagesToCheck := []string{resource.Package}
	for _, dep := range allDeps {
		// Check if this dependency is a subpackage of the resource (e.g., resource/job)
		if strings.HasPrefix(dep, resource.Package+"/") {
			packagesToCheck = append(packagesToCheck, dep)
		}
	}

	// Check each package for symbol usage
	for _, pkg := range packagesToCheck {
		// Verify that this package actually depends on the changed package
		pkgDeps := a.graph.GetAllDeps(pkg)
		dependsOnChanged := false
		for _, d := range pkgDeps {
			if d == changedPkgPath {
				dependsOnChanged = true
				break
			}
		}
		if !dependsOnChanged {
			continue
		}

		pkgDir := a.symbolAnalyzer.GetPackageDir(pkg)

		// Check regular symbol usage
		if len(info.symbols) > 0 {
			usesSymbols, err := a.symbolAnalyzer.CheckSymbolUsage(pkgDir, changedPkgPath, info.symbols)
			if err != nil {
				continue
			}
			if usesSymbols {
				return true
			}
		}

		// Check interface method usage
		if len(info.interfaceMethods) > 0 {
			usesMethods, err := a.symbolAnalyzer.CheckMethodCallUsage(pkgDir, changedPkgPath, info.interfaceMethods)
			if err != nil {
				continue
			}
			if usesMethods {
				return true
			}
		}
	}

	return false
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
