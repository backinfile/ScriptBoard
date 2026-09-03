package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"scriptboard/internal/hostfiles"
)

const textPreviewChunkBytes = 16 << 10

type textPreviewChunk struct {
	Content    string `json:"content"`
	NextOffset string `json:"nextOffset,omitempty"`
	Version    string `json:"version"`
	HasMore    bool   `json:"hasMore"`
}

func (a *App) readTextPreviewChunk(ctx context.Context, path string, offset int64, expectedVersion string, forceTXT bool) (textPreviewChunk, error) {
	file, info, err := a.hostOpenRegular(ctx, path)
	if err != nil {
		return textPreviewChunk{}, err
	}
	defer file.Close()
	if offset < 0 || offset > info.Size() {
		return textPreviewChunk{}, errors.New("invalid text preview cursor")
	}
	versionInput := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UTC().UnixNano())
	digest := sha256.Sum256([]byte(versionInput))
	version := hex.EncodeToString(digest[:12])
	if expectedVersion != "" && expectedVersion != version {
		return textPreviewChunk{}, errors.New("text preview source changed")
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return textPreviewChunk{}, err
	}
	remaining := info.Size() - offset
	readLimit := int64(textPreviewChunkBytes + utf8.UTFMax - 1)
	if remaining < readLimit {
		readLimit = remaining
	}
	content, err := io.ReadAll(io.LimitReader(file, readLimit))
	if err != nil {
		return textPreviewChunk{}, err
	}
	length := min(len(content), textPreviewChunkBytes)
	if int64(length) == remaining {
		length = len(content)
	} else if !forceTXT {
		for length > 0 && !utf8.Valid(content[:length]) {
			length--
		}
	}
	content = content[:length]
	rawLength := len(content)
	if forceTXT {
		// Explicit TXT preview keeps arbitrary regular files viewable without relaxing normal text detection.
		content = sanitizeForcedTXTPreview(content)
	} else if (offset == 0 && !hostfiles.IsLikelyTextContent(content)) || (offset > 0 && !safeTextPreviewChunk(content)) {
		return textPreviewChunk{}, errors.New("file is not safe UTF-8 text")
	}
	next := offset + int64(rawLength)
	chunk := textPreviewChunk{Content: string(content), Version: version, HasMore: next < info.Size()}
	if chunk.HasMore {
		chunk.NextOffset = strconv.FormatInt(next, 10)
	}
	return chunk, nil
}

func sanitizeForcedTXTPreview(content []byte) []byte {
	text := strings.ToValidUTF8(string(content), "\uFFFD")
	return []byte(strings.Map(func(value rune) rune {
		if unicode.IsControl(value) && value != '\t' && value != '\n' && value != '\r' && value != '\f' {
			return '\uFFFD'
		}
		return value
	}, text))
}

func safeTextPreviewChunk(content []byte) bool {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return false
	}
	controls, runes := 0, 0
	for _, value := range string(content) {
		runes++
		if unicode.IsControl(value) && value != '\t' && value != '\n' && value != '\r' && value != '\f' {
			controls++
		}
	}
	return runes == 0 || controls*100 <= runes
}

func (a *App) textPreviewContent(response http.ResponseWriter, request *http.Request) {
	relative, err := a.hostCanonicalExisting(request.Context(), request.URL.Query().Get("path"))
	if err != nil {
		writeHostFileError(response, "Unable to preview file", err)
		return
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(request.URL.Query().Get("offset")), 10, 64)
	if err != nil || offset < 0 {
		http.Error(response, "invalid text preview cursor", http.StatusBadRequest)
		return
	}
	chunk, err := a.readTextPreviewChunk(request.Context(), relative, offset, strings.TrimSpace(request.URL.Query().Get("version")), request.URL.Query().Get("format") == "txt")
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "source changed") {
			status = http.StatusConflict
		}
		http.Error(response, err.Error(), status)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(response).Encode(chunk)
}
