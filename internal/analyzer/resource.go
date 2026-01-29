package analyzer

// ResourceType represents the type of resource
type ResourceType string

const (
	ResourceTypeAPI    ResourceType = "api"
	ResourceTypeJob    ResourceType = "job"
	ResourceTypeWorker ResourceType = "worker"
)

// Resource represents a CLI command (service/job/worker)
type Resource struct {
	Name        string       `json:"name"`        // Command name (e.g., "api-gateway", "update-price")
	Type        ResourceType `json:"type"`        // "api", "job", "worker"
	Package     string       `json:"package"`     // Direct dependency package (e.g., "github.com/.../job/update-price")
	SourceFile  string       `json:"source_file"` // Source file where defined
	Description string       `json:"description"` // Command description (Short)
}

// AffectedResource represents information about an affected resource
type AffectedResource struct {
	Resource
	Reason          string   `json:"reason"`           // Reason for being affected
	AffectedPackage string   `json:"affected_package"` // Package causing the impact
	DependencyChain []string `json:"dependency_chain"` // Dependency chain
}
