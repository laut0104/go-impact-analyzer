package analyzer

import (
	"encoding/json"
	"os/exec"
	"strings"
)

// execGoListClient implements GoListClient using exec.Command
type execGoListClient struct{}

// NewGoListClient creates a new GoListClient implementation
func NewGoListClient() GoListClient {
	return &execGoListClient{}
}

// goListPackage represents the output of go list -json (internal use)
type goListPackage struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
}

// ListPackages returns package information for the given patterns
func (c *execGoListClient) ListPackages(dir string, patterns ...string) ([]PackageInfo, error) {
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var packages []PackageInfo
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err != nil {
			// Skip invalid JSON
			continue
		}
		packages = append(packages, PackageInfo{
			ImportPath: pkg.ImportPath,
			Imports:    pkg.Imports,
		})
	}

	return packages, nil
}
