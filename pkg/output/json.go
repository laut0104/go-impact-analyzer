package output

import (
	"encoding/json"
	"io"

	"github.com/laut0104/go-impact-analyzer/pkg/analyzer"
)

// AnalysisResult represents the analysis result
type AnalysisResult struct {
	ChangedPackages   []string                    `json:"changed_packages,omitempty"`
	ChangedFiles      []string                    `json:"changed_files,omitempty"`
	AffectedResources []analyzer.AffectedResource `json:"affected_resources"`
	TotalResources    int                         `json:"total_resources"`
}

// ResourceListResult represents a resource list
type ResourceListResult struct {
	Resources []analyzer.Resource `json:"resources"`
	Total     int                 `json:"total"`
}

// JSONWriter outputs in JSON format
type JSONWriter struct {
	writer io.Writer
	pretty bool
}

// NewJSONWriter creates a new JSONWriter
func NewJSONWriter(w io.Writer, pretty bool) *JSONWriter {
	return &JSONWriter{
		writer: w,
		pretty: pretty,
	}
}

// WriteAnalysisResult outputs analysis result in JSON format
func (j *JSONWriter) WriteAnalysisResult(result *AnalysisResult) error {
	return j.encode(result)
}

// WriteResourceList outputs resource list in JSON format
func (j *JSONWriter) WriteResourceList(resources []analyzer.Resource) error {
	result := ResourceListResult{
		Resources: resources,
		Total:     len(resources),
	}
	return j.encode(result)
}

func (j *JSONWriter) encode(v interface{}) error {
	encoder := json.NewEncoder(j.writer)
	if j.pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(v)
}
