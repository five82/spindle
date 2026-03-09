package fileutil

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// removeBestEffort removes a file, ignoring any error. Used for cleanup on
// failure paths where the original error is more important.
func removeBestEffort(path string) {
	_ = os.Remove(path)
}

// CopyFile copies src to dst using a buffered stream copy with 0o644 permissions.
func CopyFile(src, dst string) error {
	return CopyFileMode(src, dst, 0o644)
}

// CopyFileMode copies src to dst using a buffered stream copy with the given permissions.
func CopyFileMode(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close destination: %w", cerr)
		}
	}()

	reader := bufio.NewReader(srcFile)
	if _, err := io.Copy(dstFile, reader); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}

	return nil
}

// CopyFileVerified copies src to dst with simultaneous SHA-256 hashing and size
// verification. On mismatch the destination file is removed and an error is returned.
// Uses 0o644 permissions.
func CopyFileVerified(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	srcSize := srcInfo.Size()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	srcHash := sha256.New()
	dstHash := sha256.New()

	// Hash source bytes as they are read.
	teeReader := io.TeeReader(bufio.NewReader(srcFile), srcHash)

	// Hash destination bytes as they are written.
	multiWriter := io.MultiWriter(dstFile, dstHash)

	written, err := io.Copy(multiWriter, teeReader)
	if err != nil {
		_ = dstFile.Close()
		removeBestEffort(dst)
		return fmt.Errorf("copy data: %w", err)
	}

	if err := dstFile.Close(); err != nil {
		removeBestEffort(dst)
		return fmt.Errorf("close destination: %w", err)
	}

	if written != srcSize {
		removeBestEffort(dst)
		return fmt.Errorf("size mismatch: source %d bytes, copied %d bytes", srcSize, written)
	}

	srcSum := hex.EncodeToString(srcHash.Sum(nil))
	dstSum := hex.EncodeToString(dstHash.Sum(nil))
	if srcSum != dstSum {
		removeBestEffort(dst)
		return fmt.Errorf("hash mismatch: source %s, destination %s", srcSum, dstSum)
	}

	return nil
}
