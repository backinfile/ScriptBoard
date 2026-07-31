package app

import (
	"cmp"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"scriptboard/internal/hostfiles"
)

type fileCategory string

const (
	fileCategoryDirectory  fileCategory = "directory"
	fileCategoryRestricted fileCategory = "restricted"
	fileCategoryScript     fileCategory = "script"
	fileCategoryImage      fileCategory = "image"
	fileCategoryText       fileCategory = "text"
	fileCategoryOther      fileCategory = "other"
)

type listedFile struct {
	hostfiles.Entry
	Path     string
	Category fileCategory
}

type fileNamePart struct {
	Text  string
	Match bool
}

func prepareFileListing(entries []hostfiles.Entry, _ string, query, sortField, direction string, showHidden bool) []listedFile {
	result := make([]listedFile, 0, len(entries))
	for _, entry := range entries {
		if !showHidden && entry.Hidden {
			continue
		}
		if query != "" && !fileNameMatches(entry.Name, query) {
			continue
		}
		path := entry.Path
		result = append(result, listedFile{
			Entry:    entry,
			Path:     path,
			Category: classifyFile(entry, path),
		})
	}
	if sortField == "" {
		return result
	}
	sort.SliceStable(result, func(left, right int) bool {
		comparison := compareListedFiles(result[left], result[right], sortField)
		if direction == "desc" && result[left].Category != fileCategoryDirectory && result[right].Category != fileCategoryDirectory {
			comparison = -comparison
		}
		return comparison < 0
	})
	return result
}

func normalizeFileSort(field, direction string) (string, string) {
	switch field {
	case "name", "type", "size", "modified":
	default:
		field = ""
	}
	if field == "" || direction != "desc" {
		direction = "asc"
	}
	return field, direction
}

func compareListedFiles(left, right listedFile, field string) int {
	if left.Category == fileCategoryDirectory && right.Category != fileCategoryDirectory {
		return -1
	}
	if left.Category != fileCategoryDirectory && right.Category == fileCategoryDirectory {
		return 1
	}

	comparison := 0
	switch field {
	case "type":
		comparison = cmp.Compare(fileCategoryRank(left.Category), fileCategoryRank(right.Category))
	case "size":
		comparison = cmp.Compare(left.Size, right.Size)
	case "modified":
		comparison = left.ModifiedAt.Compare(right.ModifiedAt)
	}
	if field == "name" || comparison == 0 {
		comparison = naturalNameCompare(left.Name, right.Name)
	}
	return comparison
}

func classifyFile(entry hostfiles.Entry, path string) fileCategory {
	switch entry.Kind {
	case hostfiles.Directory:
		return fileCategoryDirectory
	case hostfiles.Restricted:
		return fileCategoryRestricted
	}
	if isScriptExtension(path) {
		return fileCategoryScript
	}
	if isImagePreviewExtension(path) {
		return fileCategoryImage
	}
	if isTextPreviewExtension(path) {
		return fileCategoryText
	}
	return fileCategoryOther
}

func isImagePreviewExtension(path string) bool {
	switch strings.ToLower(hostfiles.Extension(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func fileCategoryRank(category fileCategory) int {
	switch category {
	case fileCategoryDirectory:
		return 0
	case fileCategoryRestricted:
		return 1
	case fileCategoryScript:
		return 2
	case fileCategoryImage:
		return 3
	case fileCategoryText:
		return 4
	default:
		return 5
	}
}

func fileCategoryIcon(category fileCategory) string {
	switch category {
	case fileCategoryDirectory:
		return "folder"
	case fileCategoryRestricted:
		return "file-lock-2"
	case fileCategoryScript:
		return "file-terminal"
	case fileCategoryImage:
		return "image"
	case fileCategoryText:
		return "file-text"
	default:
		return "file"
	}
}

func fileCategoryLabel(locale webLocale, category fileCategory) string {
	return webText(locale, "files.type."+string(category))
}

func fileSortSummary(locale webLocale, field, direction string) string {
	if field == "" {
		return webText(locale, "files.natural_order")
	}
	fieldLabel := webText(locale, "files.sort."+field)
	directionKey := "files.ascending"
	if direction == "desc" {
		directionKey = "files.descending"
	}
	directionLabel := webText(locale, directionKey)
	return fieldLabel + " · " + directionLabel
}

func filesStateURL(relative, query, sortField, direction string, showHidden bool, page int) string {
	values := url.Values{}
	if relative != "" {
		values.Set("path", relative)
	}
	if query != "" {
		values.Set("q", query)
	}
	if sortField != "" {
		values.Set("sort", sortField)
		values.Set("direction", direction)
	}
	if showHidden {
		values.Set("show_hidden", "1")
	}
	if page > 1 {
		values.Set("page", strconv.Itoa(page))
	}
	if len(values) == 0 {
		return "/resources/files"
	}
	return "/resources/files?" + values.Encode()
}

func naturalNameCompare(left, right string) int {
	a, b := strings.ToLower(left), strings.ToLower(right)
	for len(a) > 0 && len(b) > 0 {
		if isASCIIDigit(a[0]) && isASCIIDigit(b[0]) {
			ai, bi := 0, 0
			for ai < len(a) && isASCIIDigit(a[ai]) {
				ai++
			}
			for bi < len(b) && isASCIIDigit(b[bi]) {
				bi++
			}
			an, bn := strings.TrimLeft(a[:ai], "0"), strings.TrimLeft(b[:bi], "0")
			if an == "" {
				an = "0"
			}
			if bn == "" {
				bn = "0"
			}
			if len(an) != len(bn) {
				return cmp.Compare(len(an), len(bn))
			}
			if an != bn {
				return strings.Compare(an, bn)
			}
			a, b = a[ai:], b[bi:]
			continue
		}
		if a[0] != b[0] {
			return cmp.Compare(a[0], b[0])
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

func splitFileNameMatches(name, query string) []fileNamePart {
	nameRunes := []rune(name)
	queryRunes := []rune(query)
	if len(queryRunes) == 0 || len(queryRunes) > len(nameRunes) {
		return []fileNamePart{{Text: name}}
	}

	foldedName := foldRunes(nameRunes)
	foldedQuery := foldRunes(queryRunes)
	parts := make([]fileNamePart, 0, 3)
	start := 0
	for index := 0; index+len(foldedQuery) <= len(foldedName); {
		if !equalRunes(foldedName[index:index+len(foldedQuery)], foldedQuery) {
			index++
			continue
		}
		if start < index {
			parts = append(parts, fileNamePart{Text: string(nameRunes[start:index])})
		}
		parts = append(parts, fileNamePart{Text: string(nameRunes[index : index+len(queryRunes)]), Match: true})
		index += len(queryRunes)
		start = index
	}
	if start < len(nameRunes) {
		parts = append(parts, fileNamePart{Text: string(nameRunes[start:])})
	}
	if len(parts) == 0 {
		return []fileNamePart{{Text: name}}
	}
	return parts
}

func fileNameMatches(name, query string) bool {
	for _, part := range splitFileNameMatches(name, query) {
		if part.Match {
			return true
		}
	}
	return false
}

func foldRunes(values []rune) []rune {
	result := make([]rune, len(values))
	for index, value := range values {
		result[index] = unicode.ToLower(value)
	}
	return result
}

func equalRunes(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
