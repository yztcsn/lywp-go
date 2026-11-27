package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// precompressedExts are already compressed, so re-deflating them only burns
// CPU. Storing them in ZIP is both faster and more faithful to the source.
var precompressedExts = map[string]bool{
	"7z": true, "avi": true, "bz2": true, "flac": true, "gif": true,
	"gz": true, "jpeg": true, "jpg": true, "mkv": true, "mp3": true,
	"mp4": true, "ogg": true, "png": true, "rar": true, "tar": true,
	"webm": true, "webp": true, "xz": true, "zip": true, "zst": true,
}

type FileItem struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	IsDir             bool   `json:"is_dir"`
	Size              int64  `json:"size"`
	Extension         string `json:"extension"`
	Modified          string `json:"modified"`
	ModifiedTimestamp int64  `json:"modified_timestamp"`
	FileCount         int    `json:"file_count,omitempty"`
	DownloadCount     int    `json:"download_count,omitempty"`
	Encrypted         bool   `json:"encrypted,omitempty"`
}

// cleanRel normalizes a user-supplied relative path and rejects traversal.
func cleanRel(raw string) (string, error) {
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "\x00") {
		return "", errors.New("invalid path")
	}
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", nil
	}
	parts := strings.Split(raw, "/")
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p {
		case "", ".":
			continue
		case "..":
			return "", errors.New("invalid path")
		}
		if strings.HasPrefix(p, ".streaming_") {
			return "", errors.New("invalid path")
		}
		clean = append(clean, p)
	}
	return strings.Join(clean, "/"), nil
}

// joinPath resolves a relative path inside root, defensively checking containment.
func joinPath(root, rel string) (string, error) {
	r, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(r))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	// Use filepath.Rel instead of a raw string prefix: roots like "D:\" on
	// Windows (or "/" on Linux) make root+separator a doubled separator that
	// rejects every child path as invalid.
	relative, err := filepath.Rel(absRoot, absFull)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", errors.New("invalid path")
	}
	return absFull, nil
}

func parentRel(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

func isInside(child, parent string) bool {
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func listDir(root, rel string) ([]FileItem, error) {
	full, err := joinPath(root, rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(full)
	if err != nil || !st.IsDir() {
		return nil, errors.New("directory not found")
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	dirs, files := []FileItem{}, []FileItem{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".streaming_") || strings.HasPrefix(name, ".") {
			continue
		}
		relPath := name
		if rel != "" {
			relPath = rel + "/" + name
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mt := info.ModTime().Unix()
		mod := info.ModTime().Format("2006-01-02 15:04:05")
		if info.IsDir() {
			size, count, latest := dirStatsDetailed(filepath.Join(full, name))
			if !latest.IsZero() {
				mt = latest.Unix()
				mod = latest.Format("2006-01-02 15:04:05")
			}
			_, encErr := os.Stat(filepath.Join(full, name, encryFileName))
			dirs = append(dirs, FileItem{Name: name, Path: relPath, IsDir: true, Size: size,
				Modified: mod, ModifiedTimestamp: mt, FileCount: count, Encrypted: encErr == nil})
		} else {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
			files = append(files, FileItem{Name: name, Path: relPath, Size: info.Size(), Extension: ext,
				Modified: mod, ModifiedTimestamp: mt})
		}
	}
	byTime := func(a, b FileItem) bool { return a.ModifiedTimestamp > b.ModifiedTimestamp }
	sort.Slice(dirs, func(i, j int) bool { return byTime(dirs[i], dirs[j]) })
	sort.Slice(files, func(i, j int) bool { return byTime(files[i], files[j]) })
	return append(dirs, files...), nil
}

type dirStatsEntry struct {
	at     time.Time
	mod    int64
	size   int64
	count  int
	latest int64
}

var dirStatsCache sync.Map

// dirStatsDetailed walks a folder to compute size, file count and the newest
// modification time anywhere in its tree. A folder is treated as modified
// whenever any descendant file or subfolder was added, changed, removed or
// moved, so the displayed time reflects real activity instead of only the
// folder entry's own timestamp.
func dirStatsDetailed(dir string) (int64, int, time.Time) {
	mod := int64(0)
	latest := time.Time{}
	if st, err := os.Stat(dir); err == nil {
		mod = st.ModTime().UnixNano()
		latest = st.ModTime()
	}
	key := dir + "|" + strconv.FormatInt(mod, 10)
	if v, ok := dirStatsCache.Load(key); ok {
		e := v.(dirStatsEntry)
		if time.Since(e.at) < 2*time.Second {
			if e.latest > 0 {
				latest = time.Unix(0, e.latest)
			}
			return e.size, e.count, latest
		}
	}
	var size int64
	var count int
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info, e := d.Info(); e == nil {
			if t := info.ModTime(); t.After(latest) {
				latest = t
			}
			if !d.IsDir() {
				size += info.Size()
				count++
			}
		}
		return nil
	})
	dirStatsCache.Store(key, dirStatsEntry{
		at: time.Now(), mod: mod, size: size, count: count, latest: latest.UnixNano(),
	})
	return size, count, latest
}

// dirStats keeps the original size/count contract for existing callers.
func dirStats(dir string) (int64, int) {
	size, count, _ := dirStatsDetailed(dir)
	return size, count
}

// dirModified returns the effective modification time of a folder.
func dirModified(dir string, fallback time.Time) time.Time {
	_, _, latest := dirStatsDetailed(dir)
	if latest.IsZero() {
		return fallback
	}
	return latest
}

func walkFiles(root, rel string, cb func(name, rel string, info os.FileInfo) error) error {
	full, err := joinPath(root, rel)
	if err != nil {
		return err
	}
	return filepath.Walk(full, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() != "." && strings.HasPrefix(info.Name(), ".") && p != full {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".streaming_") {
			return nil
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return cb(info.Name(), filepath.ToSlash(r), info)
	})
}

func searchFiles(root, query string, limit int) ([]FileItem, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	out := []FileItem{}
	if q == "" {
		return out, nil
	}
	err := walkFiles(root, "", func(name, rel string, info os.FileInfo) error {
		if len(out) >= limit {
			return filepath.SkipAll
		}
		if !strings.Contains(strings.ToLower(name), q) {
			return nil
		}
		out = append(out, FileItem{
			Name: name, Path: rel, Size: info.Size(),
			Extension:         strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")),
			Modified:          info.ModTime().Format("2006-01-02 15:04:05"),
			ModifiedTimestamp: info.ModTime().Unix(),
		})
		return nil
	})
	return out, err
}

// writeZip streams selected paths into a zip without creating any temp file.
func writeZip(w io.Writer, root string, paths []string, prefix string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	seen := map[string]bool{}
	for _, rel := range paths {
		r, err := cleanRel(rel)
		if err != nil || r == "" || seen[r] {
			continue
		}
		seen[r] = true
		full, err := joinPath(root, r)
		if err != nil {
			continue
		}
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		if st.IsDir() {
			err = filepath.Walk(full, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if info.IsDir() {
					if p != full && strings.HasPrefix(info.Name(), ".") {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasPrefix(info.Name(), ".") {
					return nil
				}
				sub, err := filepath.Rel(root, p)
				if err != nil {
					return nil
				}
				return addZipFile(zw, root, filepath.ToSlash(sub), prefix)
			})
		} else {
			err = addZipFile(zw, root, r, prefix)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func addZipFile(zw *zip.Writer, root, rel, prefix string) error {
	full, err := joinPath(root, rel)
	if err != nil {
		return nil
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return nil
	}
	name := rel
	if prefix != "" {
		name = prefix + "/" + rel
	}
	name = strings.TrimPrefix(name, "/")
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = name
	hdr.Method = zip.Deflate
	if precompressedExts[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))] {
		hdr.Method = zip.Store
	}
	out, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.CopyBuffer(out, f, make([]byte, 1<<20))
	return err
}

func safeFilename(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.Contains(name, "\x00") {
		return "", errors.New("invalid filename")
	}
	return name, nil
}

func movePath(root, from, to string) error {
	src, err := joinPath(root, from)
	if err != nil {
		return err
	}
	dstRel, err := cleanRel(to)
	if err != nil || dstRel == "" {
		return errors.New("invalid destination")
	}
	dst, err := joinPath(root, dstRel)
	if err != nil {
		return err
	}
	if isInside(dstRel, from) {
		return errors.New("cannot move a directory into itself")
	}
	if _, err := os.Stat(dst); err == nil {
		return errors.New("目标位置已存在同名文件")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// moveIntoDir moves a file or directory into targetDir while keeping its name.
// The legacy api.php "move"/"batch_move" actions pass a destination *directory*,
// not a full destination path, so the basename must be appended here.
// Returns the resulting relative path. Moving into the current directory is a
// harmless no-op and reports success.
func moveIntoDir(root, from, targetDir string) (string, error) {
	srcRel, err := cleanRel(from)
	if err != nil || srcRel == "" {
		return "", errors.New("invalid source")
	}
	dir, err := cleanRel(targetDir)
	if err != nil {
		return "", err
	}
	name := path.Base(srcRel)
	dst := name
	if dir != "" {
		dst = dir + "/" + name
	}
	if dst == srcRel {
		return dst, nil
	}
	if err := movePath(root, srcRel, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func timestamp(t time.Time) string { return t.Format("2006-01-02 15:04:05") }

func mustJSONError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
