"""Queue management for batch processing."""

import json
import logging
import sqlite3
from datetime import datetime
from enum import Enum
from pathlib import Path

from ..config import SpindleConfig
from ..identify.tmdb import MediaInfo

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
        self.created_at = created_at or datetime.now()
        self.updated_at = updated_at or datetime.now()
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

    def _init_database(self) -> None:
        """Initialize the SQLite database."""
        self.config.log_dir.mkdir(parents=True, exist_ok=True)

        with sqlite3.connect(self.db_path) as conn:
            conn.execute("""
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
            """)

            # Create index on status for faster queries
            conn.execute("""
                CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_items(status)
            """)

            # Migrate existing databases to add progress columns
            try:
                conn.execute("ALTER TABLE queue_items ADD COLUMN progress_stage TEXT")
            except sqlite3.OperationalError:
                pass  # Column already exists

            try:
                conn.execute(
                    "ALTER TABLE queue_items ADD COLUMN progress_percent REAL DEFAULT 0.0"
                )
            except sqlite3.OperationalError:
                pass  # Column already exists

            try:
                conn.execute("ALTER TABLE queue_items ADD COLUMN progress_message TEXT")
            except sqlite3.OperationalError:
                pass  # Column already exists

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
                    item.created_at,
                    item.updated_at,
                    item.progress_stage,
                    item.progress_percent,
                    item.progress_message,
                ),
            )

            item.item_id = cursor.lastrowid

        logger.info(f"Added disc to queue: {item}")
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
                INSERT INTO queue_items (source_path, status, ripped_file, created_at, updated_at,
                                        progress_stage, progress_percent, progress_message)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
                (
                    str(source_path),
                    item.status.value,
                    str(source_path),
                    item.created_at,
                    item.updated_at,
                    item.progress_stage,
                    item.progress_percent,
                    item.progress_message,
                ),
            )

            item.item_id = cursor.lastrowid
            item.ripped_file = source_path

        logger.info(f"Added file to queue: {item}")
        return item

    def update_item(self, item: QueueItem) -> None:
        """Update an existing queue item."""
        item.updated_at = datetime.now()

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
                }
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
                    item.updated_at,
                    item.progress_stage,
                    item.progress_percent,
                    item.progress_message,
                    item.item_id,
                ),
            )

        logger.debug(f"Updated queue item: {item}")

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
            cursor = conn.execute(
                f"""
                SELECT * FROM queue_items 
                WHERE status IN ({placeholders}) 
                ORDER BY created_at
            """,
                [s.value for s in statuses],
            )

            return [self._row_to_item(row) for row in cursor.fetchall()]

    def get_all_items(self) -> list[QueueItem]:
        """Get all queue items."""
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            cursor = conn.execute("""
                SELECT * FROM queue_items ORDER BY created_at DESC
            """)

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
                logger.info(f"Removed item {item_id} from queue")
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
            logger.info(f"Cleared {count} completed items from queue")
            return count

    def get_queue_stats(self) -> dict[str, int]:
        """Get statistics about the queue."""
        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.execute("""
                SELECT status, COUNT(*) as count
                FROM queue_items 
                GROUP BY status
            """)

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
                logger.warning(f"Failed to deserialize media info: {e}")

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
            progress_stage=row["progress_stage"]
            if "progress_stage" in row.keys()
            else None,
            progress_percent=row["progress_percent"]
            if "progress_percent" in row.keys()
            else 0.0,
            progress_message=row["progress_message"]
            if "progress_message" in row.keys()
            else None,
        )
