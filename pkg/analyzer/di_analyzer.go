package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// DIAnalyzer analyzes Uber Fx dependency injection patterns
type DIAnalyzer struct {
	modulePath  string
	projectRoot string
}

// NewDIAnalyzer creates a new DIAnalyzer
func NewDIAnalyzer(modulePath, projectRoot string) *DIAnalyzer {
	return &DIAnalyzer{
		modulePath:  modulePath,
		projectRoot: projectRoot,
	}
}

// DIUsageInfo holds information about DI usage in a package
type DIUsageInfo struct {
	// UsedTypes contains the fully qualified type names that are injected via DI
	UsedTypes []string
	// DirectImports contains packages that are directly imported
	DirectImports []string
}

// AnalyzeDIUsage analyzes a package directory for DI usage patterns
// It looks for:
// 1. Function parameters that receive interface types
// 2. Struct fields that hold interface types
// 3. fx.Invoke and fx.Provide patterns
func (d *DIAnalyzer) AnalyzeDIUsage(pkgDir string) (*DIUsageInfo, error) {
	info := &DIUsageInfo{
		UsedTypes:     []string{},
		DirectImports: []string{},
	}

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(pkgDir, entry.Name())
		file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
		if err != nil {
			continue
		}

		// Collect imports
		importMap := make(map[string]string) // alias -> full path
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				// Use last part of path as default alias
				parts := strings.Split(path, "/")
				alias = parts[len(parts)-1]
			}
			importMap[alias] = path
			info.DirectImports = append(info.DirectImports, path)
		}

		// Analyze function parameters and struct fields
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				// Check function parameters
				if node.Type.Params != nil {
					for _, param := range node.Type.Params.List {
						typeName := d.extractTypeName(param.Type, importMap)
						if typeName != "" {
							info.UsedTypes = append(info.UsedTypes, typeName)
						}
					}
				}
			case *ast.TypeSpec:
				// Check struct fields
				if structType, ok := node.Type.(*ast.StructType); ok {
					if structType.Fields != nil {
						for _, field := range structType.Fields.List {
							typeName := d.extractTypeName(field.Type, importMap)
							if typeName != "" {
								info.UsedTypes = append(info.UsedTypes, typeName)
							}
						}
					}
				}
			}
			return true
		})
	}

	// Remove duplicates
	info.UsedTypes = uniqueStrings(info.UsedTypes)
	info.DirectImports = uniqueStrings(info.DirectImports)

	return info, nil
}

// extractTypeName extracts the fully qualified type name from an AST expression
func (d *DIAnalyzer) extractTypeName(expr ast.Expr, importMap map[string]string) string {
	switch t := expr.(type) {
	case *ast.Ident:
		// Local type
		return t.Name
	case *ast.SelectorExpr:
		// Package.Type
		if ident, ok := t.X.(*ast.Ident); ok {
			pkgAlias := ident.Name
			typeName := t.Sel.Name
			if fullPath, ok := importMap[pkgAlias]; ok {
				return fullPath + "." + typeName
			}
			return pkgAlias + "." + typeName
		}
	case *ast.StarExpr:
		// Pointer type
		return d.extractTypeName(t.X, importMap)
	case *ast.InterfaceType:
		// interface{}
		return ""
	}
	return ""
}

// CheckTypeUsage checks if a package uses a specific type from a target package
func (d *DIAnalyzer) CheckTypeUsage(pkgDir, targetPkg string, typeNames []string) (bool, error) {
	info, err := d.AnalyzeDIUsage(pkgDir)
	if err != nil {
		return false, err
	}

	// Check if any of the used types match
	for _, usedType := range info.UsedTypes {
		for _, targetType := range typeNames {
			// Full match: github.com/org/repo/pkg.Type
			fullTarget := targetPkg + "." + targetType
			if usedType == fullTarget {
				return true, nil
			}
			// Also check if the package is directly imported and type is used
			if strings.HasSuffix(usedType, "."+targetType) && strings.Contains(usedType, targetPkg) {
				return true, nil
			}
		}
	}

	return false, nil
}

// CheckPackageImport checks if a package directly imports a target package
func (d *DIAnalyzer) CheckPackageImport(pkgDir, targetPkg string) (bool, error) {
	info, err := d.AnalyzeDIUsage(pkgDir)
	if err != nil {
		return false, err
	}

	for _, imp := range info.DirectImports {
		if imp == targetPkg {
			return true, nil
		}
	}

	return false, nil
}

// GetInjectedInterfaces returns all interface types that are injected in a package
// This is useful for finding which interfaces from a provider package are actually used
func (d *DIAnalyzer) GetInjectedInterfaces(pkgDir string, providerPkg string) ([]string, error) {
	info, err := d.AnalyzeDIUsage(pkgDir)
	if err != nil {
		return nil, err
	}

	var interfaces []string
	for _, usedType := range info.UsedTypes {
		if strings.HasPrefix(usedType, providerPkg+".") {
			// Extract type name
			typeName := strings.TrimPrefix(usedType, providerPkg+".")
			interfaces = append(interfaces, typeName)
		}
	}

	return interfaces, nil
}
