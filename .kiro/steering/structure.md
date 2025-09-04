# Project Structure & Organization

## Directory Layout

```
crush/
├── main.go                 # Application entry point
├── internal/               # Private application code
│   ├── app/               # Core application logic and coordination
│   ├── cmd/               # CLI command implementations
│   ├── config/            # Configuration management
│   ├── db/                # Database layer (SQLite + sqlc generated)
│   ├── llm/               # LLM integration and AI functionality
│   │   ├── agent/         # AI agent implementations
│   │   ├── prompt/        # Prompt templates and management
│   │   ├── provider/      # LLM provider integrations
│   │   └── tools/         # AI tools and function calling
│   ├── lsp/               # Language Server Protocol integration
│   ├── tui/               # Terminal User Interface components
│   │   ├── components/    # Reusable UI components
│   │   ├── exp/           # Experimental UI components
│   │   └── page/          # Page-level UI components
│   └── [utility packages] # Various utility packages
└── [config files]         # Build and configuration files
```

## Architecture Patterns

### Service Layer Pattern
- Services in packages like `session`, `message`, `history` provide business logic
- Services implement interfaces for testability and modularity
- Services use dependency injection for database and other dependencies

### Event-Driven Architecture
- Uses `internal/pubsub` for event broadcasting
- Services publish events that other components can subscribe to
- TUI subscribes to service events for reactive updates

### Repository Pattern
- Database access abstracted through generated sqlc interfaces
- Raw SQL queries in `internal/db/sql/` directory
- Type-safe database operations via generated Go code

### Component-Based UI
- TUI built with composable Bubble Tea components
- Each component handles its own state and events
- Components communicate via Bubble Tea message passing

## Code Organization Principles

### Package Structure
- `internal/` contains all private application code
- Packages organized by domain/functionality, not by layer
- Shared utilities in focused packages (e.g., `csync`, `fsext`)

### Configuration Management
- JSON-based configuration with schema validation
- Environment variable resolution with `$(echo $VAR)` syntax
- Hierarchical config loading (project → user → defaults)
- Dynamic config updates via `sjson`

### Database Layer
- Migrations in `internal/db/migrations/` with timestamp prefixes
- SQL queries in `internal/db/sql/` organized by domain
- Generated code in `internal/db/` (never edit manually)
- Use `sqlc generate` to regenerate after schema/query changes

### Testing Conventions
- Test files alongside source code (`*_test.go`)
- Golden file testing for UI components in `testdata/` directories
- Table-driven tests for complex scenarios
- Mock interfaces for external dependencies

### Error Handling
- Structured logging with `slog`
- Panic recovery in critical paths (e.g., `log.RecoverPanic`)
- Context-aware error propagation
- Graceful degradation for non-critical failures

## File Naming Conventions

- Go files: `snake_case.go`
- Test files: `*_test.go`
- Golden test files: `*.golden`
- SQL files: `snake_case.sql`
- Migration files: `YYYYMMDDHHMMSS_description.sql`
- Config files: `kebab-case.json/yaml`

## Import Organization

Follow Go conventions with gofumpt formatting:
1. Standard library imports
2. Third-party imports  
3. Local project imports (github.com/charmbracelet/crush/internal/...)

## Key Architectural Components

- **App**: Central coordinator managing all services and lifecycle
- **Config**: Hierarchical configuration with provider/model management
- **Agent**: AI agent implementations with tool calling capabilities
- **TUI**: Bubble Tea-based terminal interface with component architecture
- **LSP**: Language server integration for code context
- **MCP**: Model Context Protocol server integration for extensibility