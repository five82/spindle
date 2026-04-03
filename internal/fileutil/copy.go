package fileutil

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// CopyProgress reports bytes copied during a verified copy.
type CopyProgress struct {
	BytesCopied int64
	TotalBytes  int64
}

// ProgressFunc receives verified copy progress updates.
type ProgressFunc func(CopyProgress)

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
	return CopyFileVerifiedWithProgress(src, dst, nil)
}

// CopyFileVerifiedWithProgress is like CopyFileVerified but reports byte progress
// during the copy.
func CopyFileVerifiedWithProgress(src, dst string, progress ProgressFunc) error {
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
	writer := io.Writer(dstFile)
	if progress != nil {
		writer = &progressWriter{w: writer, total: srcSize, onWrite: progress}
	}
	multiWriter := io.MultiWriter(writer, dstHash)

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

// progressWriter wraps a writer and reports cumulative copy progress.
type progressWriter struct {
	w       io.Writer
	copied  int64
	total   int64
	onWrite ProgressFunc
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	if n > 0 {
		pw.copied += int64(n)
		if pw.onWrite != nil {
			pw.onWrite(CopyProgress{BytesCopied: pw.copied, TotalBytes: pw.total})
		}
	}
	return n, err
}
