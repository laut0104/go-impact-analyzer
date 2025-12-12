package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/laut0104/go-impact-analyzer/pkg/analyzer"
)

// TextWriter outputs in text format
type TextWriter struct {
	writer io.Writer
}

// NewTextWriter creates a new TextWriter
func NewTextWriter(w io.Writer) *TextWriter {
	return &TextWriter{writer: w}
}

// WriteAnalysisResult outputs analysis result in text format
func (t *TextWriter) WriteAnalysisResult(result *AnalysisResult) error {
	var sb strings.Builder

	sb.WriteString("=== Impact Analysis Result ===\n\n")

	if len(result.ChangedFiles) > 0 {
		sb.WriteString("Changed Files:\n")
		for _, f := range result.ChangedFiles {
			sb.WriteString(fmt.Sprintf("  - %s\n", f))
		}
		sb.WriteString("\n")
	}

	if len(result.ChangedPackages) > 0 {
		sb.WriteString("Changed Packages:\n")
		for _, p := range result.ChangedPackages {
			sb.WriteString(fmt.Sprintf("  - %s\n", p))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("Affected Resources (%d):\n", len(result.AffectedResources)))
	if len(result.AffectedResources) == 0 {
		sb.WriteString("  (none)\n")
	} else {
		for _, r := range result.AffectedResources {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", r.Type, r.Name))
			sb.WriteString(fmt.Sprintf("    Reason: %s\n", r.Reason))
			if len(r.DependencyChain) > 0 {
				sb.WriteString(fmt.Sprintf("    Chain: %s\n", strings.Join(r.DependencyChain, " -> ")))
			}
		}
	}

	_, err := t.writer.Write([]byte(sb.String()))
	return err
}

// WriteResourceList outputs resource list in text format
func (t *TextWriter) WriteResourceList(resources []analyzer.Resource) error {
	var sb strings.Builder

	sb.WriteString("=== Resources ===\n\n")

	// Classify by type
	apiResources := make([]analyzer.Resource, 0)
	jobResources := make([]analyzer.Resource, 0)
	workerResources := make([]analyzer.Resource, 0)

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
		sb.WriteString(fmt.Sprintf("API Services (%d):\n", len(apiResources)))
		for _, r := range apiResources {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", r.Name, r.Description))
			if r.Package != "" {
				sb.WriteString(fmt.Sprintf("    Package: %s\n", r.Package))
			}
		}
		sb.WriteString("\n")
	}

	if len(jobResources) > 0 {
		sb.WriteString(fmt.Sprintf("Jobs (%d):\n", len(jobResources)))
		for _, r := range jobResources {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", r.Name, r.Description))
			if r.Package != "" {
				sb.WriteString(fmt.Sprintf("    Package: %s\n", r.Package))
			}
		}
		sb.WriteString("\n")
	}

	if len(workerResources) > 0 {
		sb.WriteString(fmt.Sprintf("Workers (%d):\n", len(workerResources)))
		for _, r := range workerResources {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", r.Name, r.Description))
			if r.Package != "" {
				sb.WriteString(fmt.Sprintf("    Package: %s\n", r.Package))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("Total: %d resources\n", len(resources)))

	_, err := t.writer.Write([]byte(sb.String()))
	return err
}
