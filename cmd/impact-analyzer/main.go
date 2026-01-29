package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/laut0104/go-impact-analyzer/internal/analyzer"
)

// AnalysisResult represents the analysis result
type AnalysisResult struct {
	ChangedPackages   []string                    `json:"changed_packages,omitempty"`
	ChangedFiles      []string                    `json:"changed_files,omitempty"`
	AffectedResources []analyzer.AffectedResource `json:"affected_resources"`
	TotalResources    int                         `json:"total_resources"`
}

func main() {
	// Flag definitions
	var (
		listResources bool
		jsonOutput    bool
		gitDiff       bool
		baseBranch    string
		files         string
		packages      string
		projectRoot   string
		modulePath    string
		cmdDir        string
		pathPrefix    string
	)

	flag.BoolVar(&listResources, "list", false, "List all resources")
	flag.BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	flag.BoolVar(&gitDiff, "git-diff", false, "Analyze changes from git diff")
	flag.StringVar(&baseBranch, "base", "main", "Base branch for git diff comparison")
	flag.StringVar(&files, "files", "", "Comma-separated list of changed files")
	flag.StringVar(&packages, "packages", "", "Comma-separated list of changed packages")
	flag.StringVar(&projectRoot, "root", "", "Project root directory (default: auto-detect)")
	flag.StringVar(&modulePath, "module", "", "Go module path (default: auto-detect from go.mod)")
	flag.StringVar(&cmdDir, "cmd-dir", "cli/cmd", "Directory containing CLI command definitions")
	flag.StringVar(&pathPrefix, "path-prefix", "", "Path prefix to strip from file paths (e.g., 'go/' for monorepo)")
	flag.Parse()

	// Detect project root
	if projectRoot == "" {
		var err error
		projectRoot, err = detectProjectRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to detect project root: %v\n", err)
			os.Exit(1)
		}
	}

	// Detect module path from go.mod if not specified
	if modulePath == "" {
		var err error
		modulePath, err = detectModulePath(projectRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to detect module path: %v\n", err)
			fmt.Fprintf(os.Stderr, "Please specify -module flag\n")
			os.Exit(1)
		}
	}

	// Create Analyzer
	cfg := analyzer.Config{
		ModulePath:  modulePath,
		ProjectRoot: projectRoot,
		CmdDir:      cmdDir,
		PathPrefix:  pathPrefix,
		BaseBranch:  baseBranch,
	}
	a := analyzer.NewAnalyzer(cfg)

	// Run analysis
	fmt.Fprintf(os.Stderr, "Analyzing project at %s...\n", projectRoot)
	if err := a.Analyze(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to analyze: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Found %d resources\n", len(a.GetResources()))

	// Resource list mode
	if listResources {
		if jsonOutput {
			printResourceListJSON(a.GetResources())
		} else {
			printResourceListText(a.GetResources())
		}
		return
	}

	// Get changed files
	var changedFiles []string

	if gitDiff {
		// Use GitClient for git operations
		gitClient := analyzer.NewGitClient(projectRoot, baseBranch)
		allFiles, err := gitClient.GetChangedFiles(baseBranch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to get git diff: %v\n", err)
			os.Exit(1)
		}
		// Filter files by path prefix and .go extension
		for _, file := range allFiles {
			if !strings.HasSuffix(file, ".go") {
				continue
			}
			if pathPrefix != "" && !strings.HasPrefix(file, pathPrefix) {
				continue
			}
			changedFiles = append(changedFiles, file)
		}
	} else if files != "" {
		changedFiles = strings.Split(files, ",")
		for i, f := range changedFiles {
			changedFiles[i] = strings.TrimSpace(f)
		}
	} else if packages != "" {
		// Package specification mode
		pkgList := strings.Split(packages, ",")
		result := &AnalysisResult{
			ChangedPackages:   pkgList,
			AffectedResources: make([]analyzer.AffectedResource, 0),
			TotalResources:    len(a.GetResources()),
		}

		for _, pkg := range pkgList {
			pkg = strings.TrimSpace(pkg)
			affected := a.GetAffectedResourcesByPackage(pkg)
			result.AffectedResources = append(result.AffectedResources, affected...)
		}

		// Remove duplicates
		result.AffectedResources = uniqueAffectedResources(result.AffectedResources)

		printResult(result, jsonOutput)
		return
	} else {
		// Read from stdin
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				changedFiles = append(changedFiles, line)
			}
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
	}

	if len(changedFiles) == 0 {
		fmt.Fprintln(os.Stderr, "No changed files specified")
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  impact-analyzer -git-diff              # Analyze git changes")
		fmt.Fprintln(os.Stderr, "  impact-analyzer -files=file1.go,file2.go")
		fmt.Fprintln(os.Stderr, "  impact-analyzer -packages=pkg1,pkg2")
		fmt.Fprintln(os.Stderr, "  echo 'file.go' | impact-analyzer")
		fmt.Fprintln(os.Stderr, "  impact-analyzer -list                  # List all resources")
		os.Exit(0)
	}

	// Impact analysis
	affected := a.GetAffectedResources(changedFiles)

	result := &AnalysisResult{
		ChangedFiles:      changedFiles,
		AffectedResources: affected,
		TotalResources:    len(a.GetResources()),
	}

	printResult(result, jsonOutput)
}

// printResult outputs analysis result
func printResult(result *AnalysisResult, jsonOutput bool) {
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("=== Impact Analysis Result ===")
	fmt.Println()

	if len(result.ChangedFiles) > 0 {
		fmt.Println("Changed Files:")
		for _, f := range result.ChangedFiles {
			fmt.Printf("  - %s\n", f)
		}
		fmt.Println()
	}

	if len(result.ChangedPackages) > 0 {
		fmt.Println("Changed Packages:")
		for _, p := range result.ChangedPackages {
			fmt.Printf("  - %s\n", p)
		}
		fmt.Println()
	}

	fmt.Printf("Affected Resources (%d):\n", len(result.AffectedResources))
	if len(result.AffectedResources) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, r := range result.AffectedResources {
			fmt.Printf("  [%s] %s\n", r.Type, r.Name)
			fmt.Printf("    Reason: %s\n", r.Reason)
			if len(r.DependencyChain) > 0 {
				fmt.Printf("    Chain: %s\n", strings.Join(r.DependencyChain, " -> "))
			}
		}
	}
}

// printResourceListJSON outputs resource list in JSON format
func printResourceListJSON(resources []analyzer.Resource) {
	result := struct {
		Resources []analyzer.Resource `json:"resources"`
		Total     int                 `json:"total"`
	}{
		Resources: resources,
		Total:     len(resources),
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// printResourceListText outputs resource list in text format
func printResourceListText(resources []analyzer.Resource) {
	fmt.Println("=== Resources ===")
	fmt.Println()

	// Classify by type
	var apiResources, jobResources, workerResources []analyzer.Resource

	for _, r := range resources {
		switch r.Type {
		case analyzer.ResourceTypeAPI:
			apiResources = append(apiResources, r)
		case analyzer.ResourceTypeJob:
			jobResources = append(jobResources, r)
		case analyzer.ResourceTypeWorker:
			workerResources = append(workerResources, r)
		}
	}

	if len(apiResources) > 0 {
		fmt.Printf("API Services (%d):\n", len(apiResources))
		for _, r := range apiResources {
			fmt.Printf("  - %s: %s\n", r.Name, r.Description)
			if r.Package != "" {
				fmt.Printf("    Package: %s\n", r.Package)
			}
		}
		fmt.Println()
	}

	if len(jobResources) > 0 {
		fmt.Printf("Jobs (%d):\n", len(jobResources))
		for _, r := range jobResources {
			fmt.Printf("  - %s: %s\n", r.Name, r.Description)
			if r.Package != "" {
				fmt.Printf("    Package: %s\n", r.Package)
			}
		}
		fmt.Println()
	}

	if len(workerResources) > 0 {
		fmt.Printf("Workers (%d):\n", len(workerResources))
		for _, r := range workerResources {
			fmt.Printf("  - %s: %s\n", r.Name, r.Description)
			if r.Package != "" {
				fmt.Printf("    Package: %s\n", r.Package)
			}
		}
		fmt.Println()
	}

	fmt.Printf("Total: %d resources\n", len(resources))
}

// detectProjectRoot detects the project root
func detectProjectRoot() (string, error) {
	// Search for go.mod from current directory upward
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		gomod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("go.mod not found")
}

// detectModulePath detects the module path from go.mod
func detectModulePath(projectRoot string) (string, error) {
	gomodPath := filepath.Join(projectRoot, "go.mod")
	file, err := os.Open(gomodPath)
	if err != nil {
		return "", fmt.Errorf("failed to open go.mod: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimPrefix(line, "module "), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read go.mod: %w", err)
	}

	return "", fmt.Errorf("module directive not found in go.mod")
}

// uniqueAffectedResources removes duplicates
func uniqueAffectedResources(resources []analyzer.AffectedResource) []analyzer.AffectedResource {
	seen := make(map[string]bool)
	result := make([]analyzer.AffectedResource, 0, len(resources))
	for _, r := range resources {
		if !seen[r.Name] {
			seen[r.Name] = true
			result = append(result, r)
		}
	}
	return result
}
