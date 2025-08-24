# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Spindle is an automated disc ripping, encoding, and media library management system. It provides a complete workflow from physical disc to organized media library with automatic ripping, encoding with drapto (AV1), identification via TMDB, and Plex integration.

## Architecture

The project is a Python application using uv package manager with modular components:

1. **disc/**: Optical disc detection, monitoring, and MakeMKV ripping
2. **identify/**: TMDB-based media identification and metadata handling
3. **encode/**: drapto wrapper for AV1 video encoding with real-time progress
4. **organize/**: Plex-compatible file organization and library import
5. **queue/**: SQLite-based processing queue with status tracking
6. **notify/**: ntfy.sh integration for real-time notifications
7. **config/**: TOML-based configuration management

## Development Requirements

**⚠️ CRITICAL: This project REQUIRES uv package manager. Standard pip will NOT work.**

### Installation & Setup

```bash
# Install uv first (REQUIRED)
curl -LsSf https://astral.sh/uv/install.sh | sh

# For end users - install as global tool (recommended)
uv tool install git+https://github.com/five82/spindle.git

# For development - install in development mode
uv pip install -e ".[dev]"
```

### Running Commands

For end users with `uv tool install`:
```bash
# Run the application directly
spindle start
```

For development with `uv pip install -e ".[dev]"`, use `uv run`:
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

### Why uv is Required

Spindle uses uv for:
- Modern Python dependency resolution
- Virtual environment management
- Lock file generation for reproducible builds
- Fast, reliable package installation
- Development workflow standardization

## Key Components

### Drapto Integration (encode/drapto_wrapper.py)

The encoder wrapper integrates with drapto's JSON progress output system:
- Streams real-time progress events during encoding
- Supports structured progress callbacks with percentages, ETAs, and metrics
- Handles errors and validation results from drapto
- Uses `--json-progress` flag for structured output

**Note on Subtitles**: Subtitle handling is intentionally not implemented in the current version. An AI-generated subtitle feature will be added in a future release.

### Queue Management (queue/manager.py)

SQLite-based queue system with:
- Status tracking through processing stages
- Real-time progress field updates (stage, percent, message)
- Database migration support for schema changes
- Thread-safe operations for concurrent access

### Continuous Processing (processor.py)

Orchestrates the complete workflow:
- Disc monitoring and automatic ripping
- Background queue processing
- Real-time progress updates and logging
- Error handling and recovery

## Development Workflow

### Testing

```bash
# Install dev dependencies first
uv pip install -e ".[dev]"

# Run all tests
uv run pytest

# Run specific test file
uv run pytest tests/test_queue.py

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

### Database Migrations

Queue manager handles schema migrations automatically:
- New columns added with ALTER TABLE statements
- Graceful handling of existing databases
- Progress fields: progress_stage, progress_percent, progress_message

## Integration Points

### External Dependencies

1. **MakeMKV** - Disc ripping (makemkvcon binary)
2. **drapto** - Video encoding (Rust binary with JSON progress)
3. **TMDB API** - Media identification
4. **Plex API** - Library management
5. **ntfy.sh** - Notifications

### File System Structure

```
staging_dir/
├── ripped/          # Raw MakeMKV output
├── encoded/         # Drapto encoded output
└── temp/           # Temporary processing files

library_dir/
├── Movies/         # Plex-compatible movie structure
└── TV Shows/       # Plex-compatible TV structure

review_dir/
└── unidentified/   # Manual review required
```

## Code Guidelines

1. **Always use `uv run` for commands** - uv manages virtual environments automatically
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

## Common Tasks

### Adding New Progress Event Types

1. Update drapto_wrapper.py progress callback handling
2. Add new progress fields to queue/manager.py if needed
3. Update database schema with migration
4. Add tests for new event handling

### Configuration Changes

1. Update config.py with new fields
2. Add validation and defaults
3. Update config.toml sample
4. Document in README.md

### New Queue Status Types

1. Add to QueueItemStatus enum
2. Update processor workflow logic
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

Remember: Always use `uv run` for all commands (e.g., `uv run spindle start`, `uv run pytest`). uv handles virtual environment management automatically.