package auditgather

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestProfileDifferenceIncludesAudioSubtitleAndResolution(t *testing.T) {
	base := ProfileSummary{VideoCodec: "h264", Width: 1920, Height: 1080, AudioStreams: []AudioProfile{{Codec: "aac", Channels: 2}}, SubtitleStreams: []SubtitleProfile{{Codec: "subrip", Language: "en"}}}
	other := ProfileSummary{VideoCodec: "hevc", Width: 1280, Height: 720, AudioStreams: []AudioProfile{{Codec: "aac", Channels: 2}, {Codec: "aac", Channels: 2}}, SubtitleStreams: []SubtitleProfile{{Codec: "subrip", Language: "fr", IsForced: true}}}
	differences := strings.Join(describeProfileDifferences(base, other), "; ")
	for _, want := range []string{"video codec", "resolution", "audio streams", "subtitle streams", "fr (forced)"} {
		if !strings.Contains(differences, want) {
			t.Errorf("missing %q in %s", want, differences)
		}
	}
	if got := describeSubtitleStreams(nil); got != "none" {
		t.Fatal(got)
	}
	if profilesEqual(base, other) {
		t.Fatal("different profiles considered equal")
	}
}

func TestEpisodeConsistencyMajorityWithProbeErrors(t *testing.T) {
	probe := func(key, codec string) MediaFileProbe {
		return MediaFileProbe{EpisodeKey: key, Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: codec, Width: 1920, Height: 1080}, {CodecType: "audio", CodecName: "aac", Channels: 2, Tags: map[string]string{"language": "eng", "title": "Director Commentary"}}, {CodecType: "subtitle", CodecName: "subrip", Tags: map[string]string{"language": "en"}, Disposition: map[string]int{"forced": 1}}}}}
	}
	probes := []MediaFileProbe{probe("e1", "h264"), probe("e2", "hevc"), probe("e3", "h264"), {EpisodeKey: "e4", Error: "probe failed"}}
	consistency := computeEpisodeConsistency(probes)
	if consistency == nil || consistency.MajorityCount != 2 || consistency.TotalEpisodes != 3 || len(consistency.Deviations) != 1 || consistency.Deviations[0].EpisodeKey != "e2" {
		t.Fatalf("consistency: %+v", consistency)
	}
	if !consistency.MajorityProfile.AudioStreams[0].IsCommentary || !consistency.MajorityProfile.SubtitleStreams[0].IsForced {
		t.Fatalf("stream profiles: %+v", consistency.MajorityProfile)
	}
	compressed, omitted := compressMediaProbes(probes, consistency)
	if omitted != 1 || len(compressed) != 3 || !compressed[0].Representative {
		t.Fatalf("compressed: %+v omitted=%d", compressed, omitted)
	}
	if computeEpisodeConsistency(probes[:1]) != nil {
		t.Fatal("one probe should have no consistency")
	}
}

func TestMediaStreamHDRMetadataVariants(t *testing.T) {
	for _, s := range []ffprobe.Stream{{ColorTransfer: "SMPTE2084"}, {ColorTransfer: "arib-std-b67"}, {ColorPrimaries: "bt2020"}, {SideDataList: []ffprobe.SideData{{Type: "Mastering display metadata"}}}, {SideDataList: []ffprobe.SideData{{Type: "Content light metadata"}}}} {
		if !mediaStreamHDR(s) {
			t.Fatalf("not HDR: %+v", s)
		}
	}
	if mediaStreamHDR(ffprobe.Stream{ColorPrimaries: "bt709"}) {
		t.Fatal("SDR considered HDR")
	}
}
