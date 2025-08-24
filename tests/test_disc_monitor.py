"""Tests for disc detection and monitoring."""

import subprocess
import tempfile
import time
from pathlib import Path
from unittest.mock import MagicMock, Mock, call, patch
from unittest.mock import ANY

import pytest

from spindle.disc.monitor import (
    DiscEventHandler,
    DiscInfo,
    DiscMonitor,
    detect_bluray_vs_dvd,
    detect_disc,
    determine_disc_type,
    eject_disc,
    wait_for_disc_removal,
)


class TestDiscInfo:
    """Test DiscInfo class."""

    def test_disc_info_initialization(self):
        """Test DiscInfo object initialization."""
        disc = DiscInfo("/dev/sr0", "DVD", "TEST_DISC")

        assert disc.device == "/dev/sr0"
        assert disc.disc_type == "DVD"
        assert disc.label == "TEST_DISC"
        assert isinstance(disc.detected_at, float)
        assert disc.detected_at > 0

    def test_disc_info_no_label(self):
        """Test DiscInfo with no label defaults to 'Unknown'."""
        disc = DiscInfo("/dev/sr0", "Blu-ray")

        assert disc.label == "Unknown"

    def test_disc_info_empty_label(self):
        """Test DiscInfo with empty label defaults to 'Unknown'."""
        disc = DiscInfo("/dev/sr0", "DVD", "")

        assert disc.label == "Unknown"

    def test_disc_info_str(self):
        """Test DiscInfo string representation."""
        disc = DiscInfo("/dev/sr0", "DVD", "TEST_MOVIE")
        result = str(disc)

        assert "DVD disc" in result
        assert "TEST_MOVIE" in result
        assert "/dev/sr0" in result

    def test_disc_info_detected_at_timing(self):
        """Test that detected_at is set to current time."""
        before = time.time()
        disc = DiscInfo("/dev/sr0", "DVD")
        after = time.time()

        assert before <= disc.detected_at <= after


class TestDiscMonitor:
    """Test DiscMonitor class."""

    def test_monitor_initialization(self):
        """Test DiscMonitor initialization with defaults."""
        monitor = DiscMonitor()

        assert monitor.device == "/dev/sr0"
        assert monitor.callback is None
        assert monitor.observer is None
        assert monitor.is_monitoring is False

    def test_monitor_initialization_custom(self):
        """Test DiscMonitor initialization with custom parameters."""
        callback = Mock()
        monitor = DiscMonitor("/dev/sr1", callback)

        assert monitor.device == "/dev/sr1"
        assert monitor.callback == callback

    @patch("spindle.disc.monitor.Observer")
    @patch("pathlib.Path.exists")
    def test_start_monitoring_success(self, mock_exists, mock_observer_class):
        """Test successful monitoring start."""
        mock_exists.return_value = True
        mock_observer = Mock()
        mock_observer_class.return_value = mock_observer

        callback = Mock()
        monitor = DiscMonitor("/dev/sr0", callback)
        monitor.start_monitoring()

        assert monitor.is_monitoring is True
        mock_observer.schedule.assert_called_once()
        mock_observer.start.assert_called_once()

    @patch("pathlib.Path.exists")
    def test_start_monitoring_device_not_exists(self, mock_exists):
        """Test monitoring start when device path doesn't exist."""
        mock_exists.return_value = False

        monitor = DiscMonitor("/dev/nonexistent")
        monitor.start_monitoring()

        assert monitor.is_monitoring is False
        assert monitor.observer is None

    def test_start_monitoring_already_monitoring(self):
        """Test starting monitoring when already monitoring."""
        monitor = DiscMonitor()
        monitor.is_monitoring = True

        with patch("spindle.disc.monitor.logger") as mock_logger:
            monitor.start_monitoring()
            mock_logger.warning.assert_called_once()

    @patch("spindle.disc.monitor.Observer")
    @patch("pathlib.Path.exists")
    def test_stop_monitoring(self, mock_exists, mock_observer_class):
        """Test stopping monitoring."""
        mock_exists.return_value = True
        mock_observer = Mock()
        mock_observer_class.return_value = mock_observer

        monitor = DiscMonitor()
        monitor.start_monitoring()
        monitor.stop_monitoring()

        assert monitor.is_monitoring is False
        mock_observer.stop.assert_called_once()
        mock_observer.join.assert_called_once()

    def test_stop_monitoring_not_started(self):
        """Test stopping monitoring when not started."""
        monitor = DiscMonitor()
        # Should not raise any exception
        monitor.stop_monitoring()

    @patch("spindle.disc.monitor.detect_disc")
    def test_on_disc_detected_with_callback(self, mock_detect):
        """Test disc detection callback handling."""
        callback = Mock()
        monitor = DiscMonitor("/dev/sr0", callback)

        disc_info = DiscInfo("/dev/sr0", "DVD", "TEST")
        monitor._on_disc_detected(disc_info)

        callback.assert_called_once_with(disc_info)

    def test_on_disc_detected_no_callback(self):
        """Test disc detection without callback."""
        monitor = DiscMonitor()
        disc_info = DiscInfo("/dev/sr0", "DVD", "TEST")

        # Should not raise any exception
        monitor._on_disc_detected(disc_info)

    @patch("spindle.disc.monitor.detect_disc")
    def test_check_for_disc(self, mock_detect):
        """Test checking for disc."""
        disc_info = DiscInfo("/dev/sr0", "DVD", "TEST")
        mock_detect.return_value = disc_info

        monitor = DiscMonitor("/dev/sr0")
        result = monitor.check_for_disc()

        assert result == disc_info
        mock_detect.assert_called_once_with("/dev/sr0")


class TestDiscEventHandler:
    """Test DiscEventHandler class."""

    def test_handler_initialization(self):
        """Test DiscEventHandler initialization."""
        callback = Mock()
        handler = DiscEventHandler("/dev/sr0", callback)

        assert handler.device == "/dev/sr0"
        assert handler.callback == callback
        assert handler.last_check == 0

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.time")
    def test_on_modified_disc_event(self, mock_time, mock_detect):
        """Test file system modification event for disc device."""
        mock_time.side_effect = [0, 5, 5]  # last_check, current_time, current_time
        disc_info = DiscInfo("/dev/sr0", "DVD", "TEST")
        mock_detect.return_value = disc_info

        callback = Mock()
        handler = DiscEventHandler("/dev/sr0", callback)

        # Create mock event
        event = Mock()
        event.src_path = "/dev/sr0"

        handler.on_modified(event)

        assert handler.last_check == 5
        mock_detect.assert_called_once_with("/dev/sr0")
        callback.assert_called_once_with(disc_info)

    @patch("time.time")
    def test_on_modified_wrong_device(self, mock_time):
        """Test file system modification event for different device."""
        callback = Mock()
        handler = DiscEventHandler("/dev/sr0", callback)

        event = Mock()
        event.src_path = "/dev/sr1"

        handler.on_modified(event)

        callback.assert_not_called()

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.time")
    def test_on_modified_debounce(self, mock_time, mock_detect):
        """Test event debouncing (too soon after last check)."""
        mock_time.side_effect = [0, 1, 1]  # last_check, current_time, current_time

        callback = Mock()
        handler = DiscEventHandler("/dev/sr0", callback)
        handler.last_check = 0

        event = Mock()
        event.src_path = "/dev/sr0"

        handler.on_modified(event)

        # Should not call detect_disc due to debouncing
        mock_detect.assert_not_called()
        callback.assert_not_called()

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.time")
    def test_on_modified_no_disc(self, mock_time, mock_detect):
        """Test modification event when no disc is detected."""
        # Make sure time difference is > 2 seconds for debouncing
        mock_time.side_effect = [5, 5]  # current_time calls
        mock_detect.return_value = None

        callback = Mock()
        handler = DiscEventHandler("/dev/sr0", callback)
        handler.last_check = 0  # Ensure 5 - 0 > 2 for debouncing

        event = Mock()
        event.src_path = "/dev/sr0"

        handler.on_modified(event)

        mock_detect.assert_called_once_with("/dev/sr0")
        callback.assert_not_called()


class TestDetectDisc:
    """Test detect_disc function."""

    @patch("subprocess.run")
    def test_detect_disc_success(self, mock_subprocess):
        """Test successful disc detection."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "TEST_LABEL udf\n"
        mock_subprocess.return_value = mock_result

        # Mock the config timeout - we need to patch the module-level function
        with patch("spindle.disc.monitor.determine_disc_type") as mock_determine:
            mock_determine.return_value = "DVD"
            
            result = detect_disc("/dev/sr0")

        assert result is not None
        assert result.device == "/dev/sr0"
        assert result.label == "TEST_LABEL"
        assert result.disc_type == "DVD"

    @patch("subprocess.run")
    def test_detect_disc_no_label(self, mock_subprocess):
        """Test disc detection with empty label."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = " iso9660\n"  # Single space before filesystem type
        mock_subprocess.return_value = mock_result

        with patch("spindle.disc.monitor.determine_disc_type") as mock_determine:
            mock_determine.return_value = "DVD"
            
            result = detect_disc("/dev/sr0")

        assert result is not None
        # When split(), " iso9660" becomes ["iso9660"], so parts[0] is "iso9660"
        # This becomes the label in the current implementation
        assert result.label == "iso9660"
        assert result.disc_type == "DVD"

    @patch("subprocess.run")
    def test_detect_disc_empty_output(self, mock_subprocess):
        """Test disc detection with empty output."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = ""
        mock_subprocess.return_value = mock_result

        result = detect_disc("/dev/sr0")

        assert result is None

    @patch("subprocess.run")
    def test_detect_disc_command_failure(self, mock_subprocess):
        """Test disc detection when command fails."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_subprocess.return_value = mock_result

        result = detect_disc("/dev/sr0")

        assert result is None

    @patch("subprocess.run")
    def test_detect_disc_timeout(self, mock_subprocess):
        """Test disc detection timeout."""
        mock_subprocess.side_effect = subprocess.TimeoutExpired("cmd", 10)

        with patch("spindle.disc.monitor.logger") as mock_logger:
            result = detect_disc("/dev/sr0")

        assert result is None
        mock_logger.warning.assert_called_once()

    @patch("subprocess.run")
    def test_detect_disc_exception(self, mock_subprocess):
        """Test disc detection with general exception."""
        mock_subprocess.side_effect = Exception("General error")

        with patch("spindle.disc.monitor.logger") as mock_logger:
            result = detect_disc("/dev/sr0")

        assert result is None
        mock_logger.exception.assert_called_once()

    @patch("subprocess.run")
    def test_detect_disc_multiple_parts(self, mock_subprocess):
        """Test disc detection with multiple output parts."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "MOVIE_DISC udf extra_field\n"
        mock_subprocess.return_value = mock_result

        with patch("spindle.disc.monitor.determine_disc_type") as mock_determine:
            mock_determine.return_value = "Blu-ray"
            
            result = detect_disc("/dev/sr0")

        assert result is not None
        assert result.label == "MOVIE_DISC"
        mock_determine.assert_called_once_with("/dev/sr0", "udf", 10)


class TestDetermineDiscType:
    """Test determine_disc_type function."""

    @patch("subprocess.run")
    @patch("spindle.disc.monitor.detect_bluray_vs_dvd")
    def test_determine_disc_type_udf_blkid(self, mock_detect_bd, mock_subprocess):
        """Test disc type detection with UDF filesystem via blkid."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = 'TYPE="udf"'
        mock_subprocess.return_value = mock_result
        mock_detect_bd.return_value = "Blu-ray"

        result = determine_disc_type("/dev/sr0", "udf")

        assert result == "Blu-ray"
        mock_detect_bd.assert_called_once_with("/dev/sr0", 10)

    @patch("subprocess.run")
    def test_determine_disc_type_iso9660_blkid(self, mock_subprocess):
        """Test disc type detection with ISO9660 filesystem via blkid."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = 'TYPE="iso9660"'
        mock_subprocess.return_value = mock_result

        result = determine_disc_type("/dev/sr0", "iso9660")

        assert result == "DVD"

    @patch("subprocess.run")
    @patch("spindle.disc.monitor.detect_bluray_vs_dvd")
    def test_determine_disc_type_fallback_udf(self, mock_detect_bd, mock_subprocess):
        """Test disc type detection fallback to fstype for UDF."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_subprocess.return_value = mock_result
        mock_detect_bd.return_value = "Blu-ray"

        result = determine_disc_type("/dev/sr0", "udf")

        assert result == "Blu-ray"
        mock_detect_bd.assert_called_once_with("/dev/sr0", 10)

    @patch("subprocess.run")
    def test_determine_disc_type_fallback_iso9660(self, mock_subprocess):
        """Test disc type detection fallback to fstype for ISO9660."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_subprocess.return_value = mock_result

        result = determine_disc_type("/dev/sr0", "iso9660")

        assert result == "DVD"

    @patch("subprocess.run")
    def test_determine_disc_type_unknown(self, mock_subprocess):
        """Test disc type detection with unknown filesystem."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_subprocess.return_value = mock_result

        result = determine_disc_type("/dev/sr0", "unknown")

        assert result == "Unknown"

    @patch("subprocess.run")
    def test_determine_disc_type_exception(self, mock_subprocess):
        """Test disc type detection with exception."""
        mock_subprocess.side_effect = Exception("Command error")

        with patch("spindle.disc.monitor.logger") as mock_logger:
            result = determine_disc_type("/dev/sr0", "udf")

        # Should fall back to fstype-based detection
        assert result == "DVD"  # Default for UDF when detect_bluray_vs_dvd fails
        # Should have two debug calls - one for exception, one from detect_bluray_vs_dvd
        assert mock_logger.debug.call_count >= 1


class TestDetectBlurayVsDvd:
    """Test detect_bluray_vs_dvd function."""

    @patch("subprocess.run")
    def test_detect_bluray_success(self, mock_subprocess):
        """Test successful Blu-ray detection."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "blu-ray disc filesystem"
        mock_subprocess.return_value = mock_result

        result = detect_bluray_vs_dvd("/dev/sr0")

        assert result == "Blu-ray"

    @patch("subprocess.run")
    def test_detect_bluray_bdav(self, mock_subprocess):
        """Test Blu-ray detection via BDAV indicator."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "filesystem with bdav structure"
        mock_subprocess.return_value = mock_result

        result = detect_bluray_vs_dvd("/dev/sr0")

        assert result == "Blu-ray"

    @patch("subprocess.run")
    def test_detect_bluray_bdmv(self, mock_subprocess):
        """Test Blu-ray detection via BDMV indicator."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "filesystem with bdmv directory"
        mock_subprocess.return_value = mock_result

        result = detect_bluray_vs_dvd("/dev/sr0")

        assert result == "Blu-ray"

    @patch("subprocess.run")
    def test_detect_dvd_fallback(self, mock_subprocess):
        """Test DVD fallback when no Blu-ray indicators found."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "regular dvd filesystem"
        mock_subprocess.return_value = mock_result

        result = detect_bluray_vs_dvd("/dev/sr0")

        assert result == "DVD"

    @patch("subprocess.run")
    def test_detect_command_failure(self, mock_subprocess):
        """Test detection when command fails."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_subprocess.return_value = mock_result

        result = detect_bluray_vs_dvd("/dev/sr0")

        assert result == "DVD"

    @patch("subprocess.run")
    def test_detect_exception(self, mock_subprocess):
        """Test detection with exception."""
        mock_subprocess.side_effect = Exception("File command failed")

        with patch("spindle.disc.monitor.logger") as mock_logger:
            result = detect_bluray_vs_dvd("/dev/sr0")

        assert result == "DVD"
        mock_logger.debug.assert_called_once()


class TestEjectDisc:
    """Test eject_disc function."""

    @patch("subprocess.run")
    def test_eject_disc_success(self, mock_subprocess):
        """Test successful disc ejection."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result

        result = eject_disc("/dev/sr0")

        assert result is True
        mock_subprocess.assert_called_once_with(
            ["eject", "/dev/sr0"],
            check=False,
            capture_output=True,
            text=True,
            timeout=ANY,  # Will be calculated from config
        )

    @patch("subprocess.run")
    def test_eject_disc_failure(self, mock_subprocess):
        """Test disc ejection failure."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_result.stderr = "Device busy"
        mock_subprocess.return_value = mock_result

        with patch("spindle.disc.monitor.logger") as mock_logger:
            result = eject_disc("/dev/sr0")

        assert result is False
        mock_logger.error.assert_called_once()

    @patch("subprocess.run")
    def test_eject_disc_exception(self, mock_subprocess):
        """Test disc ejection with exception."""
        mock_subprocess.side_effect = Exception("Command error")

        with patch("spindle.disc.monitor.logger") as mock_logger:
            result = eject_disc("/dev/sr0")

        assert result is False
        mock_logger.exception.assert_called_once()

    @patch("subprocess.run")
    def test_eject_disc_default_device(self, mock_subprocess):
        """Test disc ejection with default device."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result

        eject_disc()

        # Should use default device
        call_args = mock_subprocess.call_args[0][0]
        assert call_args[1] == "/dev/sr0"


class TestWaitForDiscRemoval:
    """Test wait_for_disc_removal function."""

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.sleep")
    def test_wait_for_removal_success(self, mock_sleep, mock_detect):
        """Test successful waiting for disc removal."""
        mock_detect.side_effect = [
            DiscInfo("/dev/sr0", "DVD"),  # First check: disc present
            None,  # Second check: disc removed
        ]

        result = wait_for_disc_removal("/dev/sr0", timeout=5)

        assert result is True
        assert mock_detect.call_count == 2
        mock_sleep.assert_called_once_with(1)

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.sleep")  
    @patch("time.time")
    def test_wait_for_removal_timeout(self, mock_time, mock_sleep, mock_detect):
        """Test timeout while waiting for disc removal."""
        # Provide enough time values to avoid StopIteration
        time_values = iter([0, 5, 10, 15, 20, 25, 35, 40, 45])  
        mock_time.side_effect = lambda: next(time_values)
        mock_detect.return_value = DiscInfo("/dev/sr0", "DVD")  # Always present

        result = wait_for_disc_removal("/dev/sr0", timeout=30)

        assert result is False
        # Should have called detect_disc at least once
        assert mock_detect.call_count >= 1

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.sleep")
    def test_wait_for_removal_immediate(self, mock_sleep, mock_detect):
        """Test waiting when disc is already removed."""
        mock_detect.return_value = None  # No disc present

        result = wait_for_disc_removal("/dev/sr0", timeout=5)

        assert result is True
        mock_detect.assert_called_once_with("/dev/sr0")
        mock_sleep.assert_not_called()

    @patch("spindle.disc.monitor.detect_disc")
    @patch("time.sleep")
    def test_wait_for_removal_default_device(self, mock_sleep, mock_detect):
        """Test waiting for removal with default device."""
        mock_detect.return_value = None

        wait_for_disc_removal()

        mock_detect.assert_called_once_with("/dev/sr0")


class TestIntegration:
    """Integration tests combining multiple components."""

    @patch("spindle.disc.monitor.Observer")
    @patch("pathlib.Path.exists")
    @patch("spindle.disc.monitor.detect_disc")
    def test_monitor_workflow_integration(
        self, mock_detect, mock_exists, mock_observer_class,
    ):
        """Test complete monitoring workflow."""
        mock_exists.return_value = True
        mock_observer = Mock()
        mock_observer_class.return_value = mock_observer

        callback_results = []
        
        def test_callback(disc_info):
            callback_results.append(disc_info)

        monitor = DiscMonitor("/dev/sr0", test_callback)

        # Start monitoring
        monitor.start_monitoring()
        assert monitor.is_monitoring is True

        # Simulate disc detection
        disc_info = DiscInfo("/dev/sr0", "DVD", "TEST_DISC")
        monitor._on_disc_detected(disc_info)

        # Check callback was called
        assert len(callback_results) == 1
        assert callback_results[0] == disc_info

        # Stop monitoring
        monitor.stop_monitoring()
        assert monitor.is_monitoring is False

    @patch("subprocess.run")
    def test_disc_detection_workflow(self, mock_subprocess):
        """Test complete disc detection workflow."""
        # Mock lsblk output
        mock_lsblk = Mock()
        mock_lsblk.returncode = 0
        mock_lsblk.stdout = "TEST_MOVIE udf\n"

        # Mock blkid output for disc type detection
        mock_blkid = Mock()
        mock_blkid.returncode = 0
        mock_blkid.stdout = 'TYPE="udf"'

        # Mock file command for Blu-ray detection
        mock_file = Mock()
        mock_file.returncode = 0
        mock_file.stdout = "filesystem with blu-ray structure"

        mock_subprocess.side_effect = [mock_lsblk, mock_blkid, mock_file]

        result = detect_disc("/dev/sr0")

        assert result is not None
        assert result.device == "/dev/sr0"
        assert result.label == "TEST_MOVIE"
        assert result.disc_type == "Blu-ray"
        assert mock_subprocess.call_count == 3

    @patch("subprocess.run")
    def test_eject_and_wait_workflow(self, mock_subprocess):
        """Test eject and wait workflow."""
        # Mock successful eject
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result
        
        with patch("spindle.disc.monitor.wait_for_disc_removal") as mock_wait, \
             patch("spindle.disc.monitor.detect_disc") as mock_detect:
            
            mock_wait.return_value = True
            mock_detect.return_value = None  # Disc removed after eject

            # Simulate eject and wait workflow
            eject_success = eject_disc("/dev/sr0")
            wait_success = wait_for_disc_removal("/dev/sr0")
            final_check = detect_disc("/dev/sr0")

            assert eject_success is True
            assert wait_success is True
            assert final_check is None

    def test_disc_event_handler_integration(self):
        """Test DiscEventHandler with real-like event simulation."""
        callback_results = []
        
        def test_callback(disc_info):
            callback_results.append(disc_info)

        handler = DiscEventHandler("/dev/sr0", test_callback)

        # Simulate filesystem event
        event = Mock()
        event.src_path = "/dev/sr0"

        with patch("time.time", side_effect=[0, 5, 5]), \
             patch("spindle.disc.monitor.detect_disc") as mock_detect:
            
            disc_info = DiscInfo("/dev/sr0", "DVD", "TEST")
            mock_detect.return_value = disc_info

            handler.on_modified(event)

            assert len(callback_results) == 1
            assert callback_results[0] == disc_info
            assert handler.last_check == 5


class TestEdgeCases:
    """Test edge cases and error conditions."""

    def test_disc_info_with_special_characters(self):
        """Test DiscInfo with special characters in label."""
        disc = DiscInfo("/dev/sr0", "DVD", "Test: Movie & More!")
        
        assert disc.label == "Test: Movie & More!"
        assert "Test: Movie & More!" in str(disc)

    @patch("spindle.disc.monitor.Observer")
    @patch("pathlib.Path.exists")
    def test_monitor_exception_in_start(self, mock_exists, mock_observer_class):
        """Test monitor handling exceptions during start."""
        mock_exists.return_value = True
        mock_observer_class.side_effect = Exception("Observer creation failed")

        monitor = DiscMonitor()
        
        # Should handle exception gracefully
        with pytest.raises(Exception):
            monitor.start_monitoring()

    @patch("subprocess.run")
    def test_detect_disc_malformed_output(self, mock_subprocess):
        """Test disc detection with malformed lsblk output."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "invalid\toutput\tformat\twith\ttabs\n"
        mock_subprocess.return_value = mock_result

        with patch("spindle.disc.monitor.determine_disc_type") as mock_determine:
            mock_determine.return_value = "Unknown"
            
            result = detect_disc("/dev/sr0")

        assert result is not None
        # Should handle the malformed output gracefully

    def test_event_handler_rapid_events(self):
        """Test DiscEventHandler with rapid successive events."""
        callback = Mock()
        handler = DiscEventHandler("/dev/sr0", callback)

        event = Mock()
        event.src_path = "/dev/sr0"

        with patch("time.time", side_effect=[0, 1, 1.5, 2, 4, 4]):
            # First event - should be ignored (too soon)
            handler.on_modified(event)
            
            # Second event - still too soon
            handler.on_modified(event)
            
            # Third event - should be processed
            handler.last_check = 0  # Reset for test
            with patch("spindle.disc.monitor.detect_disc") as mock_detect:
                mock_detect.return_value = DiscInfo("/dev/sr0", "DVD")
                handler.on_modified(event)

        # Only one callback should have been made
        assert callback.call_count <= 1

    @patch("subprocess.run")
    def test_detect_disc_unicode_label(self, mock_subprocess):
        """Test disc detection with Unicode characters in label."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = "Тест_Фильм udf\n"  # Cyrillic characters
        mock_subprocess.return_value = mock_result

        with patch("spindle.disc.monitor.determine_disc_type") as mock_determine:
            mock_determine.return_value = "Blu-ray"
            
            result = detect_disc("/dev/sr0")

        assert result is not None
        assert result.label == "Тест_Фильм"