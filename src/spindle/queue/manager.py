"""Queue management for batch processing."""

import json
import logging
import sqlite3
from datetime import UTC, datetime
from enum import Enum
from pathlib import Path
from typing import Any

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaInfo

logger = logging.getLogger(__name__)


class QueueItemStatus(Enum):
    """Status of items in the processing queue."""

    PENDING = "pending"
    RIPPING = "ripping"
    RIPPED = "ripped"
    IDENTIFYING = "identifying"
    IDENTIFIED = "identified"
    ENCODING = "encoding"
    ENCODED = "encoded"
    ORGANIZING = "organizing"
    COMPLETED = "completed"
    FAILED = "failed"
    REVIEW = "review"  # For unidentified media


class QueueItem:
    """Represents an item in the processing queue."""

    def __init__(
        self,
        item_id: int | None = None,
        source_path: Path | None = None,
        disc_title: str | None = None,
        status: QueueItemStatus = QueueItemStatus.PENDING,
        media_info: MediaInfo | None = None,
        ripped_file: Path | None = None,
        encoded_file: Path | None = None,
        final_file: Path | None = None,
        error_message: str | None = None,
        created_at: datetime | None = None,
        updated_at: datetime | None = None,
        progress_stage: str | None = None,
        progress_percent: float = 0.0,
        progress_message: str | None = None,
    ):
        self.item_id = item_id
        self.source_path = source_path
        self.disc_title = disc_title
        self.status = status
        self.media_info = media_info
        self.ripped_file = ripped_file
        self.encoded_file = encoded_file
        self.final_file = final_file
        self.error_message = error_message
        self.created_at = created_at or datetime.now(UTC)
        self.updated_at = updated_at or datetime.now(UTC)
        self.progress_stage = progress_stage
        self.progress_percent = progress_percent
        self.progress_message = progress_message

    def __str__(self) -> str:
        if self.media_info:
            return f"{self.media_info.title} ({self.status.value})"
        if self.disc_title:
            return f"{self.disc_title} ({self.status.value})"
        if self.source_path:
            return f"{self.source_path.name} ({self.status.value})"
        return f"Queue item {self.item_id} ({self.status.value})"


class QueueManager:
    """Manages the processing queue using SQLite."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.db_path = config.log_dir / "queue.db"
        self._init_database()

    def _datetime_to_str(self, dt: datetime | None) -> str | None:
        """Convert datetime to string for SQLite storage."""
        if dt is None:
            return None
        return dt.isoformat()

    def _init_database(self) -> None:
        """Initialize the SQLite database."""
        self.config.log_dir.mkdir(parents=True, exist_ok=True)

        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS queue_items (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    source_path TEXT,
                    disc_title TEXT,
                    status TEXT NOT NULL,
                    media_info_json TEXT,
                    ripped_file TEXT,
                    encoded_file TEXT,
                    final_file TEXT,
                    error_message TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    progress_stage TEXT,
                    progress_percent REAL DEFAULT 0.0,
                    progress_message TEXT
                )
            """,
            )

            # Create index on status for faster queries
            conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_items(status)
            """,
            )

            # Apply database migrations
            self._apply_migrations(conn)

    def _apply_migrations(self, conn: sqlite3.Connection) -> None:
        """Apply database schema migrations with proper verification."""

        # Get current schema version
        current_version = self._get_schema_version(conn)
        logger.info("Current database schema version: %s", current_version)

        # Apply migrations in order
        migrations = [
            (1, self._migration_001_add_progress_columns),
            # Future migrations go here
        ]

        for version, migration_func in migrations:
            if current_version < version:
                logger.info("Applying database migration %s", version)
                try:
                    migration_func(conn)
                    self._set_schema_version(conn, version)
                    logger.info("Successfully applied migration %s", version)
                except Exception as e:
                    logger.exception(f"Migration {version} failed: {e}")
                    msg = f"Database migration {version} failed: {e}"
                    raise RuntimeError(msg)

    def _get_schema_version(self, conn: sqlite3.Connection) -> int:
        """Get the current schema version from the database."""
        try:
            # Create schema_version table if it doesn't exist
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS schema_version (
                    version INTEGER PRIMARY KEY
                )
            """,
            )

            cursor = conn.execute(
                "SELECT version FROM schema_version ORDER BY version DESC LIMIT 1",
            )
            result = cursor.fetchone()
            return result[0] if result else 0

        except sqlite3.Error as e:
            logger.exception(f"Failed to get schema version: {e}")
            return 0

    def _set_schema_version(self, conn: sqlite3.Connection, version: int) -> None:
        """Set the schema version in the database."""
        conn.execute("DELETE FROM schema_version")  # Keep only the latest version
        conn.execute("INSERT INTO schema_version (version) VALUES (?)", (version,))

    def _migration_001_add_progress_columns(self, conn: sqlite3.Connection) -> None:
        """Migration 1: Add progress tracking columns."""

        # Check if columns already exist by trying to select from them
        columns_to_add = [
            ("progress_stage", "TEXT"),
            ("progress_percent", "REAL DEFAULT 0.0"),
            ("progress_message", "TEXT"),
        ]

        for column_name, column_def in columns_to_add:
            try:
                # Test if column exists by selecting from it
                conn.execute(f"SELECT {column_name} FROM queue_items LIMIT 1")
                logger.debug("Column %s already exists", column_name)
            except sqlite3.OperationalError as e:
                if "no such column" in str(e).lower():
                    # Column doesn't exist, add it
                    logger.info("Adding column %s", column_name)
                    conn.execute(
                        f"ALTER TABLE queue_items ADD COLUMN {column_name} {column_def}",
                    )

                    # Verify the column was added
                    try:
                        conn.execute(f"SELECT {column_name} FROM queue_items LIMIT 1")
                        logger.debug("Successfully verified column %s", column_name)
                    except sqlite3.OperationalError:
                        msg = f"Failed to add column {column_name}"
                        raise RuntimeError(msg) from None
                else:
                    # Unexpected error
                    msg = f"Unexpected error checking column {column_name}: {e}"
                    raise RuntimeError(msg) from e

    def add_disc(self, disc_title: str) -> QueueItem:
        """Add a disc to the queue."""
        item = QueueItem(
            disc_title=disc_title,
            status=QueueItemStatus.PENDING,
        )

        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                """
                INSERT INTO queue_items (disc_title, status, created_at, updated_at,
                    progress_stage, progress_percent, progress_message)
                VALUES (?, ?, ?, ?, ?, ?, ?)
            """,
                (
                    disc_title,
                    item.status.value,
                    self._datetime_to_str(item.created_at),
                    self._datetime_to_str(item.updated_at),
                    item.progress_stage,
                    item.progress_percent,
                    item.progress_message,
                ),
            )

            item.item_id = cursor.lastrowid

        logger.info("Added disc to queue: %s", item)
        return item

    def add_file(self, source_path: Path) -> QueueItem:
        """Add a file to the queue."""
        item = QueueItem(
            source_path=source_path,
            status=QueueItemStatus.RIPPED,  # Files start as already ripped
        )

        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                """
                INSERT INTO queue_items (
                    source_path, status, ripped_file, created_at, updated_at,
                    progress_stage, progress_percent, progress_message
                )
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
                (
                    str(source_path),
                    item.status.value,
                    str(source_path),
                    self._datetime_to_str(item.created_at),
                    self._datetime_to_str(item.updated_at),
                    item.progress_stage,
                    item.progress_percent,
                    item.progress_message,
                ),
            )

            item.item_id = cursor.lastrowid
            item.ripped_file = source_path

        logger.info("Added file to queue: %s", item)
        return item

    def update_item(self, item: QueueItem) -> None:
        """Update an existing queue item."""
        item.updated_at = datetime.now(UTC)

        media_info_json = None
        if item.media_info:
            # Serialize media info to JSON
            media_info_json = json.dumps(
                {
                    "title": item.media_info.title,
                    "year": item.media_info.year,
                    "media_type": item.media_info.media_type,
                    "tmdb_id": item.media_info.tmdb_id,
                    "overview": item.media_info.overview,
                    "genres": item.media_info.genres,
                    "season": item.media_info.season,
                    "episode": item.media_info.episode,
                    "episode_title": item.media_info.episode_title,
                },
            )

        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                """
                UPDATE queue_items
                SET source_path = ?, disc_title = ?, status = ?, media_info_json = ?,
                    ripped_file = ?, encoded_file = ?, final_file = ?,
                    error_message = ?, updated_at = ?, progress_stage = ?,
                    progress_percent = ?, progress_message = ?
                WHERE id = ?
            """,
                (
                    str(item.source_path) if item.source_path else None,
                    item.disc_title,
                    item.status.value,
                    media_info_json,
                    str(item.ripped_file) if item.ripped_file else None,
                    str(item.encoded_file) if item.encoded_file else None,
                    str(item.final_file) if item.final_file else None,
                    item.error_message,
                    self._datetime_to_str(item.updated_at),
                    item.progress_stage,
                    item.progress_percent,
                    item.progress_message,
                    item.item_id,
                ),
            )

        logger.debug("Updated queue item: %s", item)

    def get_item(self, item_id: int) -> QueueItem | None:
        """Get a specific queue item by ID."""
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            cursor = conn.execute(
                """
                SELECT * FROM queue_items WHERE id = ?
            """,
                (item_id,),
            )

            row = cursor.fetchone()
            if not row:
                return None

            return self._row_to_item(row)

    def get_items_by_status(self, status: QueueItemStatus) -> list[QueueItem]:
        """Get all items with a specific status."""
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            cursor = conn.execute(
                """
                SELECT * FROM queue_items WHERE status = ? ORDER BY created_at
            """,
                (status.value,),
            )

            return [self._row_to_item(row) for row in cursor.fetchall()]

    def get_pending_items(self) -> list[QueueItem]:
        """Get all items ready for processing."""
        statuses = [
            QueueItemStatus.PENDING,
            QueueItemStatus.RIPPED,
            QueueItemStatus.IDENTIFIED,
            QueueItemStatus.ENCODED,
        ]

        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            placeholders = ",".join("?" * len(statuses))
            query = f"""
                SELECT * FROM queue_items
                WHERE status IN ({placeholders})
                ORDER BY created_at
            """
            cursor = conn.execute(query, [s.value for s in statuses])

            return [self._row_to_item(row) for row in cursor.fetchall()]

    def get_all_items(self) -> list[QueueItem]:
        """Get all queue items."""
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            cursor = conn.execute(
                """
                SELECT * FROM queue_items ORDER BY created_at DESC
            """,
            )

            return [self._row_to_item(row) for row in cursor.fetchall()]

    def remove_item(self, item_id: int) -> bool:
        """Remove an item from the queue."""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                """
                DELETE FROM queue_items WHERE id = ?
            """,
                (item_id,),
            )

            if cursor.rowcount > 0:
                logger.info("Removed item %s from queue", item_id)
                return True

            return False

    def clear_completed(self) -> int:
        """Remove all completed items from the queue."""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                """
                DELETE FROM queue_items WHERE status IN (?, ?)
            """,
                (QueueItemStatus.COMPLETED.value, QueueItemStatus.FAILED.value),
            )

            count = cursor.rowcount
            logger.info("Cleared %s completed items from queue", count)
            return count

    def clear_all(self, *, force: bool = False) -> int:
        """Remove all items from the queue.

        Args:
            force: If True, clear all items including those in processing status.

        Raises:
            RuntimeError: If items are currently being processed and force is False.
        """
        with sqlite3.connect(self.db_path) as conn:
            if not force:
                # Check for items currently being processed
                cursor = conn.execute(
                    "SELECT COUNT(*) FROM queue_items WHERE status IN (?, ?, ?)",
                    (
                        QueueItemStatus.RIPPING.value,
                        QueueItemStatus.IDENTIFYING.value,
                        QueueItemStatus.ENCODING.value,
                    ),
                )
                processing_count = cursor.fetchone()[0]

                if processing_count > 0:
                    msg = (
                        f"Cannot clear queue: {processing_count} items are "
                        "currently being processed"
                    )
                    logger.warning(msg)
                    raise RuntimeError(msg)

            cursor = conn.execute("DELETE FROM queue_items")
            count = cursor.rowcount
            if force:
                logger.info(
                    "Force cleared %s items from queue (including processing)", count,
                )
            else:
                logger.info("Cleared %s items from queue (full clear)", count)
            return count

    def clear_failed(self) -> int:
        """Remove only failed items from the queue."""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                "DELETE FROM queue_items WHERE status = ?",
                (QueueItemStatus.FAILED.value,),
            )
            count = cursor.rowcount
            logger.info("Cleared %s failed items from queue", count)
            return count

    def reset_stuck_processing_items(self) -> int:
        """Reset items stuck in processing status back to pending.

        This is useful when Spindle was stopped unexpectedly and items
        remain in processing states (ripping, identifying, encoding).

        Returns:
            Number of items reset.
        """
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                """UPDATE queue_items
                   SET status = ?,
                       progress_stage = 'Reset from stuck processing',
                       progress_percent = 0,
                       progress_message = NULL
                   WHERE status IN (?, ?, ?)""",
                (
                    QueueItemStatus.PENDING.value,
                    QueueItemStatus.RIPPING.value,
                    QueueItemStatus.IDENTIFYING.value,
                    QueueItemStatus.ENCODING.value,
                ),
            )
            count = cursor.rowcount
            if count > 0:
                logger.info("Reset %s stuck processing items to pending status", count)
            return count

    def check_database_health(self) -> dict[str, Any]:
        """Check database health and return diagnostic information."""
        health_info: dict[str, Any] = {
            "database_exists": self.db_path.exists(),
            "database_readable": False,
            "schema_version": None,
            "table_exists": False,
            "columns_present": [],
            "missing_columns": [],
            "integrity_check": False,
            "total_items": 0,
        }

        if not health_info["database_exists"]:
            return health_info

        try:
            with sqlite3.connect(self.db_path) as conn:
                health_info["database_readable"] = True

                # Check schema version
                try:
                    health_info["schema_version"] = self._get_schema_version(conn)
                except (sqlite3.Error, ValueError):
                    health_info["schema_version"] = "unknown"

                # Check if main table exists
                cursor = conn.execute(
                    """
                    SELECT name FROM sqlite_master
                    WHERE type='table' AND name='queue_items'
                """,
                )
                health_info["table_exists"] = cursor.fetchone() is not None

                if health_info["table_exists"]:
                    # Check column structure
                    cursor = conn.execute("PRAGMA table_info(queue_items)")
                    existing_columns = {
                        row[1] for row in cursor.fetchall()
                    }  # row[1] is column name

                    expected_columns = {
                        "id",
                        "source_path",
                        "disc_title",
                        "status",
                        "media_info_json",
                        "ripped_file",
                        "encoded_file",
                        "final_file",
                        "error_message",
                        "created_at",
                        "updated_at",
                        "progress_stage",
                        "progress_percent",
                        "progress_message",
                    }

                    health_info["columns_present"] = list(existing_columns)
                    health_info["missing_columns"] = list(
                        expected_columns - existing_columns,
                    )

                    # Get item count
                    cursor = conn.execute("SELECT COUNT(*) FROM queue_items")
                    health_info["total_items"] = cursor.fetchone()[0]

                # Run integrity check
                cursor = conn.execute("PRAGMA integrity_check")
                result = cursor.fetchone()
                health_info["integrity_check"] = result[0] == "ok" if result else False

        except Exception as e:
            health_info["error"] = str(e)
            logger.exception("Database health check failed")

        return health_info

    def get_queue_stats(self) -> dict[str, int]:
        """Get statistics about the queue."""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute(
                """
                SELECT status, COUNT(*) as count
                FROM queue_items
                GROUP BY status
            """,
            )

            stats = {}
            for row in cursor.fetchall():
                status, count = row
                stats[status] = count

            return stats

    def _row_to_item(self, row: sqlite3.Row) -> QueueItem:
        """Convert a database row to a QueueItem."""
        media_info = None
        if row["media_info_json"]:
            try:
                data = json.loads(row["media_info_json"])
                media_info = MediaInfo(
                    title=data["title"],
                    year=data["year"],
                    media_type=data["media_type"],
                    tmdb_id=data["tmdb_id"],
                    overview=data.get("overview", ""),
                    genres=data.get("genres", []),
                    season=data.get("season"),
                    episode=data.get("episode"),
                    episode_title=data.get("episode_title"),
                )
            except (json.JSONDecodeError, KeyError) as e:
                logger.warning("Failed to deserialize media info: %s", e)

        # Handle progress fields with fallbacks for older database schemas
        try:
            progress_stage = row["progress_stage"]
        except (KeyError, IndexError):
            progress_stage = None

        try:
            progress_percent = row["progress_percent"]
        except (KeyError, IndexError):
            progress_percent = 0.0

        try:
            progress_message = row["progress_message"]
        except (KeyError, IndexError):
            progress_message = None

        return QueueItem(
            item_id=row["id"],
            source_path=Path(row["source_path"]) if row["source_path"] else None,
            disc_title=row["disc_title"],
            status=QueueItemStatus(row["status"]),
            media_info=media_info,
            ripped_file=Path(row["ripped_file"]) if row["ripped_file"] else None,
            encoded_file=Path(row["encoded_file"]) if row["encoded_file"] else None,
            final_file=Path(row["final_file"]) if row["final_file"] else None,
            error_message=row["error_message"],
            created_at=datetime.fromisoformat(row["created_at"]),
            updated_at=datetime.fromisoformat(row["updated_at"]),
            progress_stage=progress_stage,
            progress_percent=progress_percent,
            progress_message=progress_message,
        )
