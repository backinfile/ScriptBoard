package managedfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrConflict = errors.New("文件已被外部修改")

type Kind string

const (
	Directory  Kind = "directory"
	Regular    Kind = "regular"
	Restricted Kind = "restricted"
)

type Entry struct {
	Name       string
	Kind       Kind
	Size       int64
	ModifiedAt time.Time
}

type Trashed struct {
	OriginalPath string
	StoredName   string
	Size         int64
	Directory    bool
}

type TextDocument struct {
	Content string
	Digest  string
}

type Script struct {
	Path   string
	Digest string
	Info   os.FileInfo
}

func (s *Store) Upload(relative, name string, source io.Reader, maxBytes int64, replace bool, storedName string) (*Trashed, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	parent, err := s.resolveDirectory(relative)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(parent, name)
	existing, existingErr := os.Lstat(target)
	if existingErr == nil && !replace {
		return nil, fmt.Errorf("同名条目已存在")
	} else if existingErr == nil && (!existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0) {
		return nil, fmt.Errorf("只能替换普通文件")
	} else if !os.IsNotExist(existingErr) {
		return nil, fmt.Errorf("检查上传目标: %w", existingErr)
	}

	temporary, err := os.CreateTemp(parent, ".scriptboard-upload-*")
	if err != nil {
		return nil, fmt.Errorf("创建上传临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("设置上传文件权限: %w", err)
	}
	written, copyErr := io.Copy(temporary, io.LimitReader(source, maxBytes+1))
	if copyErr == nil && written > maxBytes {
		copyErr = fmt.Errorf("文件超过 %d 字节上限", maxBytes)
	}
	if syncErr := temporary.Sync(); copyErr == nil && syncErr != nil {
		copyErr = syncErr
	}
	if closeErr := temporary.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return nil, fmt.Errorf("写入上传文件: %w", copyErr)
	}
	var trashed *Trashed
	if existingErr == nil {
		old, err := s.MoveToTrash(filepath.ToSlash(filepath.Join(filepath.FromSlash(relative), name)), storedName)
		if err != nil {
			return nil, err
		}
		trashed = &old
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		if trashed != nil {
			_ = s.RestoreFromTrash(trashed.StoredName, trashed.OriginalPath)
		}
		return nil, fmt.Errorf("提交上传文件: %w", err)
	}
	return trashed, nil
}

func (s *Store) OpenRegular(relative string) (*os.File, os.FileInfo, error) {
	target, info, err := s.resolveEntry(relative)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("只能下载普通文件")
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, fmt.Errorf("打开文件: %w", err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("检查文件: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("文件在打开期间发生变化")
	}
	return file, openedInfo, nil
}

func (s *Store) PrepareScript(relative string) (Script, error) {
	file, info, err := s.OpenRegular(relative)
	if err != nil {
		return Script{}, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return Script{}, fmt.Errorf("计算脚本摘要: %w", err)
	}
	return Script{Path: file.Name(), Digest: hex.EncodeToString(hash.Sum(nil)), Info: info}, nil
}

func (s *Store) Info(relative string) (os.FileInfo, error) {
	_, info, err := s.resolveEntry(relative)
	return info, err
}

func (s *Store) ToggleOwnerExecute(relative string) (bool, error) {
	target, info, err := s.resolveEntry(relative)
	if err != nil || !info.Mode().IsRegular() {
		return false, fmt.Errorf("只能修改普通文件的 owner execute 位")
	}
	mode := info.Mode().Perm()
	enabled := mode&0o100 == 0
	if enabled {
		mode |= 0o100
	} else {
		mode &^= 0o100
	}
	if err := os.Chmod(target, mode); err != nil {
		return false, err
	}
	return enabled, nil
}

func (s *Store) MoveToTrash(relative, storedName string) (Trashed, error) {
	if err := validateName(storedName); err != nil {
		return Trashed{}, fmt.Errorf("回收条目标识无效: %w", err)
	}
	target, info, err := s.resolveEntry(relative)
	if err != nil {
		return Trashed{}, err
	}
	trashRoot := filepath.Join(s.root, ".scriptboard-trash")
	if err := os.MkdirAll(trashRoot, 0o755); err != nil {
		return Trashed{}, fmt.Errorf("创建回收站: %w", err)
	}
	storedPath := filepath.Join(trashRoot, storedName)
	if err := os.Rename(target, storedPath); err != nil {
		return Trashed{}, fmt.Errorf("移动到回收站: %w", err)
	}
	return Trashed{
		OriginalPath: filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative))),
		StoredName:   storedName,
		Size:         info.Size(),
		Directory:    info.IsDir(),
	}, nil
}

func (s *Store) RestoreFromTrash(storedName, original string) error {
	if err := validateName(storedName); err != nil {
		return fmt.Errorf("回收条目标识无效: %w", err)
	}
	clean := filepath.Clean(filepath.FromSlash(original))
	name := filepath.Base(clean)
	if err := validateName(name); err != nil {
		return err
	}
	parentRelative := filepath.Dir(clean)
	if parentRelative == "." {
		parentRelative = ""
	}
	parent, err := s.resolveDirectory(filepath.ToSlash(parentRelative))
	if err != nil {
		return fmt.Errorf("恢复目标目录无效: %w", err)
	}
	target := filepath.Join(parent, name)
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("原路径已有同名条目")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查恢复目标: %w", err)
	}
	storedPath := filepath.Join(s.root, ".scriptboard-trash", storedName)
	if _, err := os.Lstat(storedPath); err != nil {
		return fmt.Errorf("回收条目不存在: %w", err)
	}
	if err := os.Rename(storedPath, target); err != nil {
		return fmt.Errorf("恢复回收条目: %w", err)
	}
	return nil
}

func (s *Store) PurgeTrash(storedName string) error {
	if err := validateName(storedName); err != nil {
		return fmt.Errorf("回收条目标识无效: %w", err)
	}
	storedPath := filepath.Join(s.root, ".scriptboard-trash", storedName)
	info, err := os.Lstat(storedPath)
	if err != nil {
		return fmt.Errorf("回收条目不存在: %w", err)
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if err := os.RemoveAll(storedPath); err != nil {
			return fmt.Errorf("清理回收目录: %w", err)
		}
		return nil
	}
	if err := os.Remove(storedPath); err != nil {
		return fmt.Errorf("清理回收条目: %w", err)
	}
	return nil
}

func (s *Store) ReadText(relative string, maxBytes int64) (TextDocument, error) {
	file, info, err := s.OpenRegular(relative)
	if err != nil {
		return TextDocument{}, err
	}
	defer file.Close()
	if info.Size() > maxBytes {
		return TextDocument{}, fmt.Errorf("文本文件超过 %d 字节上限", maxBytes)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return TextDocument{}, fmt.Errorf("读取文本文件: %w", err)
	}
	if int64(len(content)) > maxBytes || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return TextDocument{}, fmt.Errorf("文件不是可编辑的 UTF-8 文本")
	}
	digest := sha256.Sum256(content)
	return TextDocument{Content: string(content), Digest: hex.EncodeToString(digest[:])}, nil
}

func (s *Store) SaveText(relative, expectedDigest, content, storedName string, maxBytes int64) (Trashed, error) {
	if int64(len([]byte(content))) > maxBytes || !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return Trashed{}, fmt.Errorf("内容不是上限内的有效 UTF-8 文本")
	}
	current, err := s.ReadText(relative, maxBytes)
	if err != nil {
		return Trashed{}, err
	}
	if current.Digest != expectedDigest {
		return Trashed{}, ErrConflict
	}
	target, info, err := s.resolveEntry(relative)
	if err != nil {
		return Trashed{}, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".scriptboard-upload-*")
	if err != nil {
		return Trashed{}, fmt.Errorf("创建编辑临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return Trashed{}, fmt.Errorf("保留文件权限: %w", err)
	}
	if _, err := io.WriteString(temporary, content); err != nil {
		_ = temporary.Close()
		return Trashed{}, fmt.Errorf("写入编辑内容: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Trashed{}, fmt.Errorf("同步编辑内容: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Trashed{}, fmt.Errorf("关闭编辑临时文件: %w", err)
	}
	trashed, err := s.MoveToTrash(relative, storedName)
	if err != nil {
		return Trashed{}, err
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		_ = s.RestoreFromTrash(trashed.StoredName, trashed.OriginalPath)
		return Trashed{}, fmt.Errorf("提交编辑内容: %w", err)
	}
	return trashed, nil
}

func (s *Store) RollbackTextSave(relative, storedName string) error {
	target, _, err := s.resolveEntry(relative)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		return err
	}
	return s.RestoreFromTrash(storedName, relative)
}

type Store struct {
	root string
}

func Open(root string) *Store {
	return &Store{root: root}
}

func (s *Store) List(relative string) ([]Entry, error) {
	directory, err := s.resolveDirectory(relative)
	if err != nil {
		return nil, err
	}
	handle, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("读取目录: %w", err)
	}
	defer handle.Close()
	directoryEntries, err := handle.ReadDir(100_001)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("读取目录: %w", err)
	}
	if len(directoryEntries) > 100_000 {
		return nil, fmt.Errorf("单目录条目超过 100,000 个，请拆分目录")
	}

	entries := make([]Entry, 0, len(directoryEntries))
	for _, directoryEntry := range directoryEntries {
		if reservedName(directoryEntry.Name()) {
			continue
		}
		info, err := os.Lstat(filepath.Join(directory, directoryEntry.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取条目 %q: %w", directoryEntry.Name(), err)
		}
		kind := Restricted
		switch {
		case info.Mode().IsRegular():
			kind = Regular
		case info.IsDir() && sameFilesystem(s.root, filepath.Join(directory, directoryEntry.Name())):
			kind = Directory
		}
		entries = append(entries, Entry{
			Name:       directoryEntry.Name(),
			Kind:       kind,
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
		})
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Kind == Directory && entries[right].Kind != Directory {
			return true
		}
		if entries[left].Kind != Directory && entries[right].Kind == Directory {
			return false
		}
		return naturalLess(entries[left].Name, entries[right].Name)
	})
	return entries, nil
}

func naturalLess(left, right string) bool {
	a, b := strings.ToLower(left), strings.ToLower(right)
	for len(a) > 0 && len(b) > 0 {
		if a[0] >= '0' && a[0] <= '9' && b[0] >= '0' && b[0] <= '9' {
			ai, bi := 0, 0
			for ai < len(a) && a[ai] >= '0' && a[ai] <= '9' {
				ai++
			}
			for bi < len(b) && b[bi] >= '0' && b[bi] <= '9' {
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
				return len(an) < len(bn)
			}
			if an != bn {
				return an < bn
			}
			a, b = a[ai:], b[bi:]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func (s *Store) CreateDirectory(relative, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	parent, err := s.resolveDirectory(relative)
	if err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(parent, name), 0o755); err != nil {
		return fmt.Errorf("创建目录: %w", err)
	}
	return nil
}

func (s *Store) Move(source, destination string) error {
	sourceClean := filepath.Clean(filepath.FromSlash(source))
	destinationClean := filepath.Clean(filepath.FromSlash(destination))
	if sourceClean == "." || destinationClean == "." || filepath.IsAbs(destinationClean) || destinationClean == ".." || strings.HasPrefix(destinationClean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("源或目标路径无效")
	}
	sourcePath, sourceInfo, err := s.resolveEntry(filepath.ToSlash(sourceClean))
	if err != nil {
		return err
	}
	if sourceInfo.IsDir() && (destinationClean == sourceClean || strings.HasPrefix(destinationClean, sourceClean+string(filepath.Separator))) {
		return fmt.Errorf("目录不能移动到自身内部")
	}
	name := filepath.Base(destinationClean)
	if err := validateName(name); err != nil {
		return err
	}
	parentRelative := filepath.Dir(destinationClean)
	if parentRelative == "." {
		parentRelative = ""
	}
	parent, err := s.resolveDirectory(filepath.ToSlash(parentRelative))
	if err != nil {
		return err
	}
	targetPath := filepath.Join(parent, name)
	if _, err := os.Lstat(targetPath); err == nil {
		return fmt.Errorf("目标路径已有同名条目")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查移动目标: %w", err)
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		return fmt.Errorf("移动条目: %w", err)
	}
	return nil
}

func (s *Store) resolveDirectory(relative string) (string, error) {
	if relative == "" || relative == "." {
		return s.root, nil
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("路径必须相对于受管根目录")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径不能离开受管根目录")
	}
	current := s.root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" || part == "." || reservedName(part) {
			return "", fmt.Errorf("路径包含保留条目")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("路径不是可进入的普通目录")
		}
		if !sameFilesystem(s.root, current) {
			return "", fmt.Errorf("受限挂载不可进入")
		}
	}
	return current, nil
}

func (s *Store) resolveEntry(relative string) (string, os.FileInfo, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", nil, fmt.Errorf("文件路径无效")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("路径不能离开受管根目录")
	}
	current := s.root
	parts := strings.Split(clean, string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." || reservedName(part) {
			return "", nil, fmt.Errorf("路径包含保留条目")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 && index < len(parts)-1 {
			return "", nil, fmt.Errorf("受限链接不可读取")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", nil, fmt.Errorf("路径祖先不是普通目录")
		}
		if info.IsDir() && !sameFilesystem(s.root, current) {
			return "", nil, fmt.Errorf("受限挂载不可读取")
		}
		if index == len(parts)-1 {
			return current, info, nil
		}
	}
	return "", nil, fmt.Errorf("文件路径无效")
}

func reservedName(name string) bool {
	return name == ".git" || name == ".scriptboard-trash" || strings.HasPrefix(name, ".scriptboard-upload-")
}

func validateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return fmt.Errorf("名称包含非法路径字符")
	}
	if reservedName(name) {
		return fmt.Errorf("名称属于 ScriptBoard 保留条目")
	}
	return nil
}
