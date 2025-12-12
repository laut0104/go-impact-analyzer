# go-impact-analyzer

Analyze the impact of code changes on Go projects by tracing dependency chains to identify affected jobs, workers, and services.

## Overview

A CLI tool that analyzes Go codebases to determine which resources (jobs, workers, APIs) are affected by code changes. It traces dependency chains from modified files to identify the blast radius of your changes, helping teams run targeted tests and deployments.

## Installation

```bash
go install github.com/laut0104/go-impact-analyzer/cmd/impact-analyzer@latest
```

## Usage

### Basic Commands

```bash
# Analyze changes from git diff against main branch
impact-analyzer -git-diff

# Analyze changes from git diff against a specific branch
impact-analyzer -git-diff -base=develop

# Analyze specific files
impact-analyzer -files=path/to/file1.go,path/to/file2.go

# Analyze specific packages
impact-analyzer -packages=github.com/org/repo/pkg/foo,github.com/org/repo/pkg/bar

# Read changed files from stdin
echo "path/to/file.go" | impact-analyzer

# List all resources
impact-analyzer -list

# Output in JSON format
impact-analyzer -git-diff -json
```

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `-git-diff` | `false` | Analyze changes from git diff |
| `-base` | `main` | Base branch for git diff comparison |
| `-files` | | Comma-separated list of changed files |
| `-packages` | | Comma-separated list of changed packages |
| `-list` | `false` | List all resources |
| `-json` | `false` | Output in JSON format |
| `-root` | auto-detect | Project root directory |
| `-module` | auto-detect | Go module path |
| `-cmd-dir` | `cli/cmd` | Directory containing CLI command definitions |
| `-path-prefix` | | Path prefix to strip from file paths (e.g., `go/` for monorepo) |

### Example Output

```json
{
  "changed_files": [
    "pkg/service/user.go"
  ],
  "affected_resources": [
    {
      "name": "api-gateway",
      "type": "api",
      "package": "github.com/org/repo/api-gateway",
      "source_file": "/path/to/cli/cmd/api.go",
      "description": "API Gateway service",
      "reason": "depends on github.com/org/repo/pkg/service",
      "affected_package": "github.com/org/repo/pkg/service",
      "dependency_chain": [
        "github.com/org/repo/api-gateway",
        "github.com/org/repo/pkg/service"
      ]
    }
  ],
  "total_resources": 10
}
```

## How It Works

1. **Resource Discovery**: Scans CLI command definitions (using [cobra](https://github.com/spf13/cobra)) to identify jobs, workers, and API services
2. **Dependency Graph**: Builds a complete dependency graph of the Go project
3. **Impact Analysis**: Traces which resources depend on the changed packages (directly or transitively)

## Requirements

- Go 1.23+
- Project must use [cobra](https://github.com/spf13/cobra) for CLI commands
- CLI commands should be defined in a specific directory (default: `cli/cmd`)

### Expected Project Structure

```
your-project/
├── go.mod
├── cli/
│   └── cmd/
│       ├── api.go      # API service commands
│       ├── job.go      # Job commands
│       └── worker.go   # Worker commands
├── pkg/
│   └── ...
└── ...
```

## Use Cases

- **CI/CD**: Run only affected tests and deployments
- **Code Review**: Understand the impact radius of changes
- **Monorepo Management**: Identify which services need to be rebuilt

## GitHub Actions Example

```yaml
name: Impact Analysis

on:
  pull_request:
    branches: [main]

jobs:
  analyze:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'

      - name: Install impact-analyzer
        run: go install github.com/laut0104/go-impact-analyzer/cmd/impact-analyzer@latest

      - name: Analyze impact
        run: impact-analyzer -git-diff -json > impact.json

      - name: Show affected resources
        run: cat impact.json | jq '.affected_resources[].name'
```

## Library Usage

You can also use this as a library:

```go
package main

import (
    "fmt"

    "github.com/laut0104/go-impact-analyzer/pkg/analyzer"
)

func main() {
    cfg := analyzer.Config{
        ModulePath:  "github.com/your-org/your-repo",
        ProjectRoot: "/path/to/project",
        CmdDir:      "cli/cmd",
    }

    a := analyzer.NewAnalyzer(cfg)
    if err := a.Analyze(); err != nil {
        panic(err)
    }

    // Get all resources
    resources := a.GetResources()
    fmt.Printf("Found %d resources\n", len(resources))

    // Analyze impact of changed files
    affected := a.GetAffectedResources([]string{"pkg/service/user.go"})
    for _, r := range affected {
        fmt.Printf("Affected: %s (%s)\n", r.Name, r.Type)
    }
}
```

## License

MIT License
