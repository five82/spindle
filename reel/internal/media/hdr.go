package media

import "github.com/five82/reel/internal/video"

// IsAvailable reports whether native media probing is available.
func IsAvailable() bool {
	return true
}

// GetStreamHDRInfo returns HDR-related metadata from container/stream parameters only.
func GetStreamHDRInfo(path string) (*HDRInfo, error) {
	props, err := GetVideoProperties(path)
	if err != nil {
		return nil, err
	}
	hdr := props.HDRInfo
	return &hdr, nil
}

// RefineHDR returns HDR metadata refined from a decoded first frame, using
// container/stream metadata (typically VideoProperties.HDRInfo) as the base.
// Frame-level color tags read from a decoded frame can be more accurate than
// the container/codec-par tags that GetStreamHDRInfo reads, so the probe result
// is authoritative when available. inf may be nil to fall back to container-only
// values.
//
// Callers that already hold both the container properties and a video.Probe
// result should use this instead of GetHDRInfo to avoid a redundant container
// re-probe and a second first-frame decode. The encode orchestrator probes once
// and shares that result with both HDR analysis and the chunked pipeline.
func RefineHDR(container HDRInfo, inf *video.Info) HDRInfo {
	hdr := container
	if inf != nil {
		if inf.Is10Bit {
			depth := uint8(10)
			hdr.BitDepth = &depth
		}
		if inf.ColorPrimaries != nil {
			if name := colorPrimariesName(*inf.ColorPrimaries); name != "" {
				hdr.ColourPrimaries = name
			}
		}
		if inf.TransferCharacteristics != nil {
			if name := colorTransferName(*inf.TransferCharacteristics); name != "" {
				hdr.TransferCharacteristics = name
			}
		}
		if inf.MatrixCoefficients != nil {
			if name := colorSpaceName(*inf.MatrixCoefficients); name != "" {
				hdr.MatrixCoefficients = name
			}
		}
	}
	hdr.IsHDR = detectHDR(hdr.ColourPrimaries, hdr.TransferCharacteristics, hdr.MatrixCoefficients)
	return hdr
}

// GetHDRInfo returns HDR-related metadata using native libav probes. It is the
// path-based convenience for callers (such as output validation) that do not
// already have a shared video.Probe result; the encode orchestrator uses
// RefineHDR instead to avoid probing twice.
func GetHDRInfo(path string) (*HDRInfo, error) {
	container, err := GetStreamHDRInfo(path)
	if err != nil {
		return nil, err
	}
	inf, _ := video.Probe(path) // On failure RefineHDR falls back to container-only metadata.
	refined := RefineHDR(*container, inf)
	return &refined, nil
}

func colorPrimariesName(value int32) string {
	switch value {
	case 1:
		return "bt709"
	case 4:
		return "bt470m"
	case 5:
		return "bt470bg"
	case 6:
		return "smpte170m"
	case 7:
		return "smpte240m"
	case 9:
		return "bt2020"
	case 12:
		return "smpte431"
	case 22:
		return "ebu3213"
	default:
		return ""
	}
}

func colorTransferName(value int32) string {
	switch value {
	case 1:
		return "bt709"
	case 4:
		return "gamma22"
	case 5:
		return "gamma28"
	case 6:
		return "smpte170m"
	case 7:
		return "smpte240m"
	case 13:
		return "iec61966-2-1"
	case 14:
		return "bt2020-10"
	case 15:
		return "bt2020-12"
	case 16:
		return "smpte2084"
	case 18:
		return "arib-std-b67"
	default:
		return ""
	}
}

func colorSpaceName(value int32) string {
	switch value {
	case 1:
		return "bt709"
	case 4:
		return "fcc"
	case 5:
		return "bt470bg"
	case 6:
		return "smpte170m"
	case 7:
		return "smpte240m"
	case 9:
		return "bt2020nc"
	case 10:
		return "bt2020c"
	case 12:
		return "chromaticity-derived-nc"
	case 13:
		return "chromaticity-derived-c"
	case 14:
		return "ictcp"
	default:
		return ""
	}
}
