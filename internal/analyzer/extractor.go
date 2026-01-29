package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ResourceExtractor extracts resources from CLI command definitions
type ResourceExtractor struct {
	modulePath string
	fset       *token.FileSet
	// cmdPackageSuffix is the suffix to match CLI command packages (default: "/cli/cmd")
	cmdPackageSuffix string
	// resourceFileMap maps filename to ResourceType
	resourceFileMap map[string]ResourceType
	// FileSystem for file operations
	fs FileSystem
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

// WithFileSystem sets a custom FileSystem
func WithFileSystem(fs FileSystem) ExtractorOption {
	return func(e *ResourceExtractor) {
		e.fs = fs
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
		fs: NewFileSystem(),
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// ExtractFromDir extracts resources from the specified directory
func (e *ResourceExtractor) ExtractFromDir(dir string) ([]Resource, error) {
	var resources []Resource

	// Parse only the target files (api.go, job.go, worker.go)
	for fileName, resourceType := range e.resourceFileMap {
		filePath := filepath.Join(dir, fileName)

		// Check if file exists
		if _, err := e.fs.Stat(filePath); os.IsNotExist(err) {
			continue
		}

		// Parse the file
		file, err := parser.ParseFile(e.fset, filePath, nil, parser.ParseComments)
		if err != nil {
			continue
		}

		// Build import map
		importMap := e.buildImportMap(file)

		// Extract resources
		extracted := e.extractFromFile(file, importMap, resourceType, filePath)
		resources = append(resources, extracted...)
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
func (e *ResourceExtractor) extractFromFile(file *ast.File, importMap map[string]string, resourceType ResourceType, fileName string) []Resource {
	var resources []Resource

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
