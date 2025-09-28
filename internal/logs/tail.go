package logs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

type TailOptions struct {
	Offset int64
	Limit  int
	Follow bool
	Wait   time.Duration
}

type TailResult struct {
	Lines  []string
	Offset int64
}

func Tail(ctx context.Context, path string, opts TailOptions) (TailResult, error) {
	result := TailResult{Offset: opts.Offset}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.Offset = 0
			return result, nil
		}
		return result, fmt.Errorf("stat log file: %w", err)
	}

	if info.IsDir() {
		return result, fmt.Errorf("log path %q is a directory", path)
	}

	// Ensure wait is non-negative
	if opts.Wait < 0 {
		opts.Wait = 0
	}

	if opts.Offset < 0 {
		lines, offset, err := readLastLines(path, opts.Limit)
		if err != nil {
			return result, err
		}
		result.Lines = lines
		result.Offset = offset
		if opts.Follow && opts.Wait > 0 && len(lines) == 0 {
			// Continue to wait for incoming lines with the new offset
			return waitForLines(ctx, path, result.Offset, opts.Wait)
		}
		return result, nil
	}

	return readFromOffset(ctx, path, opts.Offset, opts.Follow, opts.Wait)
}

func readLastLines(path string, limit int) ([]string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("stat log file: %w", err)
	}
	size := info.Size()

	if limit <= 0 {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return nil, 0, fmt.Errorf("seek log file: %w", err)
		}
		return nil, size, nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	ring := make([]string, limit)
	count := 0
	idx := 0
	for scanner.Scan() {
		ring[idx] = scanner.Text()
		idx = (idx + 1) % limit
		if count < limit {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("read log file: %w", err)
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return nil, 0, fmt.Errorf("seek log file: %w", err)
	}

	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, 0, fmt.Errorf("determine log offset: %w", err)
	}

	lines := make([]string, count)
	if count == limit {
		for i := 0; i < count; i++ {
			lines[i] = ring[(idx+i)%limit]
		}
	} else {
		copy(lines, ring[:count])
	}

	return lines, offset, nil
}

func readFromOffset(ctx context.Context, path string, offset int64, follow bool, wait time.Duration) (TailResult, error) {
	result := TailResult{Offset: offset}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.Offset = 0
			return result, nil
		}
		return result, fmt.Errorf("stat log file: %w", err)
	}

	size := info.Size()
	if offset < 0 || offset > size {
		offset = size
	}

	lines, newOffset, err := readForward(path, offset)
	if err != nil {
		return result, err
	}

	result.Lines = lines
	result.Offset = newOffset

	if follow && wait > 0 && len(lines) == 0 {
		return waitForLines(ctx, path, newOffset, wait)
	}

	return result, nil
}

func readForward(path string, offset int64) ([]string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("seek log file: %w", err)
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("read log file: %w", err)
	}

	newOffset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, 0, fmt.Errorf("determine log offset: %w", err)
	}

	return lines, newOffset, nil
}

func waitForLines(ctx context.Context, path string, offset int64, wait time.Duration) (TailResult, error) {
	deadline := time.Now().Add(wait)
	if wait == 0 {
		deadline = time.Now()
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	result := TailResult{Offset: offset}

	for {
		lines, newOffset, err := readForward(path, offset)
		if err != nil {
			return result, err
		}
		if len(lines) > 0 {
			result.Lines = lines
			result.Offset = newOffset
			return result, nil
		}

		if time.Now().After(deadline) {
			result.Offset = newOffset
			return result, nil
		}

		select {
		case <-ctx.Done():
			result.Offset = newOffset
			return result, ctx.Err()
		case <-ticker.C:
		}
	}
}
