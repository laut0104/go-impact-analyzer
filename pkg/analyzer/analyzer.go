package analyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
	// InfrastructureFiles are files that should be treated as infrastructure files
	// Changes to these files alone don't affect resources unless the exported symbols they define are used
	// Example: ["sqlc/db.go", "sqlc/models.go"]
	InfrastructureFiles []string
}

// Analyzer analyzes dependencies and identifies affected resources
type Analyzer struct {
	config         Config
	graph          *DependencyGraph
	extractor      *ResourceExtractor
	symbolAnalyzer *SymbolAnalyzer
	diffAnalyzer   *DiffAnalyzer
	diAnalyzer     *DIAnalyzer
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
		diAnalyzer:     NewDIAnalyzer(cfg.ModulePath, cfg.ProjectRoot),
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
	symbols              []string
	interfaceMethods     []InterfaceMethodRange
	hasUnexportedChanges bool
}

// GetAffectedResources identifies resources affected by changed files
func (a *Analyzer) GetAffectedResources(changedFiles []string) []AffectedResource {
	affectedMap := make(map[string]*AffectedResource)

	// Group changed files by package with absolute paths
	type fileInfo struct {
		absPath          string
		origPath         string
		isInfrastructure bool
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
		// Check if this is an infrastructure file
		isInfra := a.isInfrastructureFile(file)
		filesByPackage[pkgPath] = append(filesByPackage[pkgPath], fileInfo{absPath: absPath, origPath: origPath, isInfrastructure: isInfra})
	}

	for pkgPath, files := range filesByPackage {
		// Check if all files in this package are infrastructure files
		allInfrastructure := true
		hasNonInfraFiles := false
		for _, fi := range files {
			if !fi.isInfrastructure {
				allInfrastructure = false
				hasNonInfraFiles = true
				break
			}
		}

		// Extract only the symbols that were actually changed (function-level)
		var changedSymbols []string
		var changedInterfaceMethods []InterfaceMethodRange
		hasUnexportedChanges := false

		// Track symbols from infrastructure files separately
		var infraSymbols []string

		for _, fi := range files {
			// Get changed line numbers from git diff
			changedLines, err := a.diffAnalyzer.GetChangedLines(fi.origPath)
			if err != nil || len(changedLines) == 0 {
				// Fallback: if we can't get diff info, use all exported symbols
				symbols, err := a.symbolAnalyzer.ExtractExportedSymbols(fi.absPath)
				if err == nil {
					if fi.isInfrastructure {
						infraSymbols = append(infraSymbols, symbols...)
					} else {
						changedSymbols = append(changedSymbols, symbols...)
					}
				}
				continue
			}

			// Get detailed symbol info including interface methods
			symbolInfo, err := a.symbolAnalyzer.GetChangedSymbolsDetailed(fi.absPath, changedLines)
			if err != nil {
				// Fallback to all symbols on error
				allSymbols, _ := a.symbolAnalyzer.ExtractExportedSymbols(fi.absPath)
				if fi.isInfrastructure {
					infraSymbols = append(infraSymbols, allSymbols...)
				} else {
					changedSymbols = append(changedSymbols, allSymbols...)
				}
				continue
			}

			if fi.isInfrastructure {
				infraSymbols = append(infraSymbols, symbolInfo.Symbols...)
			} else {
				changedSymbols = append(changedSymbols, symbolInfo.Symbols...)
				changedInterfaceMethods = append(changedInterfaceMethods, symbolInfo.InterfaceMethods...)
				if symbolInfo.HasUnexportedChanges {
					hasUnexportedChanges = true
				}
			}
		}

		// If all files are infrastructure files and no non-infra files changed,
		// we need to find which resources actually use the changed symbols from infra files
		if allInfrastructure && !hasNonInfraFiles && len(infraSymbols) > 0 {
			// Use infra symbols for checking but with more strict symbol-level matching
			changedSymbols = infraSymbols
		}

		// Remove duplicates from changedSymbols
		changedSymbols = uniqueStrings(changedSymbols)
		changedInterfaceMethods = uniqueInterfaceMethods(changedInterfaceMethods)

		// Remove interface names from changedSymbols if we have specific method info
		// This prevents false positives where a resource uses the interface type but not the changed methods
		if len(changedInterfaceMethods) > 0 {
			interfaceNames := make(map[string]bool)
			for _, m := range changedInterfaceMethods {
				interfaceNames[m.InterfaceName] = true
			}
			var filteredSymbols []string
			for _, sym := range changedSymbols {
				if !interfaceNames[sym] {
					filteredSymbols = append(filteredSymbols, sym)
				}
			}
			changedSymbols = filteredSymbols
		}

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
				symbols:              changedSymbols,
				interfaceMethods:     changedInterfaceMethods,
				hasUnexportedChanges: hasUnexportedChanges,
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
	// (Note: unexported changes are now handled by adding exported symbols from the same file)
	if len(info.symbols) == 0 && len(info.interfaceMethods) == 0 {
		return false
	}

	// If the changed package IS the resource's package (or a subpackage), it's always affected
	// This handles cases where files are added/modified within the resource's own package
	if resource.Package == changedPkgPath || strings.HasPrefix(changedPkgPath, resource.Package+"/") {
		return true
	}

	// Special handling for DI provider packages (under pkg/provider/)
	// For these packages, we check if the resource uses the interface that the provider provides,
	// rather than checking the intermediate aggregation package (like job/provider)
	if strings.Contains(changedPkgPath, "/pkg/provider/") {
		return a.isResourceAffectedByProviderChange(resource, changedPkgPath, info)
	}

	// Special handling for aggregator provider packages (like job/provider)
	// These packages aggregate multiple providers via fx.Options
	// We need to identify which specific providers were changed and check if the resource uses them
	if a.isAggregatorProviderPackage(changedPkgPath) {
		return a.isResourceAffectedByAggregatorChange(resource, changedPkgPath, info)
	}

	// Get all packages that the resource depends on (including subpackages of the resource)
	allDeps := a.graph.GetAllDeps(resource.Package)

	// Collect packages to check: resource package itself + all its subpackages
	packagesToCheck := []string{resource.Package}
	for _, dep := range allDeps {
		// Check if this dependency is a subpackage of the resource (e.g., resource/job)
		if strings.HasPrefix(dep, resource.Package+"/") {
			packagesToCheck = append(packagesToCheck, dep)
		}
	}

	// Check each package for symbol usage
	for _, pkg := range packagesToCheck {
		// Verify that this package depends on the changed package (directly or transitively)
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

		// Find all packages that directly import the changed package
		// This could be pkg itself or multiple of its dependencies
		var directImporters []string

		// Check if pkg directly imports the changed package
		pkgDirectDeps := a.graph.GetDirectDeps(pkg)
		for _, d := range pkgDirectDeps {
			if d == changedPkgPath {
				directImporters = append(directImporters, pkg)
				break
			}
		}

		// Find which of pkg's dependencies directly import the changed package
		for _, dep := range pkgDeps {
			depDirectDeps := a.graph.GetDirectDeps(dep)
			for _, d := range depDirectDeps {
				if d == changedPkgPath {
					directImporters = append(directImporters, dep)
					break
				}
			}
		}

		if len(directImporters) == 0 {
			continue
		}

		// Check each direct importer for symbol usage
		for _, directImporter := range directImporters {
			pkgDir := a.symbolAnalyzer.GetPackageDir(directImporter)

			// Check regular symbol usage
			if len(info.symbols) > 0 {
				usesSymbols, err := a.symbolAnalyzer.CheckSymbolUsage(pkgDir, changedPkgPath, info.symbols)
				if err != nil {
					continue
				}
				if usesSymbols {
					// Additional check: if the direct importer is an intermediate package (not the package being checked),
					// verify that the package being checked (or resource) actually uses the affected symbols from the direct importer
					if directImporter != pkg {
						// Get the affected exported symbols in the direct importer
						affectedSymbolsInImporter := a.getAffectedExportedSymbols(directImporter, changedPkgPath, info.symbols)
						if len(affectedSymbolsInImporter) > 0 {
							// Check if pkg or resource uses any of the affected symbols from the direct importer
							checkPkgDir := a.symbolAnalyzer.GetPackageDir(pkg)
							usesAffected, _ := a.symbolAnalyzer.CheckSymbolUsage(checkPkgDir, directImporter, affectedSymbolsInImporter)
							if !usesAffected {
								resourcePkgDir := a.symbolAnalyzer.GetPackageDir(resource.Package)
								usesAffectedFromResource, _ := a.symbolAnalyzer.CheckSymbolUsage(resourcePkgDir, directImporter, affectedSymbolsInImporter)
								if !usesAffectedFromResource {
									continue
								}
							}
						} else {
							// No affected symbols in the intermediate package
							continue
						}
					}
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
					// Additional check: if the direct importer is an intermediate package,
					// verify that the resource actually uses the affected symbols from the direct importer
					if directImporter != pkg {
						// Get the affected exported symbols in the direct importer that use the changed interface methods
						affectedSymbolsInImporter := a.getAffectedExportedSymbolsByMethods(directImporter, changedPkgPath, info.interfaceMethods)
						if len(affectedSymbolsInImporter) > 0 {
							// Check if pkg or resource uses any of the affected symbols from the direct importer
							checkPkgDir := a.symbolAnalyzer.GetPackageDir(pkg)
							usesAffected, _ := a.symbolAnalyzer.CheckSymbolUsage(checkPkgDir, directImporter, affectedSymbolsInImporter)
							if !usesAffected {
								resourcePkgDir := a.symbolAnalyzer.GetPackageDir(resource.Package)
								usesAffectedFromResource, _ := a.symbolAnalyzer.CheckSymbolUsage(resourcePkgDir, directImporter, affectedSymbolsInImporter)
								if !usesAffectedFromResource {
									continue
								}
							}
						} else {
							// No affected symbols in the intermediate package
							continue
						}
					}
					return true
				}
			}
		}
	}

	return false
}

// getAffectedExportedSymbols finds exported symbols in a package that use the changed symbols from another package
func (a *Analyzer) getAffectedExportedSymbols(pkgPath, changedPkgPath string, changedSymbols []string) []string {
	pkgDir := a.symbolAnalyzer.GetPackageDir(pkgPath)

	// Get all exported symbols in the package
	allExportedSymbols, err := a.symbolAnalyzer.ExtractAllExportedSymbolsFromDir(pkgDir)
	if err != nil {
		return nil
	}

	// For each exported symbol, check if it uses any of the changed symbols
	var affectedSymbols []string
	for _, sym := range allExportedSymbols {
		// Check if this symbol's implementation uses any of the changed symbols
		usesChanged, _ := a.symbolAnalyzer.CheckSymbolUsesSymbols(pkgDir, changedPkgPath, changedSymbols, sym)
		if usesChanged {
			affectedSymbols = append(affectedSymbols, sym)
		}
	}

	// If a factory function (like New) is affected, also include the interface it returns
	// This is because changes to the implementation affect all code using the interface
	if len(affectedSymbols) > 0 {
		returnTypes := a.symbolAnalyzer.GetFactoryReturnTypes(pkgDir, affectedSymbols)
		for _, rt := range returnTypes {
			// Check if return type is an exported interface in this package
			for _, sym := range allExportedSymbols {
				if sym == rt && !contains(affectedSymbols, sym) {
					affectedSymbols = append(affectedSymbols, sym)
				}
			}
		}
	}

	return affectedSymbols
}

// contains checks if a string slice contains a value
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// getAffectedExportedSymbolsByMethods finds exported symbols in a package that call the changed interface methods
func (a *Analyzer) getAffectedExportedSymbolsByMethods(pkgPath, changedPkgPath string, changedMethods []InterfaceMethodRange) []string {
	pkgDir := a.symbolAnalyzer.GetPackageDir(pkgPath)

	// Get all exported symbols in the package
	allExportedSymbols, err := a.symbolAnalyzer.ExtractAllExportedSymbolsFromDir(pkgDir)
	if err != nil {
		return nil
	}

	// For each exported symbol, check if it calls any of the changed interface methods
	var affectedSymbols []string
	for _, sym := range allExportedSymbols {
		// Check if this symbol's implementation calls any of the changed methods
		usesMethods, _ := a.symbolAnalyzer.CheckSymbolUsesInterfaceMethods(pkgDir, changedPkgPath, changedMethods, sym)
		if usesMethods {
			affectedSymbols = append(affectedSymbols, sym)
		}
	}

	// If a factory function (like New) is affected, also include the interface it returns
	if len(affectedSymbols) > 0 {
		returnTypes := a.symbolAnalyzer.GetFactoryReturnTypes(pkgDir, affectedSymbols)
		for _, rt := range returnTypes {
			for _, sym := range allExportedSymbols {
				if sym == rt && !contains(affectedSymbols, sym) {
					affectedSymbols = append(affectedSymbols, sym)
				}
			}
		}
	}

	return affectedSymbols
}

// isResourceAffectedByProviderChange checks if a resource is affected by changes to a DI provider package
// For provider packages, we directly check if the resource uses the interface that the provider provides,
// bypassing intermediate aggregation packages like job/provider
func (a *Analyzer) isResourceAffectedByProviderChange(resource *Resource, changedPkgPath string, info changedSymbolsInfo) bool {
	// Get the provider package directory
	providerPkgDir := a.symbolAnalyzer.GetPackageDir(changedPkgPath)

	// Find the interface types that this provider provides
	// Provider packages typically have a New function that returns an interface
	var providedInterfaces []string
	for _, sym := range info.symbols {
		returnTypes := a.symbolAnalyzer.GetFactoryReturnTypes(providerPkgDir, []string{sym})
		providedInterfaces = append(providedInterfaces, returnTypes...)
	}

	// If no interfaces are provided, the provider may only have Provider variable changed
	// In that case, we need to find what interface the New function returns
	if len(providedInterfaces) == 0 {
		returnTypes := a.symbolAnalyzer.GetFactoryReturnTypes(providerPkgDir, []string{"New"})
		providedInterfaces = append(providedInterfaces, returnTypes...)
	}

	// If still no interfaces found, fall back to not affected (conservative for provider packages)
	if len(providedInterfaces) == 0 {
		return false
	}

	// Find the package that defines these interface types
	// The provider typically imports and returns an interface from another package (e.g., mcm.MCMClient)
	interfacePackages := a.findInterfaceDefinitionPackages(providerPkgDir, providedInterfaces)

	// Check if the resource uses any of the provided interfaces
	// We check the resource package and all its subpackages
	allDeps := a.graph.GetAllDeps(resource.Package)
	packagesToCheck := []string{resource.Package}
	for _, dep := range allDeps {
		if strings.HasPrefix(dep, resource.Package+"/") {
			packagesToCheck = append(packagesToCheck, dep)
		}
	}

	for _, pkg := range packagesToCheck {
		pkgDir := a.symbolAnalyzer.GetPackageDir(pkg)

		for interfacePkg, interfaceNames := range interfacePackages {
			// Check if this package uses the provided interface via DI (struct fields, function params)
			usesInterface, _ := a.diAnalyzer.CheckTypeUsage(pkgDir, interfacePkg, interfaceNames)
			if usesInterface {
				return true
			}

			// Also check direct symbol usage (for cases where the interface is used as a type annotation)
			usesSymbol, _ := a.symbolAnalyzer.CheckSymbolUsage(pkgDir, interfacePkg, interfaceNames)
			if usesSymbol {
				return true
			}
		}
	}

	return false
}

// isAggregatorProviderPackage checks if a package is an aggregator provider package
// Aggregator packages export fx.Options variables that combine multiple providers
func (a *Analyzer) isAggregatorProviderPackage(pkgPath string) bool {
	// Check common patterns for aggregator packages
	// - job/provider
	// - api-gateway/provider (but not api-gateway/provider/*)
	// - Contains "provider" in path but not under pkg/provider/
	if strings.Contains(pkgPath, "/pkg/provider/") {
		return false
	}

	// Check if the path ends with "/provider" or contains "/provider/" followed by no more subdirs
	parts := strings.Split(pkgPath, "/")
	for i, part := range parts {
		if part == "provider" {
			// If "provider" is the last part, it's likely an aggregator
			if i == len(parts)-1 {
				return true
			}
			// If there's only "internal" after provider, also check
			if i < len(parts)-1 && parts[i+1] == "internal" {
				return true
			}
		}
	}

	return false
}

// isResourceAffectedByAggregatorChange checks if a resource is affected by changes to an aggregator provider package
// It analyzes which providers were added/modified in the fx.Options and checks if the resource uses them
func (a *Analyzer) isResourceAffectedByAggregatorChange(resource *Resource, changedPkgPath string, info changedSymbolsInfo) bool {
	pkgDir := a.symbolAnalyzer.GetPackageDir(changedPkgPath)

	// Parse the aggregator package to find fx.Options variables and their referenced providers
	referencedProviders := a.extractReferencedProviders(pkgDir, info.symbols)

	if len(referencedProviders) == 0 {
		// If we can't determine which providers are referenced, fall back to checking
		// if the resource uses any of the aggregator's direct dependencies
		return false
	}

	// For each referenced provider package, check if the resource uses its provided interfaces
	allDeps := a.graph.GetAllDeps(resource.Package)
	packagesToCheck := []string{resource.Package}
	for _, dep := range allDeps {
		if strings.HasPrefix(dep, resource.Package+"/") {
			packagesToCheck = append(packagesToCheck, dep)
		}
	}

	for _, providerPkg := range referencedProviders {
		// Get the interface that this provider provides
		providerDir := a.symbolAnalyzer.GetPackageDir(providerPkg)
		returnTypes := a.symbolAnalyzer.GetFactoryReturnTypes(providerDir, []string{"New"})

		if len(returnTypes) == 0 {
			continue
		}

		// Find where these interfaces are defined
		interfacePackages := a.findInterfaceDefinitionPackages(providerDir, returnTypes)

		// Check if the resource uses any of these interfaces
		for _, pkg := range packagesToCheck {
			checkPkgDir := a.symbolAnalyzer.GetPackageDir(pkg)

			for interfacePkg, interfaceNames := range interfacePackages {
				usesInterface, _ := a.diAnalyzer.CheckTypeUsage(checkPkgDir, interfacePkg, interfaceNames)
				if usesInterface {
					return true
				}

				usesSymbol, _ := a.symbolAnalyzer.CheckSymbolUsage(checkPkgDir, interfacePkg, interfaceNames)
				if usesSymbol {
					return true
				}
			}
		}
	}

	return false
}

// extractReferencedProviders extracts provider package paths referenced in fx.Options variables
func (a *Analyzer) extractReferencedProviders(pkgDir string, changedSymbols []string) []string {
	var providers []string

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return providers
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(pkgDir, entry.Name())
		file, err := parser.ParseFile(fset, filePath, nil, 0)
		if err != nil {
			continue
		}

		// Build import map: alias -> full path
		importMap := make(map[string]string)
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				parts := strings.Split(path, "/")
				alias = parts[len(parts)-1]
			}
			importMap[alias] = path
		}

		// Find variable declarations (fx.Options)
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}

			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				// Check if this is one of the changed symbols
				isChanged := len(changedSymbols) == 0 // If no specific symbols, check all
				for _, name := range valueSpec.Names {
					for _, changedSym := range changedSymbols {
						if name.Name == changedSym {
							isChanged = true
							break
						}
					}
				}

				if !isChanged {
					continue
				}

				// Extract provider references from the value (fx.Options(...))
				for _, value := range valueSpec.Values {
					a.extractProvidersFromExpr(value, importMap, &providers)
				}
			}
		}
	}

	return uniqueStrings(providers)
}

// extractProvidersFromExpr recursively extracts provider package references from an expression
func (a *Analyzer) extractProvidersFromExpr(expr ast.Expr, importMap map[string]string, providers *[]string) {
	switch e := expr.(type) {
	case *ast.CallExpr:
		// fx.Options(...) or fx.Provide(...)
		for _, arg := range e.Args {
			a.extractProvidersFromExpr(arg, importMap, providers)
		}
	case *ast.SelectorExpr:
		// pkg.Provider or pkg.New
		if ident, ok := e.X.(*ast.Ident); ok {
			if pkgPath, ok := importMap[ident.Name]; ok {
				// Only include if it's a provider package (contains "provider" in path)
				if strings.Contains(pkgPath, "provider") || e.Sel.Name == "Provider" {
					*providers = append(*providers, pkgPath)
				}
			}
		}
	case *ast.Ident:
		// Local reference (e.g., repository.Provider within the same package)
		// Skip for now as it's internal
	}
}

// findInterfaceDefinitionPackages finds the packages that define the given interface types
// Returns a map of package path -> interface names
func (a *Analyzer) findInterfaceDefinitionPackages(providerPkgDir string, interfaceNames []string) map[string][]string {
	result := make(map[string][]string)

	entries, err := os.ReadDir(providerPkgDir)
	if err != nil {
		return result
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(providerPkgDir, entry.Name())
		file, err := parser.ParseFile(fset, filePath, nil, 0)
		if err != nil {
			continue
		}

		// Build import map: alias -> full path
		importMap := make(map[string]string)
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				parts := strings.Split(path, "/")
				alias = parts[len(parts)-1]
			}
			importMap[alias] = path
		}

		// Find function declarations that return the target interface types
		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Type.Results == nil {
				continue
			}

			for _, resultField := range funcDecl.Type.Results.List {
				// Check if the return type is one of our interface names
				switch t := resultField.Type.(type) {
				case *ast.SelectorExpr:
					// pkg.Type
					if ident, ok := t.X.(*ast.Ident); ok {
						typeName := t.Sel.Name
						for _, ifaceName := range interfaceNames {
							if typeName == ifaceName {
								if pkgPath, ok := importMap[ident.Name]; ok {
									result[pkgPath] = append(result[pkgPath], typeName)
								}
							}
						}
					}
				}
			}
		}
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

// isInfrastructureFile checks if a file is an infrastructure file
// Infrastructure files are common files that many resources depend on,
// but changes to them should only affect resources that use the specific changed symbols
func (a *Analyzer) isInfrastructureFile(filePath string) bool {
	// Normalize the file path
	normalizedPath := filepath.ToSlash(filePath)

	// Remove path prefix if present
	if a.config.PathPrefix != "" {
		normalizedPath = strings.TrimPrefix(normalizedPath, a.config.PathPrefix)
	}

	// Check against configured infrastructure files
	for _, infraFile := range a.config.InfrastructureFiles {
		infraNormalized := filepath.ToSlash(infraFile)
		if normalizedPath == infraNormalized || strings.HasSuffix(normalizedPath, "/"+infraNormalized) {
			return true
		}
	}

	// Default infrastructure file patterns
	// These are auto-generated files that define shared types/infrastructure
	defaultInfraPatterns := []string{
		"sqlc/db.go",
		"sqlc/models.go",
		"sqlc/querier.go",
	}

	for _, pattern := range defaultInfraPatterns {
		if normalizedPath == pattern || strings.HasSuffix(normalizedPath, "/"+pattern) {
			return true
		}
	}

	return false
}
