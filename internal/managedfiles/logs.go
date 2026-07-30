package managedfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"scriptboard/internal/logstream"
)

var ErrLogSourceChanged = errors.New("日志文件已经轮转或替换")

const fileLogPollInterval = 500 * time.Millisecond

type LogSource struct {
	store         *Store
	relative      string
	sourceVersion string
}

func (s *Store) OpenLogSource(relative string) (*LogSource, error) {
	file, _, err := s.openLogRegular(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	version, err := logFileVersion(file)
	if err != nil {
		return nil, err
	}
	return &LogSource{store: s, relative: relative, sourceVersion: version}, nil
}

func (s *LogSource) Metadata() logstream.Metadata {
	return logstream.Metadata{
		Kind: "file", Name: s.relative, Technical: s.relative,
		SourceVersion: s.sourceVersion, Running: true,
	}
}

func (s *LogSource) History(ctx context.Context, before string) (logstream.Page, error) {
	file, info, err := s.store.openLogRegular(s.relative)
	if err != nil {
		return logstream.Page{}, err
	}
	defer file.Close()
	version, err := logFileVersion(file)
	if err != nil {
		return logstream.Page{}, err
	}
	if version != s.sourceVersion {
		return logstream.Page{}, ErrLogSourceChanged
	}
	end := info.Size()
	if before != "" {
		cursor, err := logstream.DecodeCursor(before)
		if err != nil {
			return logstream.Page{}, err
		}
		if cursor.Kind != "file" ||
			cursor.SourceVersion != version ||
			cursor.Offset > end ||
			cursor.Digest == "" ||
			cursor.Digest != logFileBoundaryDigest(file, cursor.Offset) {
			return logstream.Page{}, ErrLogSourceChanged
		}
		end = cursor.Offset
	}
	page := logstream.Page{Entries: make([]logstream.Entry, 0, logstream.DefaultPageLines), SourceVersion: version}
	nextEnd := end
	totalBytes := 0
	for nextEnd > 0 && len(page.Entries) < logstream.DefaultPageLines && totalBytes < logstream.DefaultPageBytes {
		if err := ctx.Err(); err != nil {
			return logstream.Page{}, err
		}
		start, raw, continuation, err := readPreviousLogSegment(file, nextEnd)
		if err != nil {
			return logstream.Page{}, err
		}
		entry := logstream.NewEntry(logstream.SourceFile, raw, continuation)
		if len(page.Entries) > 0 && totalBytes+len(entry.Text) > logstream.DefaultPageBytes {
			break
		}
		cursor, err := logstream.EncodeCursor(logstream.Cursor{
			Kind: "file", SourceVersion: version, Offset: nextEnd, Source: logstream.SourceFile,
			Digest: logFileBoundaryDigest(file, nextEnd),
		})
		if err != nil {
			return logstream.Page{}, err
		}
		entry.Cursor = cursor
		page.Entries = append(page.Entries, logstream.Entry{})
		copy(page.Entries[1:], page.Entries[:len(page.Entries)-1])
		page.Entries[0] = entry
		totalBytes += len(entry.Text)
		nextEnd = start
	}
	page.HasMore = nextEnd > 0
	if len(page.Entries) > 0 {
		page.Before, err = logstream.EncodeCursor(logstream.Cursor{
			Kind: "file", SourceVersion: version, Offset: nextEnd, Source: logstream.SourceFile,
			Digest: logFileBoundaryDigest(file, nextEnd),
		})
		if err != nil {
			return logstream.Page{}, err
		}
	}
	return page, nil
}

func (s *LogSource) Follow(ctx context.Context, after string, emit func(logstream.Event) error) error {
	file, info, err := s.store.openLogRegular(s.relative)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	version, err := logFileVersion(file)
	if err != nil {
		return err
	}
	offset := info.Size()
	if after != "" {
		cursor, cursorErr := logstream.DecodeCursor(after)
		if cursorErr != nil {
			return cursorErr
		}
		if cursor.Kind == "file" &&
			cursor.SourceVersion == version &&
			cursor.Offset <= info.Size() &&
			cursor.Digest != "" &&
			cursor.Digest == logFileBoundaryDigest(file, cursor.Offset) {
			offset = cursor.Offset
		} else {
			if err := emit(logstream.Event{
				Kind: logstream.EventGap, State: "source_changed",
				Message: "日志文件已经轮转或截断，无法证明断线期间的日志连续性。",
			}); err != nil {
				return err
			}
			offset = 0
		}
	}
	continuing, err := logOffsetContinuesLine(file, offset)
	if err != nil {
		return err
	}
	pending := make([]byte, 0, 64<<10)
	bufferStart := offset
	readOffset := offset
	boundary, err := readLogBoundary(file, readOffset)
	if err != nil {
		return err
	}
	if err := emit(logstream.Event{Kind: logstream.EventState, State: "live"}); err != nil {
		return err
	}

	waiting := false
	ticker := time.NewTicker(fileLogPollInterval)
	defer ticker.Stop()
	for {
		continuous, boundaryErr := logBoundaryMatches(file, readOffset, boundary)
		if boundaryErr != nil {
			return boundaryErr
		}
		if !continuous {
			readOffset, bufferStart = 0, 0
			pending = pending[:0]
			continuing = false
			boundary = nil
			if err := emit(logstream.Event{
				Kind: logstream.EventState, State: "truncated",
				Message: "日志文件已截断，已从文件开头继续。",
			}); err != nil {
				return err
			}
		}
		if err := readAvailableLogBytes(ctx, file, version, &readOffset, &bufferStart, &pending, &continuing, emit); err != nil {
			return err
		}
		nextBoundary, boundaryReadErr := readLogBoundary(file, readOffset)
		if boundaryReadErr != nil && !errors.Is(boundaryReadErr, io.ErrUnexpectedEOF) {
			return boundaryReadErr
		}
		if boundaryReadErr == nil {
			boundary = nextBoundary
		} else {
			boundary = nil
		}
		current, currentInfo, openErr := s.store.openLogRegular(s.relative)
		if openErr != nil {
			if !waiting {
				if err := emit(logstream.Event{
					Kind: logstream.EventState, State: "waiting",
					Message: "日志文件暂时不存在，正在等待同一路径重新创建。",
				}); err != nil {
					return err
				}
				waiting = true
			}
		} else {
			currentVersion, versionErr := logFileVersion(current)
			if versionErr != nil {
				current.Close()
				return versionErr
			}
			switch {
			case currentVersion != version:
				if err := readAvailableLogBytes(
					ctx, file, version,
					&readOffset, &bufferStart, &pending, &continuing, emit,
				); err != nil {
					current.Close()
					return err
				}
				if err := emitPendingLogLine(
					file, version, readOffset, &bufferStart, &pending, continuing, emit,
				); err != nil {
					current.Close()
					return err
				}
				if err := file.Close(); err != nil {
					current.Close()
					return err
				}
				file = current
				version = currentVersion
				readOffset, bufferStart = 0, 0
				pending = pending[:0]
				continuing = false
				boundary = nil
				waiting = false
				if err := emit(logstream.Event{
					Kind: logstream.EventState, State: "rotated",
					Message: "日志文件已轮转，正在跟随同一路径的新文件。",
				}); err != nil {
					return err
				}
			case currentInfo.Size() < readOffset:
				current.Close()
				readOffset, bufferStart = 0, 0
				pending = pending[:0]
				continuing = false
				boundary = nil
				waiting = false
				if err := emit(logstream.Event{
					Kind: logstream.EventState, State: "truncated",
					Message: "日志文件已截断，已从文件开头继续。",
				}); err != nil {
					return err
				}
			default:
				current.Close()
				if waiting {
					waiting = false
					if err := emit(logstream.Event{Kind: logstream.EventState, State: "live"}); err != nil {
						return err
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func emitPendingLogLine(
	file *os.File,
	version string,
	readOffset int64,
	bufferStart *int64,
	pending *[]byte,
	continuation bool,
	emit func(logstream.Event) error,
) error {
	if len(*pending) == 0 {
		return nil
	}
	raw := *pending
	if raw[len(raw)-1] == '\r' {
		raw = raw[:len(raw)-1]
	}
	entry := logstream.NewEntry(logstream.SourceFile, raw, continuation)
	cursor, err := logstream.EncodeCursor(logstream.Cursor{
		Kind: "file", SourceVersion: version, Offset: readOffset, Source: logstream.SourceFile,
		Digest: logFileBoundaryDigest(file, readOffset),
	})
	if err != nil {
		return err
	}
	entry.Cursor = cursor
	if err := emit(logstream.Event{Kind: logstream.EventEntry, Entry: &entry}); err != nil {
		return err
	}
	*pending = (*pending)[:0]
	*bufferStart = readOffset
	return nil
}

func readLogBoundary(file *os.File, offset int64) ([]byte, error) {
	if offset <= 0 {
		return nil, nil
	}
	start := max(int64(0), offset-64)
	boundary := make([]byte, offset-start)
	count, err := file.ReadAt(boundary, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if count != len(boundary) {
		return nil, io.ErrUnexpectedEOF
	}
	return boundary, nil
}

func logFileBoundaryDigest(file *os.File, offset int64) string {
	boundary, err := readLogBoundary(file, offset)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(boundary)
	return hex.EncodeToString(digest[:8])
}

func logBoundaryMatches(file *os.File, offset int64, expected []byte) (bool, error) {
	actual, err := readLogBoundary(file, offset)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(actual, expected), nil
}

func readAvailableLogBytes(
	ctx context.Context,
	file *os.File,
	version string,
	readOffset, bufferStart *int64,
	pending *[]byte,
	continuing *bool,
	emit func(logstream.Event) error,
) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	buffer := make([]byte, 64<<10)
	for *readOffset < info.Size() {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := file.ReadAt(buffer, *readOffset)
		if count > 0 {
			*pending = append(*pending, buffer[:count]...)
			*readOffset += int64(count)
			if err := emitCompleteLogLines(file, version, bufferStart, pending, continuing, emit); err != nil {
				return err
			}
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if count == 0 {
			break
		}
	}
	return nil
}

func emitCompleteLogLines(
	file *os.File,
	version string,
	bufferStart *int64,
	pending *[]byte,
	continuing *bool,
	emit func(logstream.Event) error,
) error {
	for len(*pending) > 0 {
		lineEnd := bytes.IndexByte(*pending, '\n')
		if lineEnd < 0 && len(*pending) < logstream.MaxEntryBytes {
			return nil
		}
		consume := lineEnd + 1
		rawEnd := lineEnd
		if lineEnd < 0 {
			consume = logstream.MaxEntryBytes
			rawEnd = consume
		}
		raw := (*pending)[:rawEnd]
		if lineEnd >= 0 && len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
		cursorOffset := *bufferStart + int64(consume)
		entry := logstream.NewEntry(logstream.SourceFile, raw, *continuing)
		cursor, err := logstream.EncodeCursor(logstream.Cursor{
			Kind: "file", SourceVersion: version, Offset: cursorOffset, Source: logstream.SourceFile,
			Digest: logFileBoundaryDigest(file, cursorOffset),
		})
		if err != nil {
			return err
		}
		entry.Cursor = cursor
		if err := emit(logstream.Event{Kind: logstream.EventEntry, Entry: &entry}); err != nil {
			return err
		}
		*pending = (*pending)[consume:]
		*bufferStart = cursorOffset
		*continuing = lineEnd < 0
	}
	return nil
}

func logOffsetContinuesLine(file *os.File, offset int64) (bool, error) {
	if offset <= 0 {
		return false, nil
	}
	var previous [1]byte
	if _, err := file.ReadAt(previous[:], offset-1); err != nil {
		return false, err
	}
	return previous[0] != '\n', nil
}

func readPreviousLogSegment(file *os.File, end int64) (int64, []byte, bool, error) {
	if end <= 0 {
		return 0, nil, false, io.EOF
	}
	contentEnd := end
	var suffix [2]byte
	readStart := max(int64(0), end-2)
	count, err := file.ReadAt(suffix[:end-readStart], readStart)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, false, err
	}
	tail := suffix[:count]
	if len(tail) > 0 && tail[len(tail)-1] == '\n' {
		contentEnd--
		if len(tail) > 1 && tail[len(tail)-2] == '\r' {
			contentEnd--
		}
	}
	start := max(int64(0), contentEnd-int64(logstream.MaxEntryBytes))
	buffer := make([]byte, contentEnd-start)
	if len(buffer) > 0 {
		if _, err := file.ReadAt(buffer, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, nil, false, err
		}
	}
	if index := strings.LastIndexByte(string(buffer), '\n'); index >= 0 {
		lineStart := start + int64(index) + 1
		raw := buffer[index+1:]
		if len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
		return lineStart, raw, false, nil
	}
	return start, buffer, start > 0, nil
}

func logFileVersion(file *os.File) (string, error) {
	identity, err := fileIdentity(file)
	if err != nil {
		return "", fmt.Errorf("读取日志文件身份: %w", err)
	}
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:16]), nil
}

func (s *Store) openLogRegular(relative string) (*os.File, os.FileInfo, error) {
	target, info, err := s.resolveEntry(relative)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("只能查看普通日志文件")
	}
	file, err := openLogFile(target)
	if err != nil {
		return nil, nil, fmt.Errorf("打开日志文件: %w", err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("检查日志文件: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		file.Close()
		return nil, nil, fmt.Errorf("日志文件在打开期间发生变化")
	}
	return file, openedInfo, nil
}

var _ logstream.Source = (*LogSource)(nil)
