package analyzer

import (
	"io/fs"
	"os"
)

// osFileSystem implements FileSystem using the os package
type osFileSystem struct{}

// NewFileSystem creates a new FileSystem implementation
func NewFileSystem() FileSystem {
	return &osFileSystem{}
}

// ReadDir reads the directory named by path and returns a list of directory entries
func (f *osFileSystem) ReadDir(path string) ([]fs.DirEntry, error) {
	return os.ReadDir(path)
}

// ReadFile reads the named file and returns the contents
func (f *osFileSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Stat returns file info for the named file
func (f *osFileSystem) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}
