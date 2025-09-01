"""Tests for enhanced disc identification system."""

import tempfile
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.metadata_extractor import EnhancedDiscMetadata, EnhancedDiscMetadataExtractor
from spindle.disc.title_selector import ContentType, IntelligentTitleSelector, SelectionCriteria
from spindle.disc.ripper import Title, Track
from spindle.identify.tmdb import MediaIdentifier, MediaInfo
from spindle.identify.tmdb_cache import TMDBCache


@pytest.fixture
def test_config():
    """Create a test configuration."""
    return SpindleConfig(
        staging_dir=Path("/tmp/spindle/staging"),
        library_dir=Path("/tmp/spindle/library"),
        log_dir=Path("/tmp/spindle/logs"),
        review_dir=Path("/tmp/spindle/review"),
        tmdb_api_key="test_key",
    )


@pytest.fixture
def sample_movie_titles():
    """Create sample movie titles."""
    return [
        Title("0", 7200, 4000000000, 24, [
            Track("0", "video", "H.264", "eng", 7200, 3000000000),
            Track("1", "audio", "DTS-HD", "eng", 7200, 800000000),
            Track("2", "audio", "AC3", "eng", 7200, 200000000),
        ]),
        Title("1", 900, 500000000, 3, [
            Track("0", "video", "H.264", "eng", 900, 400000000),
            Track("1", "audio", "AC3", "eng", 900, 100000000),
        ]),
        Title("2", 1200, 700000000, 4, [
            Track("0", "video", "H.264", "eng", 1200, 600000000),
            Track("1", "audio", "AC3", "eng", 1200, 100000000),
        ]),
    ]


@pytest.fixture
def sample_tv_titles():
    """Create sample TV episode titles."""
    titles = []
    for i in range(4):
        titles.append(Title(
            str(i),
            2640,  # 44 minutes
            1500000000,  # 1.5GB
            22,
            [
                Track("0", "video", "H.264", "eng", 2640, 1200000000),
                Track("1", "audio", "AC3", "eng", 2640, 300000000),
            ]
        ))
    return titles


class TestEnhancedDiscMetadataExtractor:
    """Test the enhanced metadata extractor."""
    
    def test_bd_info_parsing(self):
        """Test bd_info output parsing."""
        extractor = EnhancedDiscMetadataExtractor()
        
        sample_output = """
        Volume Identifier   : BLAZING_SADDLES
        BluRay detected     : yes
        Disc name           : Blazing Saddles
        provider data       : '                                '
        """
        
        result = extractor.parse_bd_info_output(sample_output)
        
        assert result["volume_identifier"] == "BLAZING_SADDLES"
        assert result["disc_name"] == "Blazing Saddles"
        
    def test_tv_series_detection(self):
        """Test TV series detection from volume ID."""
        metadata = EnhancedDiscMetadata()
        metadata.volume_id = "BATMAN_TV_S1_DISC_1"
        metadata.disc_type_info = {"is_tv": True, "season": 1, "disc": 1}
        
        assert metadata.is_tv_series() is True
        
        season, disc = metadata.get_season_disc_info()
        assert season == 1
        assert disc == 1
        
    def test_title_candidate_extraction(self):
        """Test extraction of title candidates in priority order."""
        metadata = EnhancedDiscMetadata()
        metadata.disc_name = "50 First Dates"
        metadata.bdmt_title = "50 First Dates (Blu-ray)"
        metadata.volume_id = "00000095_50_FIRST_DATES"
        metadata.makemkv_label = "LOGICAL_VOLUME_ID"
        
        candidates = metadata.get_best_title_candidates()
        
        # Should prioritize disc_name over volume_id cleanup
        assert candidates[0] == "50 First Dates"
        assert "50 FIRST DATES" in candidates  # Cleaned volume ID
        # Should exclude generic makemkv label
        assert "LOGICAL_VOLUME_ID" not in candidates
        
    def test_makemkv_cinfo_parsing(self):
        """Test parsing of MakeMKV CINFO output for content identification."""
        extractor = EnhancedDiscMetadataExtractor()
        
        # Sample MakeMKV output with CINFO lines from real example
        makemkv_output = """
        CINFO:2,0,"Blazing Saddles"
        CINFO:30,0,"MOVIE_DISC"
        CINFO:32,0,"BLAZING_SADDLES_BD"
        TINFO:0,2,0,"Main Feature"
        TINFO:0,9,0,"01:33:17"
        MSG:1005,0,1,"MakeMKV v1.17.5 win(x64-release) started","%1 started","MakeMKV v1.17.5 win(x64-release)"
        """
        
        result = extractor.parse_makemkv_info_output(makemkv_output)
        
        assert result["disc_title"] == "Blazing Saddles"
        assert result["volume_name"] == "MOVIE_DISC"
        assert result["volume_id"] == "BLAZING_SADDLES_BD"
        
    def test_makemkv_enhanced_metadata_population(self):
        """Test that MakeMKV data enhances metadata when other sources are missing."""
        extractor = EnhancedDiscMetadataExtractor()
        metadata = EnhancedDiscMetadata()  # Empty metadata
        
        makemkv_output = """
        CINFO:2,0,"The Matrix"
        CINFO:32,0,"MATRIX_DISC_1"
        """
        
        # Populate with MakeMKV data
        result = extractor.populate_makemkv_data_from_output(metadata, makemkv_output, [])
        
        assert result.disc_name == "The Matrix"
        assert result.volume_id == "MATRIX_DISC_1"
        assert result.makemkv_label == "MATRIX_DISC_1"


class TestTMDBCache:
    """Test TMDB caching functionality."""
    
    def test_cache_initialization(self):
        """Test cache database initialization."""
        with tempfile.TemporaryDirectory() as temp_dir:
            cache_dir = Path(temp_dir) / "cache"
            cache = TMDBCache(cache_dir, ttl_days=30)
            
            assert cache.db_path.exists()
            assert cache.ttl_days == 30
    
    def test_cache_operations(self):
        """Test cache store and retrieve operations."""
        with tempfile.TemporaryDirectory() as temp_dir:
            cache_dir = Path(temp_dir) / "cache"
            cache = TMDBCache(cache_dir, ttl_days=30)
            
            # Store results
            test_results = [{"id": 123, "title": "Test Movie"}]
            success = cache.cache_results("test movie", test_results, "movie")
            assert success is True
            
            # Retrieve results
            cached = cache.search_cache("test movie", "movie")
            assert cached is not None
            assert cached.results == test_results
            assert cached.is_valid() is True


class TestIntelligentTitleSelector:
    """Test intelligent title and track selection."""
    
    def test_movie_content_selection(self, sample_movie_titles):
        """Test movie title selection."""
        criteria = SelectionCriteria(max_extras=2, include_commentary=True)
        selector = IntelligentTitleSelector(criteria)
        
        media_info = MediaInfo(
            title="Test Movie",
            year=2023,
            media_type="movie",
            tmdb_id=123
        )
        
        selection = selector.select_content(sample_movie_titles, media_info, ContentType.MOVIE)
        
        assert selection.content_type == ContentType.MOVIE
        assert len(selection.main_titles) == 1  # Longest title as main feature
        assert selection.main_titles[0].duration == 7200  # 2 hours
        assert len(selection.extra_titles) <= 2  # Respects max_extras
        
    def test_tv_content_selection(self, sample_tv_titles):
        """Test TV series title selection."""
        criteria = SelectionCriteria()
        selector = IntelligentTitleSelector(criteria)
        
        media_info = MediaInfo(
            title="Test Series",
            year=2023,
            media_type="tv",
            tmdb_id=456
        )
        
        selection = selector.select_content(sample_tv_titles, media_info, ContentType.TV_SERIES)
        
        assert selection.content_type == ContentType.TV_SERIES
        assert len(selection.main_titles) == 4  # All similar-duration episodes
        
    def test_track_selection(self, sample_movie_titles):
        """Test audio track selection."""
        criteria = SelectionCriteria(
            preferred_audio_codecs=["DTS-HD", "TrueHD", "AC3"],
            include_commentary=True,
            max_commentary_tracks=1
        )
        selector = IntelligentTitleSelector(criteria)
        
        title = sample_movie_titles[0]  # Main feature with multiple audio tracks
        selected_tracks = selector._select_tracks_for_title(title, is_main_content=True)
        
        # Should include video track
        video_tracks = [t for t in selected_tracks if t.track_type == "video"]
        assert len(video_tracks) == 1
        
        # Should select highest quality audio (DTS-HD over AC3)
        audio_tracks = [t for t in selected_tracks if t.track_type == "audio"]
        assert len(audio_tracks) >= 1
        # First audio track should be DTS-HD (highest quality)
        assert any(t.codec == "DTS-HD" for t in audio_tracks)
    
    def test_version_detection(self):
        """Test movie version detection (director's cut, extended, theatrical)."""
        # Create sample titles with different versions
        from spindle.disc.ripper import Title, Track
        
        theatrical = Title("0", 7200, 4000000000, 24, [
            Track("0", "video", "H.264", "eng", 7200, 3000000000),
        ], name="Theatrical Cut")
        
        directors_cut = Title("1", 8100, 4500000000, 26, [
            Track("0", "video", "H.264", "eng", 8100, 3300000000),
        ], name="Director's Cut")
        
        final_cut = Title("2", 8400, 4700000000, 28, [
            Track("0", "video", "H.264", "eng", 8400, 3500000000),
        ], name="Final Cut")
        
        titles = [theatrical, directors_cut, final_cut]
        
        # Test with prefer_extended_versions=True (should select only Final Cut)
        criteria_extended = SelectionCriteria(prefer_extended_versions=True)
        selector_extended = IntelligentTitleSelector(criteria_extended)
        
        media_info = MediaInfo(
            title="Test Movie",
            year=2023,
            media_type="movie",
            tmdb_id=123
        )
        
        selection_extended = selector_extended.select_content(titles, media_info, ContentType.MOVIE)
        
        # Should select only one version (the best extended version)
        assert len(selection_extended.main_titles) == 1
        assert "Final Cut" in selection_extended.main_titles[0].name  # Ultimate version preferred
        
        # Test with prefer_extended_versions=False (should select only Theatrical)
        criteria_theatrical = SelectionCriteria(prefer_extended_versions=False)
        selector_theatrical = IntelligentTitleSelector(criteria_theatrical)
        
        selection_theatrical = selector_theatrical.select_content(titles, media_info, ContentType.MOVIE)
        
        # Should select only theatrical version
        assert len(selection_theatrical.main_titles) == 1
        assert "Theatrical" in selection_theatrical.main_titles[0].name
        
    def test_version_type_detection(self):
        """Test detection of specific version types from title names."""
        criteria = SelectionCriteria()
        selector = IntelligentTitleSelector(criteria)
        
        from spindle.disc.ripper import Title, Track
        
        test_cases = [
            ("Main Feature", "main"),
            ("Director's Cut", "directors_cut"),
            ("Extended Edition", "extended"),
            ("Theatrical Version", "theatrical"),
            ("Ultimate Cut", "ultimate"),
            ("Special Edition", "special"),
            ("Unrated Version", "extended"),
            ("Final Cut", "ultimate"),
        ]
        
        for title_name, expected_type in test_cases:
            title = Title("0", 7200, 4000000000, 24, [], name=title_name)
            detected_type = selector._get_version_type(title)
            assert detected_type == expected_type, f"Expected {expected_type} for '{title_name}', got {detected_type}"
    
    def test_real_world_examples(self):
        """Test real-world disc examples (Blade Runner, Lord of the Rings)."""
        from spindle.disc.ripper import Title, Track
        
        # Blade Runner example - multiple cuts
        blade_runner_titles = [
            Title("0", 7020, 4000000000, 24, [], name="Theatrical Cut"),  # 117 min
            Title("1", 6960, 3900000000, 24, [], name="Director's Cut"),  # 116 min  
            Title("2", 7020, 4100000000, 24, [], name="Final Cut"),       # 117 min
        ]
        
        criteria_extended = SelectionCriteria(prefer_extended_versions=True)
        selector = IntelligentTitleSelector(criteria_extended)
        
        media_info = MediaInfo("Blade Runner", 1982, "movie", 78)
        selection = selector.select_content(blade_runner_titles, media_info, ContentType.MOVIE)
        
        # Should select only Final Cut (ultimate version)
        assert len(selection.main_titles) == 1
        assert "Final Cut" in selection.main_titles[0].name
        
        # Test with theatrical preference
        criteria_theatrical = SelectionCriteria(prefer_extended_versions=False)
        selector_theatrical = IntelligentTitleSelector(criteria_theatrical)
        selection_theatrical = selector_theatrical.select_content(blade_runner_titles, media_info, ContentType.MOVIE)
        
        # Should select only Theatrical Cut
        assert len(selection_theatrical.main_titles) == 1
        assert "Theatrical" in selection_theatrical.main_titles[0].name
        
        # Lord of the Rings example
        lotr_titles = [
            Title("0", 10680, 6000000000, 48, [], name="Main Feature"),      # 178 min
            Title("1", 12480, 7000000000, 56, [], name="Extended Edition"),  # 208 min
        ]
        
        selection_extended = selector.select_content(lotr_titles, media_info, ContentType.MOVIE)
        
        # Should select only Extended Edition
        assert len(selection_extended.main_titles) == 1
        assert "Extended" in selection_extended.main_titles[0].name
        
        # With theatrical preference, should select Main Feature
        selection_theatrical = selector_theatrical.select_content(lotr_titles, media_info, ContentType.MOVIE)
        assert len(selection_theatrical.main_titles) == 1
        assert "Main Feature" in selection_theatrical.main_titles[0].name


class TestMediaIdentifierIntegration:
    """Test the enhanced MediaIdentifier."""
    
    @pytest.mark.asyncio
    async def test_disc_content_identification_with_cache(self, test_config):
        """Test disc content identification with caching."""
        identifier = MediaIdentifier(test_config)
        
        title_candidates = ["Blazing Saddles", "BLAZING_SADDLES"]
        
        # Mock TMDB API responses
        with patch.object(identifier.tmdb, 'search_movie') as mock_search:
            mock_search.return_value = [{"id": 123, "title": "Blazing Saddles"}]
            
            with patch.object(identifier.tmdb, 'get_movie_details') as mock_details:
                # The mock will be called through our caching layer
                mock_details.return_value = {
                    "id": 123,
                    "title": "Blazing Saddles",
                    "release_date": "1974-06-07",
                    "genres": [{"name": "Comedy"}, {"name": "Western"}],
                    "overview": "A satirical Western comedy film."
                }
                
                # Since we're mocking and not using runtime verification,
                # the method will try regular search then get details
                result = await identifier.identify_disc_content(title_candidates, runtime_minutes=93)
                
                assert result is not None
                assert result.title == "Blazing Saddles"
                assert result.media_type == "movie"
                # The exact year and genres depend on our cache/fallback logic
                assert result.tmdb_id == 123
    
    def test_generic_label_detection(self, test_config):
        """Test detection of generic disc labels."""
        identifier = MediaIdentifier(test_config)
        
        generic_labels = [
            "LOGICAL_VOLUME_ID",
            "DVD_VIDEO", 
            "BLURAY",
            "123456",
            "ABC",
        ]
        
        for label in generic_labels:
            assert identifier.is_generic_label(label) is True
            
        valid_labels = [
            "BLAZING_SADDLES",
            "50_FIRST_DATES",
            "THE_FIRM",
        ]
        
        for label in valid_labels:
            assert identifier.is_generic_label(label) is False
    
    def test_title_normalization(self, test_config):
        """Test title normalization and cleaning."""
        identifier = MediaIdentifier(test_config)
        
        test_cases = [
            ("BLAZING_SADDLES", "BLAZING SADDLES"),
            ("50.First.Dates", "50 First Dates"),
            ("The-Matrix-DVD", "The Matrix"),
            ("Movie Title BLURAY DISC 1", "Movie Title"),
        ]
        
        for input_title, expected in test_cases:
            result = identifier.normalize_title(input_title)
            assert result == expected


class TestIntegrationWithRealExamples:
    """Integration tests using the real media examples."""
    
    def test_enhanced_metadata_extraction_structure(self):
        """Test that enhanced metadata extraction handles missing files gracefully."""
        extractor = EnhancedDiscMetadataExtractor()
        
        # Test with non-existent path (should not crash)
        fake_path = Path("/nonexistent/disc/path")
        metadata = extractor.extract_all_metadata(fake_path)
        
        assert isinstance(metadata, EnhancedDiscMetadata)
        # All fields should be None/empty for non-existent path
        assert metadata.volume_id is None
        assert metadata.disc_name is None
        
    def test_title_selector_with_no_titles(self):
        """Test title selector behavior with empty title list."""
        criteria = SelectionCriteria()
        selector = IntelligentTitleSelector(criteria)
        
        selection = selector.select_content([], None, ContentType.UNKNOWN)
        
        assert selection.content_type == ContentType.UNKNOWN
        assert len(selection.main_titles) == 0
        assert len(selection.extra_titles) == 0
        assert selection.confidence > 0  # Should have some minimal confidence
        
    @pytest.mark.asyncio
    async def test_identification_fallback_chain(self, test_config):
        """Test the complete identification fallback chain."""
        identifier = MediaIdentifier(test_config)
        
        # Test with empty title candidates (should return None)
        result = await identifier.identify_disc_content([])
        assert result is None
        
        # Test with generic labels only (should return None)
        generic_candidates = ["LOGICAL_VOLUME_ID", "DVD_VIDEO"]
        result = await identifier.identify_disc_content(generic_candidates)
        assert result is None