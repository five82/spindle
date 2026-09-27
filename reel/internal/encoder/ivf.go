package encoder

import (
	"encoding/binary"
	"fmt"
	"io"
)

// writeIVFHeader writes a 32-byte IVF file header.
func writeIVFHeader(w io.Writer, width, height uint16, fpsNum, fpsDen uint32) error {
	var hdr [32]byte
	copy(hdr[0:4], "DKIF")
	binary.LittleEndian.PutUint16(hdr[4:6], 0)  // version
	binary.LittleEndian.PutUint16(hdr[6:8], 32) // header size
	copy(hdr[8:12], "AV01")
	binary.LittleEndian.PutUint16(hdr[12:14], width)
	binary.LittleEndian.PutUint16(hdr[14:16], height)
	binary.LittleEndian.PutUint32(hdr[16:20], fpsNum)
	binary.LittleEndian.PutUint32(hdr[20:24], fpsDen)
	// bytes 24-28: number of frames (patched during merge, 0 here)
	// bytes 28-32: unused
	_, err := w.Write(hdr[:])
	if err != nil {
		return fmt.Errorf("failed to write IVF header: %w", err)
	}
	return nil
}

// PeakSecondBps returns the highest one-second bitrate in an IVF file,
// bucketing frame payloads by displayed second (frame pts is the frame index
// in EncodeChunkToIVF's output). Hardware decoders are provisioned for the
// signaled level's instantaneous rate, so the target-quality search gates
// probes on this rather than on the chunk average.
func PeakSecondBps(r io.Reader, fpsNum, fpsDen uint32) (float64, error) {
	if fpsNum == 0 || fpsDen == 0 {
		return 0, fmt.Errorf("invalid frame rate %d/%d", fpsNum, fpsDen)
	}
	var hdr [32]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, fmt.Errorf("failed to read IVF header: %w", err)
	}
	buckets := make(map[uint64]uint64)
	var frameHdr [12]byte
	for {
		if _, err := io.ReadFull(r, frameHdr[:]); err == io.EOF {
			break
		} else if err != nil {
			return 0, fmt.Errorf("failed to read IVF frame header: %w", err)
		}
		size := binary.LittleEndian.Uint32(frameHdr[0:4])
		pts := binary.LittleEndian.Uint64(frameHdr[4:12])
		second := pts * uint64(fpsDen) / uint64(fpsNum)
		buckets[second] += uint64(size)
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return 0, fmt.Errorf("failed to skip IVF frame data: %w", err)
		}
	}
	var peak uint64
	for _, bytes := range buckets {
		if bytes > peak {
			peak = bytes
		}
	}
	return float64(peak) * 8, nil
}

// IVFVideoBytes returns the total compressed frame payload of an IVF file,
// excluding the file and per-frame container headers. Bits-per-pixel measures
// what the encoder spent on picture data, so the container must not count.
func IVFVideoBytes(r io.Reader) (uint64, error) {
	var hdr [32]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, fmt.Errorf("failed to read IVF header: %w", err)
	}
	var total uint64
	var frameHdr [12]byte
	for {
		if _, err := io.ReadFull(r, frameHdr[:]); err == io.EOF {
			break
		} else if err != nil {
			return 0, fmt.Errorf("failed to read IVF frame header: %w", err)
		}
		size := binary.LittleEndian.Uint32(frameHdr[0:4])
		total += uint64(size)
		if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
			return 0, fmt.Errorf("failed to skip IVF frame data: %w", err)
		}
	}
	return total, nil
}

// writeIVFFrame writes a single IVF frame (4 bytes size + 8 bytes pts + data).
func writeIVFFrame(w io.Writer, data []byte, pts int64) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("failed to write IVF frame size: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, uint64(pts)); err != nil {
		return fmt.Errorf("failed to write IVF frame timestamp: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write IVF frame data: %w", err)
	}
	return nil
}
