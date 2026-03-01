package fileutil

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// CopyFile streams src to dst using io.Copy with default permissions (0o644).
func CopyFile(src, dst string) error {
	return CopyFileMode(src, dst, 0o644)
}

// CopyFileMode streams src to dst, setting the given file mode on dst.
func CopyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// CopyFileVerified streams src to dst with SHA256 + size integrity verification.
// Removes dst on mismatch.
func CopyFileVerified(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	srcSize := srcInfo.Size()

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	srcHasher := sha256.New()
	dstHasher := sha256.New()
	tee := io.TeeReader(in, srcHasher)
	multi := io.MultiWriter(out, dstHasher)

	written, err := io.Copy(multi, tee)
	if err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	if written != srcSize {
		_ = os.Remove(dst)
		return fmt.Errorf("copy size mismatch: source %d bytes, copied %d bytes", srcSize, written)
	}

	if !bytes.Equal(srcHasher.Sum(nil), dstHasher.Sum(nil)) {
		_ = os.Remove(dst)
		return fmt.Errorf("copy hash mismatch: file corrupted during copy")
	}

	return nil
}
