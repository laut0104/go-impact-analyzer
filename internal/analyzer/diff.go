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

// DiffResult contains both added and deleted line information
type DiffResult struct {
	AddedLines   []int // Line numbers in the new file that were added/modified
	DeletedLines []int // Line numbers in the old file that were deleted
}

// GetChangedLines extracts changed line numbers for a specific file using git diff
func (d *DiffAnalyzer) GetChangedLines(filePath string) ([]int, error) {
	return d.gitClient.GetChangedLines(filePath)
}

// GetChangedLinesWithDeleted extracts both added and deleted line numbers for a specific file
func (d *DiffAnalyzer) GetChangedLinesWithDeleted(filePath string) (*DiffResult, error) {
	return d.gitClient.GetChangedLinesWithDeleted(filePath)
}

// parseUnifiedDiff parses unified diff output and extracts added/modified line numbers
func parseUnifiedDiff(diffOutput string) ([]int, error) {
	result, err := parseUnifiedDiffWithDeleted(diffOutput)
	if err != nil {
		return nil, err
	}
	return result.AddedLines, nil
}

// parseUnifiedDiffWithDeleted parses unified diff output and extracts both added and deleted line numbers
func parseUnifiedDiffWithDeleted(diffOutput string) (*DiffResult, error) {
	result := &DiffResult{}

	// Regex to match hunk headers: @@ -old_start,old_count +new_start,new_count @@
	// We need both old and new line numbers
	hunkRegex := regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

	scanner := bufio.NewScanner(strings.NewReader(diffOutput))
	currentOldLine := 0
	currentNewLine := 0
	inHunk := false

	for scanner.Scan() {
		line := scanner.Text()

		// Check for hunk header
		if matches := hunkRegex.FindStringSubmatch(line); matches != nil {
			oldStart, _ := strconv.Atoi(matches[1])
			newStart, _ := strconv.Atoi(matches[3])
			currentOldLine = oldStart
			currentNewLine = newStart
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
			result.AddedLines = append(result.AddedLines, currentNewLine)
			currentNewLine++
			continue
		}

		// Removed line (starts with -) - track old line number
		if strings.HasPrefix(line, "-") {
			result.DeletedLines = append(result.DeletedLines, currentOldLine)
			currentOldLine++
			continue
		}

		// Context line (starts with space) - increment both counters but don't record
		if strings.HasPrefix(line, " ") {
			currentOldLine++
			currentNewLine++
			continue
		}
	}

	return result, nil
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
