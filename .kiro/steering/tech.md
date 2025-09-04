# Technology Stack

## Core Technologies

- **Language**: Go 1.25.0 with experimental greenteagc garbage collector
- **Database**: SQLite with ncruces/go-sqlite3 driver
- **Database Migrations**: Goose v3 for schema management
- **SQL Generation**: sqlc for type-safe database queries
- **TUI Framework**: Bubble Tea v2 (charmbracelet/bubbletea) for terminal interface
- **CLI Framework**: Cobra for command-line interface
- **Configuration**: JSON-based with sjson for dynamic updates

## Key Dependencies

- **AI/LLM Integration**: 
  - OpenAI Go SDK
  - Anthropic SDK Go
  - Google Generative AI
  - AWS SDK v2 (Bedrock)
  - Azure SDK
- **Terminal/UI**: 
  - Lipgloss v2 for styling
  - Glamour v2 for markdown rendering
  - Bubble Tea components
- **Development Tools**:
  - LSP protocol implementation
  - Model Context Protocol (MCP) support via mark3labs/mcp-go
  - File watching with fsnotify

## Build System & Commands

### Task Runner
Uses Taskfile (task) for build automation. Key commands:

```bash
# Development
task dev          # Run with profiling enabled
task build        # Build the application
task install      # Install to GOPATH/bin

# Code Quality
task lint         # Run golangci-lint
task lint-fix     # Run linters with auto-fix
task fmt          # Format code with gofumpt
task test         # Run all tests

# Database
task schema       # Generate JSON schema for configuration

# Profiling
task profile:cpu    # 10s CPU profile
task profile:heap   # Heap profile
task profile:allocs # Allocations profile
```

### Direct Go Commands
```bash
# Build
go build .

# Test
go test ./...

# Install
go install .

# Run with profiling
CRUSH_PROFILE=true go run .
```

## Environment Configuration

- **CGO_ENABLED**: 0 (disabled for static builds)
- **GOEXPERIMENT**: greenteagc (experimental garbage collector)
- **GOTOOLCHAIN**: go1.25.0 for lint installation

## Database Schema

- Uses SQLite with migrations in `internal/db/migrations/`
- SQL queries in `internal/db/sql/`
- Generated Go code in `internal/db/` via sqlc
- Supports sessions, messages, and file history tracking