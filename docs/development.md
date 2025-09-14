# Development Guide

This guide covers development setup, and testing, and contribution guidelines for Spindle.

## Development Installation

**⚠️ IMPORTANT: Spindle requires uv package manager. Standard pip will not work.**

### Prerequisites

Ensure you have the prerequisites from the main README installed:
- uv package manager
- MakeMKV
- drapto

### Local Development Setup

```bash
# Clone the repository
git clone https://github.com/five82/spindle.git
cd spindle

# Install in editable mode with development dependencies
uv pip install -e ".[dev]"
```

The `".[dev]"` syntax means:
- `.` = install the current directory (spindle) in editable mode
- `[dev]` = also install the optional development dependencies (pytest, black, ruff, mypy, etc.)

## Development Workflow

### Running Commands

For development with editable install, use `uv run` - uv automatically manages the virtual environment:

```bash
# Run the application
uv run spindle start

# Run tests
uv run pytest

# Run code quality tools
uv run black src/
uv run ruff check src/
uv run mypy src/

# Run any Python script
uv run python script.py
```

Note: End users who install with `uv tool install` can run `spindle` directly without `uv run`.

### Testing

```bash
# Install dev dependencies first
uv pip install -e ".[dev]"

# Run all tests
uv run pytest

# Run specific test file
uv run pytest tests/storage/test_queue.py

# Run with coverage
uv run pytest --cov=spindle
```

### Code Quality

```bash
# Format code
uv run black src/

# Lint code
uv run ruff check src/

# Type checking
uv run mypy src/

# Fix import sorting
uv run isort src/
```

### Configuration

Development configuration at `~/.config/spindle/config.toml`:
- Use test directories for staging and library
- Set test TMDB API key
- Configure test Plex instance

## Architecture Overview

The project uses a clean modular Python architecture with layered components:

### Core Orchestration
1. **core/daemon.py**: Daemon lifecycle management and process control
2. **core/orchestrator.py**: Main workflow orchestration and component coordination
3. **core/workflow.py**: Workflow state management

### Component Layer
4. **components/disc_handler.py**: Disc processing coordination (identification and ripping)
5. **components/encoder.py**: Video encoding coordination
6. **components/organizer.py**: Library organization coordination

### Service Layer
7. **services/tmdb.py**: TMDB API integration for media identification
8. **services/drapto.py**: Drapto AV1 encoding service wrapper
9. **services/plex.py**: Plex media server integration
10. **services/makemkv.py**: MakeMKV disc ripping service wrapper
11. **services/ntfy.py**: Notification service via ntfy.sh

### Storage Layer
12. **storage/queue.py**: SQLite-based processing queue management
13. **storage/cache.py**: Unified caching system

### Legacy Modules (Preserved)
14. **disc/**: Low-level disc analysis and processing modules
15. **cli.py**: Simplified command-line interface using new core modules
16. **config.py**: TOML-based configuration management
17. **error_handling.py**: Enhanced error handling system

### Key Components

#### Drapto Integration (services/drapto.py)

The encoder wrapper integrates with drapto's JSON progress output system:
- Streams real-time progress events during encoding
- Supports structured progress callbacks with percentages, ETAs, and metrics
- Handles errors and validation results from drapto
- Uses `--json-progress` flag for structured output

#### Queue Management (storage/queue.py)

SQLite-based queue system with:
- Status tracking through processing stages
- Real-time progress field updates (stage, percent, message)
- Database migration support for schema changes
- Thread-safe operations for concurrent access

#### Workflow Orchestration (core/orchestrator.py)

Orchestrates the complete workflow:
- Disc monitoring and automatic ripping
- Background queue processing
- Real-time progress updates and logging
- Error handling and recovery

## Database Migrations

Queue manager handles schema migrations automatically:
- New columns added with ALTER TABLE statements
- Graceful handling of existing databases
- Progress fields: progress_stage, progress_percent, progress_message

## Code Guidelines

1. **Use `uv run` for development commands** - uv manages virtual environments automatically for editable installs
2. Use type hints throughout the codebase
3. Handle errors gracefully with proper logging
4. Use pathlib.Path for file operations
5. Follow async/await patterns for I/O operations
6. Use structured logging with context
7. Test database migrations thoroughly
8. Document configuration requirements

## Testing Strategy

1. **Unit Tests** - Individual component testing
2. **Integration Tests** - Cross-component functionality
3. **Database Tests** - Queue management and migrations
4. **Mock External Services** - TMDB, Plex, drapto for testing
5. **Progress Callback Tests** - Verify JSON progress handling

## Common Development Tasks

### Adding New Progress Event Types

1. Update services/drapto.py progress callback handling
2. Add new progress fields to storage/queue.py if needed
3. Update database schema with migration
4. Add tests for new event handling

### Configuration Changes

1. Update config.py with new fields
2. Add validation and defaults
3. Update config.toml sample
4. Document in README.md

### New Queue Status Types

1. Add to QueueItemStatus enum
2. Update orchestrator workflow logic
3. Add database handling
4. Update status display in CLI

## Debugging

### Logs

- Application logs: `log_dir/spindle.log`
- Queue database: `log_dir/queue.db`
- Drapto output: Captured and parsed as JSON

### Common Issues

1. **uv not found** - Install uv package manager first
2. **Permission errors** - Check file/directory ownership
3. **Drapto not found** - Install from GitHub with cargo
4. **Database lock** - Ensure single spindle instance
5. **Progress not updating** - Verify JSON progress flag and parsing

## Performance Considerations

1. **SQLite WAL mode** - For concurrent queue access
2. **Streaming JSON parsing** - Real-time progress without buffering
3. **Background threads** - Non-blocking progress updates
4. **Resource cleanup** - Proper subprocess and file handle management

## Why uv is Required

Spindle uses uv for:
- Modern Python dependency resolution
- Virtual environment management
- Lock file generation for reproducible builds
- Fast, reliable package installation
- Development workflow standardization

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the code guidelines
4. Add tests for new functionality
5. Run the code quality tools
6. Submit a pull request
