package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
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
	// FileSystem for file operations
	fs FileSystem
}

// NewSymbolAnalyzer creates a new SymbolAnalyzer
func NewSymbolAnalyzer(modulePath, projectDir string) *SymbolAnalyzer {
	return &SymbolAnalyzer{
		fset:         token.NewFileSet(),
		modulePath:   modulePath,
		projectDir:   projectDir,
		fileSymbols:  make(map[string][]string),
		packageFiles: make(map[string][]string),
		fs:           NewFileSystem(),
	}
}

// NewSymbolAnalyzerWithFS creates a new SymbolAnalyzer with a custom FileSystem
func NewSymbolAnalyzerWithFS(modulePath, projectDir string, fs FileSystem) *SymbolAnalyzer {
	return &SymbolAnalyzer{
		fset:         token.NewFileSet(),
		modulePath:   modulePath,
		projectDir:   projectDir,
		fileSymbols:  make(map[string][]string),
		packageFiles: make(map[string][]string),
		fs:           fs,
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
	entries, err := s.fs.ReadDir(pkgDir)
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

// FunctionRange represents the line range of a function or method
type FunctionRange struct {
	Name      string
	StartLine int
	EndLine   int
}

// ExtractFunctionRanges extracts all exported function/method ranges from a Go file
func (s *SymbolAnalyzer) ExtractFunctionRanges(filePath string) ([]FunctionRange, error) {
	file, err := parser.ParseFile(s.fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var ranges []FunctionRange

	ast.Inspect(file, func(n ast.Node) bool {
		funcDecl, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		if funcDecl.Name == nil || !isExported(funcDecl.Name.Name) {
			return true
		}

		startPos := s.fset.Position(funcDecl.Pos())
		endPos := s.fset.Position(funcDecl.End())

		ranges = append(ranges, FunctionRange{
			Name:      funcDecl.Name.Name,
			StartLine: startPos.Line,
			EndLine:   endPos.Line,
		})

		return true
	})

	return ranges, nil
}

// ExtractTypeRanges extracts all exported type declaration ranges from a Go file
func (s *SymbolAnalyzer) ExtractTypeRanges(filePath string) ([]FunctionRange, error) {
	file, err := parser.ParseFile(s.fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var ranges []FunctionRange

	ast.Inspect(file, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if !isExported(typeSpec.Name.Name) {
				continue
			}

			startPos := s.fset.Position(genDecl.Pos())
			endPos := s.fset.Position(genDecl.End())

			ranges = append(ranges, FunctionRange{
				Name:      typeSpec.Name.Name,
				StartLine: startPos.Line,
				EndLine:   endPos.Line,
			})
		}

		return true
	})

	return ranges, nil
}

// ExtractConstantRanges extracts all exported constant/variable declaration ranges from a Go file
func (s *SymbolAnalyzer) ExtractConstantRanges(filePath string) ([]FunctionRange, error) {
	file, err := parser.ParseFile(s.fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var ranges []FunctionRange

	ast.Inspect(file, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}

		// Only process const and var declarations
		if genDecl.Tok != token.CONST && genDecl.Tok != token.VAR {
			return true
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for _, name := range valueSpec.Names {
				if !isExported(name.Name) {
					continue
				}

				// Use the spec's position for individual constants within a block
				startPos := s.fset.Position(valueSpec.Pos())
				endPos := s.fset.Position(valueSpec.End())

				ranges = append(ranges, FunctionRange{
					Name:      name.Name,
					StartLine: startPos.Line,
					EndLine:   endPos.Line,
				})
			}
		}

		return true
	})

	return ranges, nil
}

// GetChangedSymbols returns the exported symbols that were modified based on changed line numbers
func (s *SymbolAnalyzer) GetChangedSymbols(filePath string, changedLines []int) ([]string, error) {
	if len(changedLines) == 0 {
		return nil, nil
	}

	// Get function ranges
	funcRanges, err := s.ExtractFunctionRanges(filePath)
	if err != nil {
		return nil, err
	}

	// Get type ranges
	typeRanges, err := s.ExtractTypeRanges(filePath)
	if err != nil {
		return nil, err
	}

	// Get constant/variable ranges
	constRanges, err := s.ExtractConstantRanges(filePath)
	if err != nil {
		return nil, err
	}

	allRanges := append(funcRanges, typeRanges...)
	allRanges = append(allRanges, constRanges...)

	// Find which symbols are affected by the changed lines
	changedSymbols := make(map[string]bool)
	for _, line := range changedLines {
		for _, r := range allRanges {
			if line >= r.StartLine && line <= r.EndLine {
				changedSymbols[r.Name] = true
			}
		}
	}

	// Convert to slice
	result := make([]string, 0, len(changedSymbols))
	for sym := range changedSymbols {
		result = append(result, sym)
	}

	return result, nil
}

// InterfaceMethodRange represents the line range of an interface method
type InterfaceMethodRange struct {
	InterfaceName string
	MethodName    string
	StartLine     int
	EndLine       int
}

// ExtractInterfaceMethodRanges extracts all interface method ranges from a Go file
func (s *SymbolAnalyzer) ExtractInterfaceMethodRanges(filePath string) ([]InterfaceMethodRange, error) {
	file, err := parser.ParseFile(s.fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var ranges []InterfaceMethodRange

	ast.Inspect(file, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}

			if !isExported(typeSpec.Name.Name) {
				continue
			}

			// Extract methods from interface
			for _, method := range interfaceType.Methods.List {
				if len(method.Names) == 0 {
					continue
				}
				methodName := method.Names[0].Name
				if !isExported(methodName) {
					continue
				}

				startPos := s.fset.Position(method.Pos())
				endPos := s.fset.Position(method.End())

				ranges = append(ranges, InterfaceMethodRange{
					InterfaceName: typeSpec.Name.Name,
					MethodName:    methodName,
					StartLine:     startPos.Line,
					EndLine:       endPos.Line,
				})
			}
		}

		return true
	})

	return ranges, nil
}

// FindInterfacesWithMethods finds interfaces in a file that contain methods with the given names
// This is used to detect when a concrete method implementation changes, we should also
// consider the interfaces that define those methods as affected
func (s *SymbolAnalyzer) FindInterfacesWithMethods(filePath string, methodNames []string) ([]InterfaceMethodRange, error) {
	if len(methodNames) == 0 {
		return nil, nil
	}

	// Build a set of method names for quick lookup
	methodNameSet := make(map[string]bool)
	for _, name := range methodNames {
		methodNameSet[name] = true
	}

	// Get all interface methods from the file
	allInterfaceMethods, err := s.ExtractInterfaceMethodRanges(filePath)
	if err != nil {
		return nil, err
	}

	// Find interfaces that have methods matching the changed method names
	var matchingMethods []InterfaceMethodRange
	for _, im := range allInterfaceMethods {
		if methodNameSet[im.MethodName] {
			matchingMethods = append(matchingMethods, im)
		}
	}

	return matchingMethods, nil
}

// GetChangedInterfaceMethods returns the interface methods that were modified based on changed line numbers
func (s *SymbolAnalyzer) GetChangedInterfaceMethods(filePath string, changedLines []int) ([]InterfaceMethodRange, error) {
	if len(changedLines) == 0 {
		return nil, nil
	}

	methodRanges, err := s.ExtractInterfaceMethodRanges(filePath)
	if err != nil {
		return nil, err
	}

	// Find which methods are affected by the changed lines
	var changedMethods []InterfaceMethodRange
	changedSet := make(map[string]bool)

	for _, line := range changedLines {
		for _, m := range methodRanges {
			key := m.InterfaceName + "." + m.MethodName
			if line >= m.StartLine && line <= m.EndLine && !changedSet[key] {
				changedMethods = append(changedMethods, m)
				changedSet[key] = true
			}
		}
	}

	return changedMethods, nil
}

// CheckMethodCallUsage checks if a package calls any of the given interface methods
func (s *SymbolAnalyzer) CheckMethodCallUsage(pkgDir string, targetPkgPath string, methods []InterfaceMethodRange) (bool, error) {
	if len(methods) == 0 {
		return false, nil
	}

	// Build method set for quick lookup (just method names, since we check calls)
	methodSet := make(map[string]bool)
	for _, m := range methods {
		methodSet[m.MethodName] = true
	}

	// Get target package alias from its path
	targetPkgName := filepath.Base(targetPkgPath)

	// Parse all Go files in the package directory
	entries, err := s.fs.ReadDir(pkgDir)
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

		// Only check files that import the target package
		// This prevents false positives from methods with the same name on different interfaces
		if importAlias == "" || importAlias == "_" {
			continue
		}

		// Check for method calls
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			if found {
				return false
			}

			// Check for method call: obj.MethodName(...)
			callExpr, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := callExpr.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			// Check if calling a method that matches our changed methods
			if methodSet[sel.Sel.Name] {
				// Direct package access: pkg.MethodName(...)
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == importAlias {
					found = true
					return false
				}
				// Method call on variable (interface implementations)
				// Since we confirmed this file imports the target package,
				// we can assume the method call is related to that package
				found = true
				return false
			}

			return true
		})

		if found {
			return true, nil
		}
	}

	return false, nil
}

// ChangedSymbolInfo contains information about changed symbols including interface methods
type ChangedSymbolInfo struct {
	// Regular symbols (functions, types, etc.)
	Symbols []string
	// Changed interface methods
	InterfaceMethods []InterfaceMethodRange
	// HasUnexportedChanges indicates if unexported functions/methods were modified
	HasUnexportedChanges bool
}

// GetChangedSymbolsDetailed returns detailed information about changed symbols including interface methods
func (s *SymbolAnalyzer) GetChangedSymbolsDetailed(filePath string, changedLines []int) (*ChangedSymbolInfo, error) {
	if len(changedLines) == 0 {
		return &ChangedSymbolInfo{}, nil
	}

	// Get regular changed symbols
	symbols, err := s.GetChangedSymbols(filePath, changedLines)
	if err != nil {
		return nil, err
	}

	// Get changed interface methods (from interface definitions)
	methods, err := s.GetChangedInterfaceMethods(filePath, changedLines)
	if err != nil {
		return nil, err
	}

	// Find interfaces that have methods matching the changed symbols
	// This handles the case where a concrete method implementation changes
	// and we need to mark the interface as affected too
	if len(symbols) > 0 {
		interfaceMethodsFromImpl, err := s.FindInterfacesWithMethods(filePath, symbols)
		if err == nil && len(interfaceMethodsFromImpl) > 0 {
			// Merge with existing methods, avoiding duplicates
			existingKeys := make(map[string]bool)
			for _, m := range methods {
				existingKeys[m.InterfaceName+"."+m.MethodName] = true
			}
			for _, m := range interfaceMethodsFromImpl {
				key := m.InterfaceName + "." + m.MethodName
				if !existingKeys[key] {
					methods = append(methods, m)
					existingKeys[key] = true
				}
			}
		}
	}

	// Check if any unexported symbols were changed
	hasUnexportedChanges, err := s.HasUnexportedChanges(filePath, changedLines)
	if err != nil {
		hasUnexportedChanges = false
	}

	// If unexported functions were changed, add all exported symbols from the same file
	// because exported functions may call the changed unexported functions
	if hasUnexportedChanges {
		allExported, err := s.ExtractExportedSymbols(filePath)
		if err == nil {
			// Add all exported symbols to the changed symbols list
			symbolSet := make(map[string]bool)
			for _, sym := range symbols {
				symbolSet[sym] = true
			}
			for _, sym := range allExported {
				if !symbolSet[sym] {
					symbols = append(symbols, sym)
				}
			}
		}
	}

	// Keep interface names in symbols list since they represent type changes
	// that affect code using the interface type
	// (previously removed them, but we need to keep them for proper impact analysis)

	return &ChangedSymbolInfo{
		Symbols:              symbols,
		InterfaceMethods:     methods,
		HasUnexportedChanges: hasUnexportedChanges,
	}, nil
}

// ExtractAllExportedSymbolsFromDir extracts all exported symbols from all Go files in a directory
func (s *SymbolAnalyzer) ExtractAllExportedSymbolsFromDir(pkgDir string) ([]string, error) {
	entries, err := s.fs.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}

	var allSymbols []string
	seen := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(pkgDir, entry.Name())
		symbols, err := s.ExtractExportedSymbols(filePath)
		if err != nil {
			continue
		}

		for _, sym := range symbols {
			if !seen[sym] {
				seen[sym] = true
				allSymbols = append(allSymbols, sym)
			}
		}
	}

	return allSymbols, nil
}

// CheckSymbolUsesSymbols checks if a specific symbol in a package uses any of the target symbols from another package
func (s *SymbolAnalyzer) CheckSymbolUsesSymbols(pkgDir string, targetPkgPath string, targetSymbols []string, symbolName string) (bool, error) {
	if len(targetSymbols) == 0 {
		return false, nil
	}

	// Build symbol set for quick lookup
	symbolSet := make(map[string]bool)
	for _, sym := range targetSymbols {
		symbolSet[sym] = true
	}

	// Get target package alias from its path
	targetPkgName := filepath.Base(targetPkgPath)

	// Parse all Go files in the package directory
	entries, err := s.fs.ReadDir(pkgDir)
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

		// Find the symbol (function, method, or type) in this file
		var symbolNode ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.FuncDecl:
				if decl.Name != nil && decl.Name.Name == symbolName {
					symbolNode = decl
					return false
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.Name == symbolName {
							symbolNode = decl
							return false
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.Name == symbolName {
								symbolNode = decl
								return false
							}
						}
					}
				}
			}
			return true
		})

		if symbolNode == nil {
			continue
		}

		// Check if the symbol uses any of the target symbols
		found := false
		ast.Inspect(symbolNode, func(n ast.Node) bool {
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

// HasUnexportedChanges checks if any unexported functions/methods were modified
func (s *SymbolAnalyzer) HasUnexportedChanges(filePath string, changedLines []int) (bool, error) {
	if len(changedLines) == 0 {
		return false, nil
	}

	file, err := parser.ParseFile(s.fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	// Build a set of changed lines for quick lookup
	changedLineSet := make(map[int]bool)
	for _, line := range changedLines {
		changedLineSet[line] = true
	}

	hasUnexported := false

	ast.Inspect(file, func(n ast.Node) bool {
		if hasUnexported {
			return false
		}

		funcDecl, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}

		// Skip exported functions
		if funcDecl.Name != nil && isExported(funcDecl.Name.Name) {
			return true
		}

		// Check if this unexported function has changes
		startPos := s.fset.Position(funcDecl.Pos())
		endPos := s.fset.Position(funcDecl.End())

		for line := startPos.Line; line <= endPos.Line; line++ {
			if changedLineSet[line] {
				hasUnexported = true
				return false
			}
		}

		return true
	})

	return hasUnexported, nil
}

// GetFactoryReturnTypes extracts return types from factory functions (functions that return interfaces)
func (s *SymbolAnalyzer) GetFactoryReturnTypes(pkgDir string, functionNames []string) []string {
	entries, err := s.fs.ReadDir(pkgDir)
	if err != nil {
		return nil
	}

	funcSet := make(map[string]bool)
	for _, fn := range functionNames {
		funcSet[fn] = true
	}

	var returnTypes []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		filePath := filepath.Join(pkgDir, entry.Name())
		file, err := parser.ParseFile(s.fset, filePath, nil, 0)
		if err != nil {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			funcDecl, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}

			// Check if this is one of the target functions
			if funcDecl.Name == nil || !funcSet[funcDecl.Name.Name] {
				return true
			}

			// Get return types
			if funcDecl.Type.Results != nil {
				for _, result := range funcDecl.Type.Results.List {
					// Extract type name
					typeName := extractTypeName(result.Type)
					if typeName != "" && isExported(typeName) {
						returnTypes = append(returnTypes, typeName)
					}
				}
			}

			return true
		})
	}

	return returnTypes
}

// extractTypeName extracts the type name from an ast.Expr
func extractTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		// Pointer type - get the underlying type
		return extractTypeName(t.X)
	case *ast.SelectorExpr:
		// Qualified type (pkg.Type) - return just the type name
		return t.Sel.Name
	default:
		return ""
	}
}

// CheckSymbolUsesInterfaceMethods checks if a specific symbol in a package calls any of the target interface methods
func (s *SymbolAnalyzer) CheckSymbolUsesInterfaceMethods(pkgDir string, targetPkgPath string, methods []InterfaceMethodRange, symbolName string) (bool, error) {
	if len(methods) == 0 {
		return false, nil
	}

	// Build method set for quick lookup
	methodSet := make(map[string]bool)
	for _, m := range methods {
		methodSet[m.MethodName] = true
	}

	// Parse all Go files in the package directory
	entries, err := s.fs.ReadDir(pkgDir)
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

		// Find the symbol (function or method) in this file
		var symbolNode ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			switch decl := n.(type) {
			case *ast.FuncDecl:
				if decl.Name != nil && decl.Name.Name == symbolName {
					symbolNode = decl
					return false
				}
			}
			return true
		})

		if symbolNode == nil {
			continue
		}

		// Check if the symbol calls any of the target methods
		found := false
		ast.Inspect(symbolNode, func(n ast.Node) bool {
			if found {
				return false
			}

			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Check for method call: x.MethodName()
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			// Check if the method name matches
			if methodSet[sel.Sel.Name] {
				found = true
				return false
			}

			return true
		})

		if found {
			return true, nil
		}
	}

	return false, nil
}
