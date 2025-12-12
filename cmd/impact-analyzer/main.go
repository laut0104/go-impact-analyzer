package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/laut0104/go-impact-analyzer/pkg/analyzer"
	"github.com/laut0104/go-impact-analyzer/pkg/output"
)

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
			writer := output.NewJSONWriter(os.Stdout, true)
			if err := writer.WriteResourceList(a.GetResources()); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			writer := output.NewTextWriter(os.Stdout)
			if err := writer.WriteResourceList(a.GetResources()); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	// Get changed files
	var changedFiles []string

	if gitDiff {
		var err error
		// Get git repository root for git diff
		gitRoot, err := getGitRoot(projectRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to detect git root: %v\n", err)
			os.Exit(1)
		}
		changedFiles, err = getGitDiffFiles(gitRoot, baseBranch, pathPrefix)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to get git diff: %v\n", err)
			os.Exit(1)
		}
	} else if files != "" {
		changedFiles = strings.Split(files, ",")
		for i, f := range changedFiles {
			changedFiles[i] = strings.TrimSpace(f)
		}
	} else if packages != "" {
		// Package specification mode
		pkgList := strings.Split(packages, ",")
		result := &output.AnalysisResult{
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

		outputResult(result, jsonOutput)
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

	result := &output.AnalysisResult{
		ChangedFiles:      changedFiles,
		AffectedResources: affected,
		TotalResources:    len(a.GetResources()),
	}

	outputResult(result, jsonOutput)
}

func outputResult(result *output.AnalysisResult, jsonOutput bool) {
	if jsonOutput {
		writer := output.NewJSONWriter(os.Stdout, true)
		if err := writer.WriteAnalysisResult(result); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		writer := output.NewTextWriter(os.Stdout)
		if err := writer.WriteAnalysisResult(result); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
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

// getGitRoot finds the git repository root
func getGitRoot(startDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// getGitDiffFiles gets changed file list from git diff
func getGitDiffFiles(gitRoot, baseBranch, pathPrefix string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", baseBranch+"...HEAD")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		// Fallback: simple diff
		cmd = exec.Command("git", "diff", "--name-only", baseBranch)
		cmd.Dir = gitRoot
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff failed: %w", err)
		}
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasSuffix(line, ".go") {
			continue
		}
		// Filter by path prefix if specified (e.g., only include files under "go/")
		if pathPrefix != "" {
			if !strings.HasPrefix(line, pathPrefix) {
				continue
			}
		}
		files = append(files, line)
	}
	return files, nil
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
