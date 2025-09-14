# AGENTS.md

This file provides guidance when working with code in this repository.

## Project Overview

Spindle is an automated disc ripping, encoding, and media library management system. It provides a complete workflow from physical disc to organized media library with automatic ripping, encoding with drapto (AV1), identification via TMDB, and Plex integration.

### Project Scope and Development Philosophy

**This is a personal project in early development.** Key characteristics:

- **Single user**: No external users or production deployments
- **Early stage**: Active development with frequent architectural changes
- **Breaking changes are beneficial**: Prioritize code quality over compatibility
- **No backwards compatibility required**: Avoid unnecessary complexity from compatibility layers
- **Focus on maintainability**: Clean architecture over legacy support

**Development Guidelines**: Make aggressive improvements to code structure without worrying about backwards compatibility. Remove deprecated patterns immediately rather than maintaining compatibility layers.

## Architecture

The project is a Python application using uv package manager with a clean modular architecture:

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

# Run any Python script
uv run python script.py
```

### CLI Operation and Monitoring

**Daemon-Only Operation:**
Spindle operates exclusively as a daemon - there is no foreground mode. This simplifies the architecture and provides consistent behavior across all environments.

```bash
# Start daemon (runs in background)
uv run spindle start

# Monitor daemon output with colors
uv run spindle show --follow    # Real-time monitoring (like tail -f with colors)
uv run spindle show --lines 50  # Show last 50 log lines

# Check system status
uv run spindle status

# Stop daemon
uv run spindle stop
```

**Log Monitoring:**
The `spindle show` command provides a hybrid approach combining Unix reliability with enhanced UX:
- Uses system `tail` command for robust file streaming
- Adds real-time colorization: ERROR (red), WARNING (yellow), INFO (blue), DEBUG (dim)
- Supports standard tail options: `--follow/-f` and `--lines/-n`
- Handles missing files and commands gracefully

### Why uv is Required

Spindle uses uv for:
- Modern Python dependency resolution
- Virtual environment management
- Lock file generation for reproducible builds
- Fast, reliable package installation
- Development workflow standardization

## Key Components

### Drapto Integration (services/drapto.py)

The encoder wrapper integrates with drapto's JSON progress output system:
- Streams real-time progress events during encoding
- Supports structured progress callbacks with percentages, ETAs, and metrics
- Handles errors and validation results from drapto
- Uses `--json-progress` flag for structured output

**Note on Subtitles**: Subtitle handling is intentionally not implemented in the current version. An AI-generated subtitle feature will be added in a future release.

### Queue Management (storage/queue.py)

SQLite-based queue system with clean workflow separation:
- **Two-Phase Processing**: Identification phase followed by ripping phase
- **Status tracking**: `PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED`
- **Analysis Result Storage**: Persists identification results between phases using `rip_spec_data` field
- **Real-time progress tracking**: Stage, percentage, and detailed messages
- **Database migrations**: Automatic schema evolution with version management
- **Thread-safe operations**: Concurrent access support

### Enhanced Error Handling (error_handling.py)

Comprehensive user-friendly error management system:
- **Phase-specific errors**: Separate handling for identification vs ripping failures
- **Categorized error types**: Configuration, Dependency, Hardware, Media, etc. with specific solutions
- **Rich console display**: Emojis, colors, and actionable guidance
- **Smart error classification**: Pattern-based error detection
- **Recovery guidance**: Clear next steps for common issues
- **Integration across components**: Consistent error handling throughout the system

### Workflow Orchestrator (core/orchestrator.py)

Clean separation of concerns with optimal resource usage:
- **Identification Phase**: Pure content analysis and rip planning
  - Disc scanning and title analysis
  - TMDB-based content identification
  - Intelligent title selection and filename planning
  - Episode mapping for TV shows
- **Ripping Phase**: Execution of predetermined rip plan
  - Rips only selected titles with correct names
  - **Disc ejection on completion** - frees drive for next disc
- **Background Processing**: Non-blocking encoding and organization
  - Encoding, organizing, and Plex import happen in background
  - Multiple discs can be in various processing stages simultaneously
- Enhanced error handling with user-friendly messages

## Optimized Workflow Design

### Complete Processing Pipeline
```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED (disc ejected) → ENCODING (background) → ENCODED (background) → ORGANIZING (background) → COMPLETED (ready in Plex)
```

### Workflow Phase Separation

**Phase 1: Disc-Dependent Operations (Blocking)**
- **PENDING**: Disc detected and queued
- **IDENTIFYING**: Content analysis, TMDB lookup, title selection, filename planning
- **IDENTIFIED**: Rip plan determined, ready to execute
- **RIPPING**: Extract selected titles with predetermined names
- **RIPPED**: ✅ **Disc ejected** - optical drive freed for next disc

**Phase 2: Background Processing (Non-Blocking)**
- **ENCODING**: AV1 encoding with drapto (CPU-intensive, concurrent)
- **ENCODED**: Video compression complete
- **ORGANIZING**: Move to Plex library structure, trigger scan
- **COMPLETED**: ✅ **Media ready to watch in Plex**

### Optimal Resource Usage

**Disc Ejection Behavior**: Discs are ejected immediately after successful ripping completion. This provides:
- **Clear feedback**: Ejection = ready for next disc, no ejection = needs attention
- **Optimal throughput**: Drive freed ASAP for continuous processing
- **Error context**: Failed rips keep disc inserted for retry without reinsertion

**True "Insert and Forget" Experience**:
- **Multiple concurrent workflows**: Several discs can be in different background stages
- **Unattended operation**: ntfy notifications for key milestones (ripped, completed, errors)
- **Maximum efficiency**: Expensive optical drive resource used minimally
- **Remote monitoring**: Full status visibility without physical presence

## Development Workflow

### Implementation Guidelines

**⚠️ CRITICAL: Complete Implementation Required**

When implementing features or fixes, Claude Code must:

1. **Never Skip Implementation Steps** - Complete all requested functionality fully
2. **Never Partially Implement** - Avoid leaving features half-done or marking them as "TODO"
3. **Seek User Approval First** - Before deciding to skip or defer any implementation steps
4. **Ask Before Making Trade-offs** - Don't arbitrarily choose to implement only part of a feature
5. **Complete What You Start** - If you begin implementing something, finish it completely

**Acceptable Exceptions:**
- User explicitly requests partial implementation
- User specifies something as a "future enhancement"
- User provides explicit approval to defer specific steps

**Not Acceptable:**
- Skipping steps for convenience or perceived complexity
- Marking implementation items as TODO without user consent
- Making unilateral decisions about feature scope reduction
- Leaving code in a broken or incomplete state

This ensures consistent, complete implementations that match user expectations and maintain code quality.

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
```

### Configuration

Development configuration at `~/.config/spindle/config.toml`:
- Use test directories for staging and library
- Set test TMDB API key
- Configure test Plex instance

### Database Schema & Migrations

Queue manager handles schema evolution automatically with version-controlled migrations:

**Current Schema (Version 2)**:
- **Core fields**: id, source_path, disc_title, status, media_info_json, ripped_file, encoded_file, final_file
- **Progress tracking**: progress_stage, progress_percent, progress_message (Migration 1)
- **Workflow separation**: rip_spec_data (Migration 2) - stores identification results between phases

**Migration System**:
- **Version tracking**: schema_version table maintains current database version
- **Automatic upgrades**: New columns added via ALTER TABLE statements on startup
- **Graceful fallbacks**: Older databases work seamlessly with missing column handling
- **Data preservation**: All existing queue items and progress retained during upgrades

**Rip Specification Storage** (`rip_spec_data` field):
```json
{
  "analysis_result": {
    "content_type": "movie|tv_series",
    "confidence": 0.95,
    "titles_to_rip": [{"index": 1, "name": "Main Feature", "duration": 7200}],
    "episode_mappings": {"1": {"season_number": 1, "episode_number": 1, "episode_title": "Pilot"}}
  },
  "disc_info": {"label": "MOVIE_TITLE", "device": "/dev/sr0"},
  "is_multi_disc": false
}
```

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

- **Comprehensive Test Files**: Each covering a distinct functional area including error handling
- **Essential Test Coverage**: Concentrated on critical workflow paths and user experience
- **Integration-Focused**: All major components tested with focus on integration over units

### Test File Structure

1. **test_config.py** - Configuration loading, validation, directory creation
2. **test_queue.py** - Complete workflow lifecycle (PENDING → COMPLETED)
3. **test_disc_processing.py** - Disc detection to ripped files workflow
4. **test_identification.py** - TMDB integration and metadata handling
5. **test_enhanced_identification.py** - Advanced identification with caching and title selection
6. **test_encoding.py** - drapto wrapper and progress tracking
7. **test_organization.py** - Library organization and Plex integration
8. **test_cli.py** - Command-line interface, daemon-only operation, and show command functionality
9. **test_error_handling.py** - Enhanced error system with user-friendly messages
10. **test_rip_spec.py** - Disc processing specifications and data structures
11. **test_simple_multi_disc.py** - Multi-disc handling and series detection

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

## Common Development Tasks

### Adding New Workflow Phases

When extending the processing pipeline:

1. **Add new status** to `QueueItemStatus` enum in `storage/queue.py`
2. **Update orchestrator logic** in `core/orchestrator.py`:
   - Add new method for phase (e.g., `_new_phase_item()`)
   - Update `_get_next_processable_item()` to include new status
   - Update `_process_single_item()` workflow routing
3. **Database migration** (if storing new data):
   - Add migration method `_migration_00X_add_new_field()`
   - Update migration list in `_apply_migrations()`
   - Update `update_item()` and `_row_to_item()` methods
4. **Update CLI display** in `cli.py` for new status visualization
5. **Add tests** covering the new workflow stage

### Modifying Identification Logic

For changes to content analysis and title selection:

1. **Update `disc/analyzer.py`** for new identification patterns
2. **Modify `rip_spec_data` structure** in `_complete_disc_identification()`
3. **Update database schema** if storing additional analysis data
4. **Extend `_reconstruct_rip_spec_from_item()`** to handle new data
5. **Add TMDB API integration** if new metadata fields needed

### Adding Progress Event Types

1. Update services/drapto.py progress callback handling
2. Add new progress fields to storage/queue.py if needed
3. Update database schema with migration if persistent storage needed
4. Add tests for new event handling

### Configuration Changes

1. Update config.py with new fields and validation
2. Add defaults and documentation
3. Update sample config.toml
4. Test configuration loading and validation

### CLI Development Patterns

**Daemon-Only Architecture:**
- All CLI commands assume daemon operation (no foreground mode)
- Use `--systemd` flag for systemd service compatibility only
- Commands either start/stop daemon or interact with running daemon

**Adding New CLI Commands:**
1. **Add command to `cli.py`** with proper Click decorators
2. **Follow daemon interaction pattern** - commands should work with running daemon
3. **Add comprehensive tests to `test_cli.py`**:
   - Help text verification
   - Option parsing and validation
   - Error handling (missing files, dependencies)
   - Integration with configuration system
   - Mock external dependencies (subprocess, file system)

**CLI Testing Best Practices:**
- **User behavior focus**: Test what users experience, not implementation details
- **Mock external calls**: subprocess, file operations, system dependencies
- **Integration testing**: Configuration loading, error handling, command coordination
- **Error scenarios**: Missing files, failed dependencies, invalid options

## Debugging

### Logs

- Application logs: `log_dir/spindle.log`
- Queue database: `log_dir/queue.db`
- Drapto output: Captured and parsed as JSON

**Log Monitoring Commands:**
```bash
# Real-time log monitoring with colors
uv run spindle show --follow

# View last 50 log lines
uv run spindle show --lines 50

# Check daemon status
uv run spindle status
```

### Common Issues

1. **uv not found** - Install uv package manager first
2. **Permission errors** - Check file/directory ownership
3. **Drapto not found** - Install from GitHub with cargo
4. **Database lock** - Ensure single spindle instance
5. **Progress not updating** - Verify JSON progress flag and parsing
6. **Disc not ejecting** - Check if ripping completed successfully (only successful rips eject disc)
7. **Stuck in IDENTIFYING** - TMDB API issues or disc not properly mounted
8. **Missing rip_spec_data** - Database migration may not have run; restart application

### Workflow Debugging

**Phase-specific debugging**:
- **IDENTIFYING phase**: Check TMDB connectivity, disc mount points, and MakeMKV scan results
- **RIPPING phase**: Verify `rip_spec_data` contains valid title selection and analysis results
- **Background phases**: Monitor separate encoding/organization processes, check drapto availability

**Database inspection**:
```bash
# View queue items with all fields
sqlite3 ~/.local/share/spindle/logs/queue.db "SELECT id, disc_title, status, progress_stage, progress_percent FROM queue_items;"

# Check rip specification data
sqlite3 ~/.local/share/spindle/logs/queue.db "SELECT disc_title, rip_spec_data FROM queue_items WHERE rip_spec_data IS NOT NULL;"

# Monitor workflow transitions
sqlite3 ~/.local/share/spindle/logs/queue.db "SELECT disc_title, status, updated_at FROM queue_items ORDER BY updated_at DESC LIMIT 10;"
```

### Error Handling

Phase-aware error handling with user-friendly messages:
- **Identification errors**: TMDB connectivity, disc mounting, content recognition
- **Ripping errors**: MakeMKV issues, disc read problems, storage space
- **Background errors**: drapto failures, Plex connectivity, file organization issues
- **Rich console display**: Categorized errors with emojis, colors, and specific recovery guidance

## Performance Considerations

1. **SQLite WAL mode** - For concurrent queue access
2. **Streaming JSON parsing** - Real-time progress without buffering
3. **Background threads** - Non-blocking progress updates
4. **Resource cleanup** - Proper subprocess and file handle management

Remember: Always use `uv run` for all commands (e.g., `uv run spindle start`, `uv run pytest`). uv handles virtual environment management automatically.

## Pre-Commit CI Validation

**⚠️ CRITICAL: Always run local CI checks before committing to prevent GitHub Actions failures.**

### Simplified CI Pipeline

The project uses a streamlined, practical CI approach focused on essential quality checks:

```bash
# Run all essential CI checks locally (mirrors GitHub Actions)
./check-ci.sh
```

### Essential Checks Only

The simplified pipeline runs these core checks:

1. **Tests with Coverage**: `pytest tests/ -v --cov=spindle --cov-report=xml --cov-report=term`
2. **Code Formatting**: `black --check src/`
3. **Linting**: `ruff check src/`
4. **Package Build**: `uv build` + `twine check dist/*`

**Removed Complexity**: MyPy, isort, bandit, pip-audit, and codecov integration were removed as unnecessary overhead for a personal project in early development.

### Individual Commands

If you need to run specific checks:

```bash
# Fix formatting issues
uv run black src/

# Fix linting issues
uv run ruff check src/ --fix

# Run tests
uv run pytest tests/ -v
```

### Development Workflow

1. Make code changes
2. Run `./check-ci.sh` to verify essential checks pass
3. Fix any issues reported by the script
4. Only commit and push when script shows "🎉 All essential checks passed!"

This simplified approach prioritizes rapid development iteration while maintaining code quality, perfectly suited for the project's scope and development philosophy.

This prevents GitHub Actions CI failures and ensures consistent code quality.
