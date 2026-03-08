# System Design: Daemon and Disc Detection

Daemon lifecycle, disc detection pipeline, and daemon orchestration layer.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Daemon Lifecycle

### 1.1 Startup Sequence

**Construction phase** (in `New()`):
1. Load and validate configuration.
2. Ensure directories exist.
3. Open SQLite queue database (init schema if needed).
4. Create workflow manager with configured poll interval.
5. Create notification service.
6. Create daemon instance with lock file path `filepath.Join(cfg.Paths.LogDir, "spindle.lock")`.
7. Create disc monitor (if optical drive configured).
8. Create netlink monitor (if optical drive configured).
9. Create HTTP API server (Unix socket at `$XDG_RUNTIME_DIR/spindle.sock`,
    optional TCP bind at `api.bind`).

**Start phase** (in `Start()`):
1. **Acquire lock file** via `flock.TryLock()` (fail if another instance running).
2. Run dependency checks (preflight).
3. **Recover stale items**: Reset `in_progress` on any items left in-progress
   from a previous crash (see DESIGN_QUEUE.md Section 5).
4. Start workflow manager (begins pipeline processing loop).
5. Start disc monitor (prepare for detection events). Fatal if fails.
6. Start netlink monitor (begin listening for udev events). Non-fatal if fails.
7. Start HTTP API server (Unix socket + optional TCP). Fatal if fails.
8. Set `running = true` via atomic bool.

### 1.2 Shutdown Sequence

1. Cancel daemon context.
2. Stop netlink monitor.
3. Stop disc monitor.
4. Stop HTTP API server (5-second graceful shutdown).
5. Stop workflow manager (cancels stage contexts; waits for in-flight goroutines).
6. Mark all in-progress queue items as not-in-progress:
   `ResetInProgressOnShutdown()` with a **5-second timeout context**. Items
   retain their current stage so they resume from the correct point on restart.
   Failure here does not prevent shutdown.
7. Release lock file via `d.lock.Unlock()`.
8. Clear context references and set `running = false`.

`Close()` calls `Stop()`, then closes the log archive and queue store.

### 1.3 Lock File

- Path: `{log_dir}/spindle.lock`
- Uses `flock` (file locking) for mutual exclusion.
- Prevents multiple daemon instances.
- Released on clean shutdown.

### 1.4 Disc Pause/Resume

- `PauseDisc()` / `ResumeDisc()` toggle an `atomic.Bool`.
- Uses `CompareAndSwap()` so only actual state changes return true.
- While paused: netlink events are ignored (logged at DEBUG), manual disc detection
  via HTTP API returns "disc detection paused".
- The ripping handler pauses disc monitoring before ripping and resumes it
  after completion (see DESIGN_STAGES.md Section 2.6).

---

## 2. Disc Detection

### 2.1 Netlink Udev Monitor

- `Start()` is non-blocking: validates device, creates netlink connection, spawns
  monitor goroutine, returns immediately.
- Connection failure is non-fatal: daemon runs with manual detection only.
- `monitorLoop()` selects on: `ctx.Done` (shutdown), `quit` (explicit stop),
  `queue` (matched udev events), `errs` (netlink errors).
- Matches events with: `SUBSYSTEM=block`, `ID_CDROM=1`, `ID_CDROM_MEDIA=1`,
  `ACTION=change|add`.
- **Device name extraction**: `DEVNAME` env var (primary, e.g., "sr0") ->
  `DEVPATH` split on "/" to get last component, prepend "/dev/" (fallback).
- Filters: only events for configured `optical_drive` device.
- **Pause check**: before invoking handler, checks `isPaused()` callback;
  if paused, logs at DEBUG and drops the event.

### 2.2 Disc Info Gathering

1. Run `lsblk -P -o LABEL,FSTYPE {device}` to get disc label and filesystem type.
2. Run `blkid -p -o export {device}` for more precise filesystem type.
3. Determine disc type:
   - `udf` -> Check for Blu-ray indicators, default to "Blu-ray"
   - `iso9660` -> "DVD"
   - Empty -> Check mount points for BDMV/VIDEO_TS directories
4. Check for Blu-ray vs DVD: Run `file -s {device}` and check mount points
   `/media/cdrom`, `/media/cdrom0` for `BDMV` or `VIDEO_TS` directories.

### 2.3 Disc Monitor

The disc monitor wraps fingerprinting and queue submission with concurrency guards.

- `processing` bool (mutex-protected) prevents concurrent disc detection.
- Fingerprint timeout: **2 minutes** (`fingerprint.ComputeTimeout` with
  `2*time.Minute`).
- On fingerprint failure: notifies via `errorNotifier.FingerprintFailed()` callback.
- Before detection, checks `HasDiscDependentItem()` to avoid concurrent disc access.

### 2.4 Disc Fingerprinting

Compute a deterministic SHA-256 hash of disc filesystem metadata to uniquely
identify each physical disc.

**Mount resolution** (in order):
1. Check `/proc/mounts` for device already mounted (symlink-aware comparison).
2. Check fallback paths (`/media/cdrom`, `/media/cdrom0`) for disc directory structure.
3. Auto-mount via `mount <device>` (fstab provides mount point); auto-unmount after fingerprinting.

**Disc classification** (determines hash strategy):
1. If `discType` hint is provided, use it (accepts "Blu-ray", "DVD", case-insensitive).
2. Otherwise probe mount point: `BDMV/` directory -> Blu-ray, `VIDEO_TS/` -> DVD.

**Blu-ray fingerprint**:
- Collect core files: `BDMV/index.bdmv`, `BDMV/MovieObject.bdmv` (if present).
- Collect all `.mpls` files from `BDMV/PLAYLIST/`.
- Collect all `.clpi` files from `BDMV/CLIPINF/`.
- `CERTIFICATE/id.bdmv` is intentionally excluded -- multi-disc sets share the
  same certificate, causing collisions.
- Sort file list alphabetically, hash full file contents (no size limit).

**DVD fingerprint**:
- Collect all `.ifo` files from `VIDEO_TS/`.
- Sort alphabetically, hash full file contents (no size limit).

**Fallback fingerprint** (unknown disc type or missing metadata):
- Walk entire mount point, collect all files.
- Sort alphabetically, hash first 64 KiB of each file.

**Hash computation** (`hashFileManifest`):
For each file in sorted order, feed into a single SHA-256 hasher:
1. Relative path (forward slashes) + NUL byte
2. File size as decimal string + NUL byte
3. File content (full or limited by maxBytes) + NUL byte

Output: hex-encoded SHA-256 digest.

### 2.5 Title Hashing

Compute a deterministic SHA-256 hash for a single MakeMKV title using stable
attributes. Purpose: identify the same logical content across different disc
pressings (disc fingerprint is intentionally excluded).

**Hash components** (in order, each NUL-terminated):
1. Title name (lowercased, trimmed)
2. Duration (integer seconds)
3. Segment map (trimmed)
4. For each track (sorted by StreamID asc, then Type asc):
   - StreamID, Order, Type, CodecID, CodecShort, CodecLong
   - Language, LanguageName, Name
   - ChannelCount, ChannelLayout, BitRate
   - All MakeMKV attributes (sorted by numeric key)

Output: hex-encoded SHA-256 digest.

### 2.6 Duplicate Detection

- Check queue for existing items with same fingerprint that are `IsInWorkflow()`
  (any non-terminal stage, including in-progress items).
- If found: return existing item ID, do not create duplicate.
- **Identical fingerprints from different disc pressings**: SHA-256 collisions are
  astronomically unlikely, but identical disc pressings (same files) produce the
  same fingerprint by design. These are deduplicated silently with a log message
  at INFO level (`decision_type: "duplicate_detection"`).

### 2.7 Disc-Dependent Stage Guard

- Before detection, check if any item is in `identification` or `ripping` stage
  with `in_progress = 1`.
- If so, skip detection to avoid concurrent disc access (which causes read errors).

### 2.8 Tray Status Detection

Uses `CDROM_DRIVE_STATUS` ioctl (`0x5326`) on the device file descriptor.

| Return Code | Constant | Meaning |
|-------------|----------|---------|
| 0 | DriveStatusNoInfo | No information available |
| 1 | DriveStatusNoDisc | No disc in drive |
| 2 | DriveStatusTrayOpen | Tray is open |
| 3 | DriveStatusNotReady | Drive not ready |
| 4 | DriveStatusDiscOK | Disc loaded and ready |

`WaitForReady()`: polls up to **60 times** at **1-second intervals** (60 seconds
total), returns immediately when status reaches `DriveStatusDiscOK`.

### 2.9 Disc Ejection

`Ejector` interface with `commandEjector` implementation:
- Shells out to system `eject` command (no device arg uses default drive).
- Called after ripping completes to free the drive for the next disc.

### 2.10 Label Reading (lsblk)

Runs `lsblk -P -o LABEL,FSTYPE {device}` and parses key="value" output format.

`ReadLabel()` accepts a timeout duration, wraps context with deadline, and
extracts the `LABEL` and `FSTYPE` fields from the first non-empty output line.

### 2.11 Label Validation

`IsUnusableLabel()` rejects labels matching these patterns:
- Empty strings
- Generic names: `LOGICAL_VOLUME_ID`, `VOLUME_ID`, `DVD_VIDEO`, `BLURAY`,
  `BD_ROM`, `UNTITLED`, `UNKNOWN DISC`, `VOLUME_`, `DISK_`, `TRACK_`
- All digits: `^\d+$`
- Short codes: 1-4 character uppercase alphanumeric codes
- Disc patterns: contains "DISC"/"DISK" + underscore
- Technical uppercase: all uppercase with underscores, length > 8 chars

`ExtractDiscNameFromVolumeID()` cleans volume IDs:
1. Strip leading digits + underscore prefix
2. Strip season/disc suffix (`_S\d+_DISC_\d+$`)
3. Strip `_TV$` suffix
4. Replace underscores with spaces

Used by `shouldQueryBDInfo()` to decide whether the MakeMKV title name is
sufficient or bd_info should be queried for better metadata.

### 2.12 Completed Disc Re-Insertion

When a previously completed disc is re-inserted (duplicate fingerprint found):

- `shouldRefreshDiscTitle()`: Returns true if the item's disc title is empty or
  equals `"Unknown Disc"` (case-insensitive).
- `tryRefreshDiscTitle()`: Re-scans disc label via lsblk and updates the queue
  item's title if a better label is available. Non-fatal on failure.

This allows previously unidentified discs to pick up correct metadata on
re-insertion without reprocessing.

### 2.13 User-Stopped Item Prevention

When a disc with a known fingerprint is detected and the existing queue item has
`stage = failed` with `IsUserStopReason()` review reason (`"Stop requested by
user"`):

- The item is NOT reset for reprocessing.
- Detection returns the existing item without modification.
- Prevents automatic reprocessing of discs that were intentionally stopped.

---

## 3. Daemon Orchestration

### 3.1 Daemon Runtime Entry Point (`daemonrun`)

`Run(ctx, cfg, opts)` is the main daemon process function:

1. Set up signal handlers:
   - **SIGINT, SIGTERM**: Graceful shutdown (cancel context, drain pipeline).
   - **SIGQUIT**: Dump goroutine stacks to stderr for debugging, then
     continue running (does not shut down).
   - **SIGUSR1**: Toggle log level between INFO and DEBUG. Useful for
     reducing log noise temporarily without a restart.
2. Create timestamped DEBUG-level JSON log file:
   `spindle-{YYYYMMDD}T{HHMMSS.sss}Z.log`.
3. Write PID file (`spindle.pid`).
4. Open queue store.
5. Create notification service.
6. Create workflow manager.
7. Register all 7 stages via `ConfigureStages()`.
8. Create daemon instance.
9. Call `daemon.Start()`.
10. Block on signal context until shutdown signal received.

**Log retention**: On startup, cleans old log files exceeding
`logging.retention_days` across 3 targets: daemon logs, per-item logs, and
tool logs.

**Current log pointer**: Creates `spindle.log` symlink (with hardlink fallback)
pointing to the active log file for easy access.

**Options:**

- `LogLevel`: Override log level.
- `Development`: Enable development mode logging.

### 3.2 Daemon Control (`daemonctl`)

CLI-facing daemon lifecycle management used by `spindle start/stop/restart/status`.

Three functions:

- `Start(executablePath, socketPath, opts, timeout)`: Launch daemon as detached
  background process (`spindle daemon-run`), poll for HTTP API socket
  availability. Returns immediately if already running.
- `Stop(socketPath, gracePeriod, fallbackPID)`: Send HTTP stop request, wait
  for shutdown, force-kill (SIGKILL + PID/lock file cleanup) if still alive.
- `IsRunning(socketPath)`: Check daemon reachability via HTTP API socket.
  Returns PID if reachable.

`spindle restart` composes `Stop` then `Start` at the call site.

Status aggregation (dependency checks, library path validation, system status
formatting) is handled by the `spindle status` command directly, calling
`deps.CheckBinaries()` and config validation functions. It does not belong
in the daemon control package.

**Sentinel error:** `ErrDaemonNotRunning` returned when HTTP API socket unreachable.

### 3.3 One-Shot Stage Execution (`stageexec`)

Package for running a single stage with queue persistence outside the daemon
workflow. Used by CLI commands (`spindle identify`, `spindle gensubtitle`).

`Run(ctx, Options)` executes a stage handler against a queue item:

1. Set `in_progress = 1`, persist to store.
2. Call `Handler.Run(ctx, item)`.
3. Advance to next stage, set `in_progress = 0`, persist.

On failure at any step: mark item as `failed` with error message, notify via
error event, return the stage error.

**Options:** Logger, Store, Notifier, Handler, StageName, Processing status,
Done status, Item.

The `Handler` interface mirrors `stage.Handler` (single `Run` method). The
per-item logger is attached to the context (see DESIGN_OVERVIEW.md
Section 4.5).

### 3.4 Queue Access Fallback (`queueaccess`)

Provides a unified interface for queue operations that works with or without
the daemon running.

**`Access` interface** (11 methods):

```
Stats, List, Describe, ClearAll, ClearCompleted, ClearFailed,
Remove, RetryAll, Retry, RetryEpisode, Stop, ActiveFingerprints
```

**Implementations:**

- `NewHTTPAccess(client)`: Routes operations through daemon HTTP API.
- `NewStoreAccess(store)`: Direct SQLite access (no daemon needed).

**`Session`**: Access handle + cleanup function. `Close()` releases resources
(closes DB connection for store access).

**`OpenWithFallback(httpClient, openStore)`**: Try HTTP API first; if daemon
unavailable, fall back to direct store access. CLI queue commands use this for
offline operation.
