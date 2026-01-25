package analyzer

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// execGitClient implements GitClient using exec.Command
type execGitClient struct {
	projectDir string
	baseBranch string
}

// NewGitClient creates a new GitClient implementation
func NewGitClient(projectDir, baseBranch string) GitClient {
	return &execGitClient{
		projectDir: projectDir,
		baseBranch: baseBranch,
	}
}

// GetRootDir returns the git repository root directory
func (g *execGitClient) GetRootDir() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = g.projectDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// GetChangedFiles returns list of changed files compared to base branch
func (g *execGitClient) GetChangedFiles(baseBranch string) ([]string, error) {
	gitRoot, err := g.GetRootDir()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("git", "diff", "--name-only", baseBranch+"...HEAD")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		// Fallback: simple diff
		cmd = exec.Command("git", "diff", "--name-only", baseBranch)
		cmd.Dir = gitRoot
		out, err = cmd.Output()
		if err != nil {
			return nil, err
		}
	}

	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

// GetChangedLines returns changed line numbers for a specific file
func (g *execGitClient) GetChangedLines(filePath string) ([]int, error) {
	// Ensure projectDir is absolute
	projectDir := g.projectDir
	if !filepath.IsAbs(projectDir) {
		var err error
		projectDir, err = filepath.Abs(projectDir)
		if err != nil {
			projectDir = g.projectDir
		}
	}

	// Convert to relative path if absolute
	relPath := filePath
	if filepath.IsAbs(filePath) {
		var err error
		relPath, err = filepath.Rel(projectDir, filePath)
		if err != nil {
			relPath = filePath
		}
	}

	// Find git root directory
	gitRoot, err := g.GetRootDir()
	if err != nil {
		// Fallback to projectDir if git root cannot be found
		gitRoot = projectDir
	}

	// Calculate relative path from project dir to git root
	projectRelToGitRoot, err := filepath.Rel(gitRoot, projectDir)
	if err != nil {
		projectRelToGitRoot = ""
	}

	// Build the full relative path from git root
	var gitRelPath string
	if projectRelToGitRoot != "" && projectRelToGitRoot != "." {
		// Check if relPath already starts with the prefix (e.g., "go/")
		// to avoid double prefixing like "go/go/..."
		if strings.HasPrefix(relPath, projectRelToGitRoot+"/") {
			gitRelPath = relPath
		} else {
			gitRelPath = filepath.Join(projectRelToGitRoot, relPath)
		}
	} else {
		gitRelPath = relPath
	}

	// Run git diff to get line-by-line changes
	cmd := exec.Command("git", "diff", "-U0", g.baseBranch+"...HEAD", "--", gitRelPath)
	cmd.Dir = gitRoot

	output, err := cmd.Output()
	if err != nil {
		// If diff fails, return empty (file might be new)
		return nil, nil
	}

	return parseUnifiedDiff(string(output))
}
