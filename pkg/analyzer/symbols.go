package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// SymbolAnalyzer analyzes file-level symbol dependencies
type SymbolAnalyzer struct {
	fset       *token.FileSet
	modulePath string
	projectDir string
	// File path -> exported symbols (functions, types, variables, constants)
	fileSymbols map[string][]string
	// Package path -> file paths within the package
	packageFiles map[string][]string
}

// NewSymbolAnalyzer creates a new SymbolAnalyzer
func NewSymbolAnalyzer(modulePath, projectDir string) *SymbolAnalyzer {
	return &SymbolAnalyzer{
		fset:         token.NewFileSet(),
		modulePath:   modulePath,
		projectDir:   projectDir,
		fileSymbols:  make(map[string][]string),
		packageFiles: make(map[string][]string),
	}
}

// ExtractExportedSymbols extracts exported symbols from a Go file
func (s *SymbolAnalyzer) ExtractExportedSymbols(filePath string) ([]string, error) {
	// Check cache
	if symbols, ok := s.fileSymbols[filePath]; ok {
		return symbols, nil
	}

	file, err := parser.ParseFile(s.fset, filePath, nil, 0)
	if err != nil {
		return nil, err
	}

	var symbols []string

	ast.Inspect(file, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			// Function or method
			if decl.Name != nil && isExported(decl.Name.Name) {
				symbols = append(symbols, decl.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					// Type declaration
					if isExported(s.Name.Name) {
						symbols = append(symbols, s.Name.Name)
					}
				case *ast.ValueSpec:
					// Variable or constant declaration
					for _, name := range s.Names {
						if isExported(name.Name) {
							symbols = append(symbols, name.Name)
						}
					}
				}
			}
		}
		return true
	})

	s.fileSymbols[filePath] = symbols
	return symbols, nil
}

// isExported checks if a name is exported (starts with uppercase)
func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// CheckSymbolUsage checks if a package uses any of the given symbols from another package
func (s *SymbolAnalyzer) CheckSymbolUsage(pkgDir string, targetPkgPath string, symbols []string) (bool, error) {
	if len(symbols) == 0 {
		return false, nil
	}

	// Build symbol set for quick lookup
	symbolSet := make(map[string]bool)
	for _, sym := range symbols {
		symbolSet[sym] = true
	}

	// Get target package alias from its path
	targetPkgName := filepath.Base(targetPkgPath)

	// Parse all Go files in the package directory
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(pkgDir, entry.Name())
		file, err := parser.ParseFile(s.fset, filePath, nil, 0)
		if err != nil {
			continue
		}

		// Find the import alias for the target package
		importAlias := ""
		for _, imp := range file.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			if impPath == targetPkgPath {
				if imp.Name != nil {
					importAlias = imp.Name.Name
				} else {
					importAlias = targetPkgName
				}
				break
			}
		}

		if importAlias == "" || importAlias == "_" {
			continue
		}

		// Check if any of the symbols are used
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}

			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}

			// Check if it's accessing the target package
			if ident.Name == importAlias {
				// Check if the accessed symbol is in our list
				if symbolSet[sel.Sel.Name] {
					found = true
					return false
				}
			}

			return true
		})

		if found {
			return true, nil
		}
	}

	return false, nil
}

// GetPackageDir returns the directory for a package path
func (s *SymbolAnalyzer) GetPackageDir(pkgPath string) string {
	// Remove module path prefix to get relative path
	relPath := strings.TrimPrefix(pkgPath, s.modulePath)
	relPath = strings.TrimPrefix(relPath, "/")
	return filepath.Join(s.projectDir, relPath)
}

// FileToPackagePath converts a file path to its package path
func (s *SymbolAnalyzer) FileToPackagePath(filePath string) string {
	// Get directory
	dir := filepath.Dir(filePath)

	// Convert to relative path from project root
	relDir, err := filepath.Rel(s.projectDir, dir)
	if err != nil {
		return ""
	}

	if relDir == "." {
		return s.modulePath
	}

	return s.modulePath + "/" + filepath.ToSlash(relDir)
}
