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

### Disc Mounting Requirements

**Desktop Systems**: Automatic disc mounting is handled by desktop environments (GNOME, KDE, etc.) - no additional configuration needed.

**Server Systems**: Configure automounting via fstab:
```bash
sudo mkdir -p /media/cdrom
echo '/dev/sr0 /media/cdrom udf,iso9660 ro,auto 0 0' | sudo tee -a /etc/fstab
```

Spindle expects mounted discs at standard locations (`/media/cdrom` or `/media/cdrom0`).

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

**Disc Ejection Behavior**: Discs are only ejected upon successful completion of ripping. Failed rips do NOT eject the disc, allowing users to retry or investigate the issue without having to reinsert the disc. This provides clear feedback that ejection = success, no ejection = needs attention.

**"Insert and Forget" Workflow**: The combination of disc ejection behavior and ntfy notifications creates an optimal unattended processing experience:
- **Success**: Disc auto-ejects + notification confirms completion → ready for next disc
- **Failure**: Disc remains in drive + ntfy alert explains what went wrong → can retry remotely or investigate later
- Users maintain context about which disc failed without needing to reinsert for retry
- Remote notifications eliminate the need to manually check status, enabling true "insert and forget" operation

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
6. **Automounting** - System-level disc mounting (desktop environment or fstab)

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

**Essential Test Suite (Clean Slate Approach)**

The project uses a focused, essential test suite designed to validate user-facing behavior rather than implementation details:

- **Test-to-Source Ratio**: 0.30:1 (~2,080 test lines vs 6,933 source lines)
- **7 Focused Files**: Each covering a distinct functional area
- **95+ Essential Tests**: Concentrated on critical workflow paths

### Test File Structure

1. **test_config.py** (129 lines, 8 tests) - Configuration loading, validation, directory creation
2. **test_queue.py** (179 lines, 8 tests) - Complete workflow lifecycle (PENDING → COMPLETED)
3. **test_disc_processing.py** (287 lines, 18 tests) - Disc detection to ripped files workflow
4. **test_identification.py** (265 lines, 17 tests) - TMDB integration and metadata handling
5. **test_encoding.py** (225 lines, 15 tests) - drapto wrapper and progress tracking
6. **test_organization.py** (275 lines, 18 tests) - Library organization and Plex integration
7. **test_cli.py** (280 lines, 18 tests) - Command-line interface and workflow coordination

### Testing Philosophy

- **User Behavior Focus** - Test what users experience, not internal implementation
- **Essential Coverage Only** - Critical paths and error conditions, avoiding redundant edge cases
- **Integration Over Units** - Validate component interactions rather than isolated functions
- **Mock External Services** - TMDB, Plex, drapto, MakeMKV for testing
- **Real Database Operations** - Use actual SQLite for queue testing with temp directories

### Test Maintenance

- **Clean Slate Design** - Right-sized from the start, avoiding over-engineering
- **Focused Scope** - Each test validates specific user-facing functionality
- **Minimal Redundancy** - No duplicate coverage of the same behavior patterns
- **Fast Execution** - Essential tests run quickly for rapid development feedback

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

## Pre-Commit CI Validation

**⚠️ CRITICAL: Always run local CI checks before committing to prevent GitHub Actions failures.**

### Quick CI Check

Use the provided script that mirrors the exact CI pipeline:

```bash
# Run all CI checks locally (same as GitHub Actions)
./check-ci.sh
```

This script runs the exact same checks as `.github/workflows/ci.yml`:

1. **Tests with Coverage**: `pytest tests/ -v --cov=spindle --cov-report=xml --cov-report=term`
2. **Code Formatting**: `black --check src/`
3. **Linting**: `ruff check src/`
4. **Type Checking**: `mypy src/`
5. **Import Sorting**: `isort --check-only src/`
6. **Security Scan**: `bandit -r src/ -ll` (warnings continue on error)
7. **Vulnerability Check**: `pip-audit` (warnings continue on error)
8. **Package Build**: `uv build` + `twine check dist/*`

### Individual Commands

If you need to run specific checks:

```bash
# Fix formatting issues
uv run black src/
uv run isort src/

# Fix linting issues  
uv run ruff check src/ --fix

# Check type errors
uv run mypy src/

# Run tests
uv run pytest tests/ -v
```

### Workflow

1. Make code changes
2. Run `./check-ci.sh` to verify all CI checks pass
3. Fix any issues reported by the script
4. Only commit and push when script shows "🎉 All CI checks passed!"

This prevents GitHub Actions CI failures and ensures consistent code quality.