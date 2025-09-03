"""Essential library organization tests - Plex integration and file management."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaInfo
from spindle.organize.library import LibraryOrganizer


@pytest.fixture
def temp_config():
    """Create temporary config for testing."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
            plex_url="http://localhost:32400",
            plex_token="test_token_12345",
            movies_dir="Movies",
            tv_dir="TV Shows",
        )


@pytest.fixture
def sample_movie_info():
    """Sample movie metadata."""
    return MediaInfo(
        title="Test Movie",
        year=2023,
        media_type="movie",
        tmdb_id=12345,
        overview="A test movie",
        genres=["Action", "Adventure"]
    )


@pytest.fixture
def sample_tv_info():
    """Sample TV show metadata."""
    return MediaInfo(
        title="Test TV Show",
        year=2023,
        media_type="tv",
        tmdb_id=67890,
        overview="A test TV show",
        genres=["Drama"],
        seasons=3
    )


@pytest.fixture
def sample_encoded_file():
    """Sample encoded video file."""
    with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as f:
        f.write(b"encoded video content")
        yield Path(f.name)


class TestLibraryOrganizer:
    """Test Plex-compatible file organization."""
    
    def test_organizer_initialization(self, temp_config):
        """Test organizer initializes with configuration."""
        organizer = LibraryOrganizer(temp_config)
        
        assert organizer.library_dir == temp_config.library_dir
        assert organizer.plex_url == "http://localhost:32400"
        assert organizer.plex_token == "test_token_12345"

    def test_movie_filename_generation(self, temp_config, sample_movie_info):
        """Test movie filename generation following Plex conventions."""
        organizer = LibraryOrganizer(temp_config)
        
        filename = organizer.generate_filename(sample_movie_info)
        
        assert "Test Movie" in filename
        assert "(2023)" in filename
        
        expected = "Test Movie (2023)"
        assert filename == expected

    def test_tv_filename_generation(self, temp_config, sample_tv_info):
        """Test TV show filename generation."""
        organizer = LibraryOrganizer(temp_config)
        
        filename = organizer.generate_filename(sample_tv_info)
        
        assert "Test TV Show" in filename
        assert "(2023)" in filename

    def test_movie_directory_structure(self, temp_config, sample_movie_info):
        """Test movie directory structure creation."""
        organizer = LibraryOrganizer(temp_config)
        
        target_dir = organizer.get_target_directory(sample_movie_info)
        
        assert target_dir.name == "Test Movie (2023)"
        assert target_dir.parent.name == "Movies"
        assert target_dir.parent.parent == temp_config.library_dir

    def test_tv_directory_structure(self, temp_config, sample_tv_info):
        """Test TV show directory structure creation."""
        organizer = LibraryOrganizer(temp_config)
        
        target_dir = organizer.get_target_directory(sample_tv_info)
        
        assert target_dir.name == "Test TV Show (2023)"
        assert target_dir.parent.name == "TV Shows"
        assert target_dir.parent.parent == temp_config.library_dir

    def test_sanitize_filename(self, temp_config):
        """Test filename sanitization for filesystem compatibility."""
        organizer = LibraryOrganizer(temp_config)
        
        unsafe_name = "Movie: The Sequel / Part 2 <Director's Cut>"
        safe_name = organizer.sanitize_filename(unsafe_name)
        
        assert "/" not in safe_name
        assert "<" not in safe_name
        assert ">" not in safe_name
        assert ":" not in safe_name


class TestFileOrganization:
    """Test file movement and organization operations."""
    
    def test_organize_movie_file(self, temp_config, sample_movie_info, sample_encoded_file):
        """Test organizing movie file into library."""
        organizer = LibraryOrganizer(temp_config)
        temp_config.library_dir.mkdir(parents=True, exist_ok=True)
        
        target_path = organizer.organize_media(sample_encoded_file, sample_movie_info)
        
        assert target_path.exists()
        assert target_path.parent.name == "Test Movie (2023)"
        assert target_path.parent.parent.name == "Movies"
        assert target_path.name == "Test Movie (2023).mp4"

    def test_organize_tv_file(self, temp_config, sample_tv_info, sample_encoded_file):
        """Test organizing TV show file into library."""
        organizer = LibraryOrganizer(temp_config)
        temp_config.library_dir.mkdir(parents=True, exist_ok=True)
        
        target_path = organizer.organize_media(sample_encoded_file, sample_tv_info)
        
        assert target_path.exists()
        assert target_path.parent.name == "Test TV Show (2023)"
        assert target_path.parent.parent.name == "TV Shows"

    def test_organize_existing_file_conflict(self, temp_config, sample_movie_info, sample_encoded_file):
        """Test handling of existing file conflicts."""
        organizer = LibraryOrganizer(temp_config)
        temp_config.library_dir.mkdir(parents=True, exist_ok=True)
        
        target_path = organizer.organize_media(sample_encoded_file, sample_movie_info)
        
        with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as f:
            f.write(b"different content")
            second_file = Path(f.name)
        
        result_path = organizer.organize_media(second_file, sample_movie_info)
        
        assert result_path.exists()

    def test_directory_creation(self, temp_config, sample_movie_info):
        """Test automatic directory creation."""
        organizer = LibraryOrganizer(temp_config)
        
        target_dir = organizer.get_target_directory(sample_movie_info)
        
        assert not target_dir.exists()
        
        target_dir.mkdir(parents=True, exist_ok=True)
        
        assert target_dir.exists()
        assert target_dir.is_dir()


class TestPlexIntegration:
    """Test Plex server integration."""
    
    @patch('spindle.organize.library.PlexServer')
    def test_trigger_library_scan(self, mock_plex_server, temp_config):
        """Test triggering Plex library scan."""
        mock_server_instance = Mock()
        mock_section = Mock()
        mock_section.update.return_value = None
        mock_server_instance.library.sections.return_value = [mock_section]
        mock_plex_server.return_value = mock_server_instance
        
        organizer = LibraryOrganizer(temp_config)
        success = organizer.trigger_library_scan()
        
        assert success is True
        mock_section.update.assert_called_once()

    @patch('requests.post')
    def test_trigger_library_scan_failure(self, mock_post, temp_config):
        """Test handling of Plex scan failure."""
        mock_response = Mock()
        mock_response.status_code = 401
        mock_post.return_value = mock_response
        
        organizer = LibraryOrganizer(temp_config)
        success = organizer.trigger_library_scan()
        
        assert success is False

    @patch('spindle.organize.library.PlexServer')
    def test_verify_plex_connection(self, mock_plex_server, temp_config):
        """Test Plex server connection verification."""
        mock_server_instance = Mock()
        mock_server_instance.friendlyName = "Test Plex Server"
        mock_plex_server.return_value = mock_server_instance
        
        organizer = LibraryOrganizer(temp_config)
        connected = organizer.verify_plex_connection()
        
        assert connected is True


class TestWorkflowIntegration:
    """Test organization integration with complete workflow."""
    
    def test_queue_organization_workflow(self, temp_config, sample_movie_info, sample_encoded_file):
        """Test organization integrates with queue workflow."""
        from spindle.queue.manager import QueueManager, QueueItemStatus
        
        queue_manager = QueueManager(temp_config)
        item = queue_manager.add_disc("TEST_MOVIE_DISC")
        item.status = QueueItemStatus.ENCODED
        item.media_info = sample_movie_info
        item.encoded_file = sample_encoded_file
        queue_manager.update_item(item)
        
        organizer = LibraryOrganizer(temp_config)
        temp_config.library_dir.mkdir(parents=True, exist_ok=True)
        
        organized_path = organizer.organize_media(sample_encoded_file, sample_movie_info)
        
        item.status = QueueItemStatus.COMPLETED
        item.final_file = organized_path
        queue_manager.update_item(item)
        
        updated_item = queue_manager.get_item(item.item_id)
        assert updated_item.status == QueueItemStatus.COMPLETED
        assert updated_item.final_file == organized_path
        assert organized_path.exists()

    @patch('spindle.organize.library.PlexServer')
    def test_complete_organization_with_plex(self, mock_plex_server, temp_config, sample_movie_info, sample_encoded_file):
        """Test complete organization including Plex notification."""
        mock_server_instance = Mock()
        mock_section = Mock()
        mock_section.update.return_value = None
        mock_server_instance.library.sections.return_value = [mock_section]
        mock_plex_server.return_value = mock_server_instance
        
        organizer = LibraryOrganizer(temp_config)
        temp_config.library_dir.mkdir(parents=True, exist_ok=True)
        
        organized_path = organizer.organize_media(sample_encoded_file, sample_movie_info)
        plex_success = organizer.trigger_library_scan()
        
        assert organized_path.exists()
        assert plex_success is True

    def test_error_recovery_organization(self, temp_config, sample_movie_info):
        """Test organization error handling and recovery."""
        organizer = LibraryOrganizer(temp_config)
        
        missing_file = Path("/tmp/nonexistent.mp4")
        
        with pytest.raises((FileNotFoundError, OSError)):
            organizer.organize_media(missing_file, sample_movie_info)

    def test_library_structure_validation(self, temp_config):
        """Test library directory structure validation."""
        organizer = LibraryOrganizer(temp_config)
        
        movies_dir = temp_config.library_dir / "Movies"
        tv_dir = temp_config.library_dir / "TV Shows"
        
        organizer.ensure_library_structure()
        
        assert movies_dir.exists()
        assert tv_dir.exists()
        assert movies_dir.is_dir()
        assert tv_dir.is_dir()


class TestMetadataHandling:
    """Test metadata preservation during organization."""
    
    def test_preserve_media_info(self, temp_config, sample_movie_info, sample_encoded_file):
        """Test media information is preserved during organization."""
        organizer = LibraryOrganizer(temp_config)
        temp_config.library_dir.mkdir(parents=True, exist_ok=True)
        
        organized_path = organizer.organize_media(sample_encoded_file, sample_movie_info)
        
        assert "Test Movie" in str(organized_path)
        assert "2023" in str(organized_path)

    def test_filename_encoding_handling(self, temp_config):
        """Test handling of special characters in filenames."""
        special_media_info = MediaInfo(
            title="Café München: The Résumé",
            year=2023,
            media_type="movie",
            tmdb_id=99999
        )
        
        organizer = LibraryOrganizer(temp_config)
        filename = organizer.generate_filename(special_media_info)
        
        assert filename is not None
        assert len(filename) > 0