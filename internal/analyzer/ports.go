package analyzer

import "io/fs"

// GitClient abstracts git operations for testability
type GitClient interface {
	// GetChangedFiles returns list of changed files compared to base branch
	GetChangedFiles(baseBranch string) ([]string, error)
	// GetChangedLines returns changed line numbers for a specific file
	GetChangedLines(filePath string) ([]int, error)
	// GetRootDir returns the git repository root directory
	GetRootDir() (string, error)
}

// GoListClient abstracts go list command for testability
type GoListClient interface {
	// ListPackages returns package information for the given patterns
	ListPackages(dir string, patterns ...string) ([]PackageInfo, error)
}

// PackageInfo represents information about a Go package
type PackageInfo struct {
	ImportPath string
	Imports    []string
}

// FileSystem abstracts file system operations for testability
type FileSystem interface {
	// ReadDir reads the directory named by path and returns a list of directory entries
	ReadDir(path string) ([]fs.DirEntry, error)
	// ReadFile reads the named file and returns the contents
	ReadFile(path string) ([]byte, error)
	// Stat returns file info for the named file
	Stat(path string) (fs.FileInfo, error)
}
