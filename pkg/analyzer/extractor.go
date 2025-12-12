package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// ResourceExtractor extracts resources from CLI command definitions
type ResourceExtractor struct {
	modulePath string
	fset       *token.FileSet
	// cmdPackageSuffix is the suffix to match CLI command packages (default: "/cli/cmd")
	cmdPackageSuffix string
	// resourceFileMap maps filename to ResourceType
	resourceFileMap map[string]ResourceType
}

// ExtractorOption is a function that configures ResourceExtractor
type ExtractorOption func(*ResourceExtractor)

// WithCmdPackageSuffix sets a custom CLI command package suffix
func WithCmdPackageSuffix(suffix string) ExtractorOption {
	return func(e *ResourceExtractor) {
		e.cmdPackageSuffix = suffix
	}
}

// WithResourceFileMap sets a custom mapping from filename to ResourceType
func WithResourceFileMap(m map[string]ResourceType) ExtractorOption {
	return func(e *ResourceExtractor) {
		e.resourceFileMap = m
	}
}

// NewResourceExtractor creates a new ResourceExtractor
func NewResourceExtractor(modulePath string, opts ...ExtractorOption) *ResourceExtractor {
	e := &ResourceExtractor{
		modulePath:       modulePath,
		fset:             token.NewFileSet(),
		cmdPackageSuffix: "/cli/cmd",
		resourceFileMap: map[string]ResourceType{
			"api.go":    ResourceTypeAPI,
			"job.go":    ResourceTypeJob,
			"worker.go": ResourceTypeWorker,
		},
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// ExtractFromDir extracts resources from the specified directory
func (e *ResourceExtractor) ExtractFromDir(dir string) ([]Resource, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedImports | packages.NeedFiles,
		Dir:  dir,
		Fset: e.fset,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}

	var resources []Resource

	for _, pkg := range pkgs {
		// Only target cmd packages
		if !strings.HasSuffix(pkg.PkgPath, e.cmdPackageSuffix) {
			continue
		}

		// Build import alias to path mapping
		for _, file := range pkg.Syntax {
			importMap := e.buildImportMap(file)
			extracted := e.extractFromFile(file, importMap, pkg.GoFiles)
			resources = append(resources, extracted...)
		}
	}

	return resources, nil
}

// buildImportMap builds alias -> package path mapping from import declarations
func (e *ResourceExtractor) buildImportMap(file *ast.File) map[string]string {
	importMap := make(map[string]string)

	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)

		// Use alias if present, otherwise use package name
		var alias string
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			// Use last element of path as package name
			parts := strings.Split(path, "/")
			alias = parts[len(parts)-1]
			// Cannot use if contains hyphen
			if strings.Contains(alias, "-") {
				alias = ""
			}
		}

		if alias != "" && alias != "_" && alias != "." {
			importMap[alias] = path
		}
	}

	return importMap
}

// extractFromFile extracts resources from a file
func (e *ResourceExtractor) extractFromFile(file *ast.File, importMap map[string]string, goFiles []string) []Resource {
	var resources []Resource

	// Determine resource type from filename
	fileName := e.fset.Position(file.Pos()).Filename
	resourceType := e.detectResourceType(fileName)

	if resourceType == "" {
		return resources
	}

	ast.Inspect(file, func(n ast.Node) bool {
		// Look for &cobra.Command{...}
		unary, ok := n.(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			return true
		}

		compLit, ok := unary.X.(*ast.CompositeLit)
		if !ok {
			return true
		}

		// Check if cobra.Command type
		if !e.isCobraCommand(compLit) {
			return true
		}

		// Extract resource info from fields
		resource := e.extractResourceFromCompositeLit(compLit, importMap, resourceType, fileName)
		if resource != nil {
			resources = append(resources, *resource)
		}

		return true
	})

	return resources
}

// detectResourceType determines resource type from filename
func (e *ResourceExtractor) detectResourceType(fileName string) ResourceType {
	base := filepath.Base(fileName)
	if rt, ok := e.resourceFileMap[base]; ok {
		return rt
	}
	return ""
}

// isCobraCommand checks if CompositeLit is a cobra.Command type
func (e *ResourceExtractor) isCobraCommand(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return ident.Name == "cobra" && sel.Sel.Name == "Command"
}

// extractResourceFromCompositeLit extracts resource info from CompositeLit
func (e *ResourceExtractor) extractResourceFromCompositeLit(
	lit *ast.CompositeLit,
	importMap map[string]string,
	resourceType ResourceType,
	sourceFile string,
) *Resource {
	var name, description, pkg string

	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}

		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}

		switch key.Name {
		case "Use":
			// Use: "command-name" or Use: "command-name [args]"
			if basicLit, ok := kv.Value.(*ast.BasicLit); ok && basicLit.Kind == token.STRING {
				useValue := strings.Trim(basicLit.Value, `"`)
				// Command name is before space
				parts := strings.SplitN(useValue, " ", 2)
				name = parts[0]
			}

		case "Short":
			if basicLit, ok := kv.Value.(*ast.BasicLit); ok && basicLit.Kind == token.STRING {
				description = strings.Trim(basicLit.Value, `"`)
			}

		case "RunE":
			// Identify package called from RunE
			pkg = e.extractPackageFromRunE(kv.Value, importMap)
		}
	}

	if name == "" {
		return nil
	}

	return &Resource{
		Name:        name,
		Type:        resourceType,
		Package:     pkg,
		SourceFile:  sourceFile,
		Description: description,
	}
}

// extractPackageFromRunE identifies the package called from RunE field
func (e *ResourceExtractor) extractPackageFromRunE(expr ast.Expr, importMap map[string]string) string {
	var pkg string

	ast.Inspect(expr, func(n ast.Node) bool {
		// Look for package.Run(...) pattern
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Look for Run or RunWorkerPool method calls
		if sel.Sel.Name != "Run" && sel.Sel.Name != "RunWorkerPool" {
			return true
		}

		// Get package alias
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		// Get package path from importMap
		if path, exists := importMap[ident.Name]; exists {
			pkg = path
			return false // Stop searching when found
		}

		return true
	})

	return pkg
}

// ExtractImportedPackages extracts job/API related packages imported from CLI command files
func (e *ResourceExtractor) ExtractImportedPackages(dir string) (map[string][]string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedImports,
		Dir:  dir,
		Fset: e.fset,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}

	result := make(map[string][]string)

	for _, pkg := range pkgs {
		if !strings.HasSuffix(pkg.PkgPath, e.cmdPackageSuffix) {
			continue
		}

		for _, file := range pkg.Syntax {
			fileName := e.fset.Position(file.Pos()).Filename
			base := filepath.Base(fileName)

			// Only target resource files
			if _, ok := e.resourceFileMap[base]; !ok {
				continue
			}

			var imports []string
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				// Only packages within the project
				if strings.HasPrefix(path, e.modulePath) {
					imports = append(imports, path)
				}
			}
			result[base] = imports
		}
	}

	return result, nil
}
