package analyzer

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

// DiffAnalyzer analyzes git diff to extract changed line information
type DiffAnalyzer struct {
	projectDir string
	baseBranch string
	gitClient  GitClient
}

// NewDiffAnalyzer creates a new DiffAnalyzer
func NewDiffAnalyzer(projectDir, baseBranch string) *DiffAnalyzer {
	return &DiffAnalyzer{
		projectDir: projectDir,
		baseBranch: baseBranch,
		gitClient:  NewGitClient(projectDir, baseBranch),
	}
}

// NewDiffAnalyzerWithClient creates a new DiffAnalyzer with a custom GitClient
func NewDiffAnalyzerWithClient(projectDir, baseBranch string, gitClient GitClient) *DiffAnalyzer {
	return &DiffAnalyzer{
		projectDir: projectDir,
		baseBranch: baseBranch,
		gitClient:  gitClient,
	}
}

// FileChanges represents changes in a single file
type FileChanges struct {
	FilePath     string
	ChangedLines []int // Line numbers that were added or modified
}

// GetChangedLines extracts changed line numbers for a specific file using git diff
func (d *DiffAnalyzer) GetChangedLines(filePath string) ([]int, error) {
	return d.gitClient.GetChangedLines(filePath)
}

// parseUnifiedDiff parses unified diff output and extracts added/modified line numbers
func parseUnifiedDiff(diffOutput string) ([]int, error) {
	var changedLines []int

	// Regex to match hunk headers: @@ -old_start,old_count +new_start,new_count @@
	// We're interested in the new file line numbers (after +)
	hunkRegex := regexp.MustCompile(`@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

	scanner := bufio.NewScanner(strings.NewReader(diffOutput))
	currentNewLine := 0
	inHunk := false

	for scanner.Scan() {
		line := scanner.Text()

		// Check for hunk header
		if matches := hunkRegex.FindStringSubmatch(line); matches != nil {
			startLine, _ := strconv.Atoi(matches[1])
			currentNewLine = startLine
			inHunk = true
			continue
		}

		if !inHunk {
			continue
		}

		// Skip diff metadata lines
		if strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}

		// Added line (starts with +)
		if strings.HasPrefix(line, "+") {
			changedLines = append(changedLines, currentNewLine)
			currentNewLine++
			continue
		}

		// Removed line (starts with -) - don't increment new line counter
		if strings.HasPrefix(line, "-") {
			continue
		}

		// Context line (starts with space) - increment counter but don't record
		if strings.HasPrefix(line, " ") {
			currentNewLine++
			continue
		}
	}

	return changedLines, nil
}

// GetAllChangedLines returns changed lines for multiple files
func (d *DiffAnalyzer) GetAllChangedLines(filePaths []string) (map[string][]int, error) {
	result := make(map[string][]int)

	for _, path := range filePaths {
		lines, err := d.GetChangedLines(path)
		if err != nil {
			continue
		}
		if len(lines) > 0 {
			result[path] = lines
		}
	}

	return result, nil
}
