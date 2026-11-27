package main

// Compatibility layer: the original PHP front-end talks to api.php/auth.php
// with an `action` query parameter. These handlers keep that contract so the
// full original UI can run unchanged on the Go server.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var publicCompatActions = map[string]bool{
	"download_share":           true,
	"stream_share":             true,
	"verify_share":             true,
	"list_share":               true,
	"count_download":           true,
	"share_batch_download_zip": true,
	"fix_permissions":          true,
}

func (s *Server) handleAuthCompat(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("action") {
	case "check":
		writeJSON(w, 200, map[string]any{
			"success": true, "authenticated": s.authenticated(r),
		})
	case "login":
		s.handleAuth(w, r, "login")
	case "logout":
		s.handleAuth(w, r, "logout")
	default:
		fail(w, 404, "未知操作", nil)
	}
}

func (s *Server) handleCompatAPI(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if !publicCompatActions[action] && !s.requireAuth(w, r) {
		return
	}
	// Public share endpoints are gated by the share itself; everything else
	// must respect folder encryption.
	if publicCompatActions[action] {
		switch action {
		case "download_share":
			s.shareDownloadCompat(w, r)
		case "stream_share":
			s.shareStreamCompat(w, r)
		case "verify_share":
			s.shareVerifyCompat(w, r)
		case "list_share":
			s.shareListFilesCompat(w, r)
		case "count_download":
			id := r.URL.Query().Get("id")
			if s.store.countShare(id) {
				if p := r.URL.Query().Get("path"); p != "" {
					s.store.countFiles(id, []string{p})
				}
			}
			writeJSON(w, 200, map[string]any{"success": true, "allowed": true})
		case "share_batch_download_zip":
			s.shareBatchZipCompat(w, r)
		case "fix_permissions":
			ok(w, map[string]any{"ok": true})
		default:
			fail(w, 404, "未知操作", nil)
		}
		return
	}
	skip := map[string]bool{
		"folder_check": true, "folder_unlock": true,
		"folder_encrypt": true, "direct_link": true,
		"offline_list": true, "offline_cancel": true,
		"offline_delete": true, "offline_retry": true,
		"offline_torrent": true,
	}
	if !s.folderGuard(w, r, skip) {
		return
	}
	switch action {
	case "list":
		s.handleList(w, r)
	case "upload_stream":
		s.handleUpload(w, r)
	case "download":
		s.handleDownload(w, r)
	case "stream":
		s.handleStream(w, r)
	case "download_folder":
		s.handleDownloadFolderCompat(w, r)
	case "rename":
		s.handleRename(w, r)
	case "delete":
		s.handleDeleteCompat(w, r)
	case "move":
		s.handleMoveCompat(w, r)
	case "create_folder":
		s.handleMkdir(w, r)
	case "batch_delete":
		s.handleBatchDeleteCompat(w, r)
	case "batch_move":
		s.handleBatchMoveCompat(w, r)
	case "batch_download":
		s.handleBatchDownloadCompat(w, r)
	case "batch_download_zip":
		s.handleBatchZipCompat(w, r)
	case "share":
		s.handleShareCreate(w, r)
	case "direct_link":
		s.handleShareDirect(w, r)
	case "list_shares":
		s.handleListSharesCompat(w, r)
	case "delete_share":
		s.handleShareDelete(w, r)
	case "verify_share":
		s.shareVerifyCompat(w, r)
	case "list_share":
		s.shareListFilesCompat(w, r)
	case "download_share":
		s.shareDownloadCompat(w, r)
	case "share_batch_download_zip":
		s.shareBatchZipCompat(w, r)
	case "count_download":
		id := r.URL.Query().Get("id")
		allowed := s.store.countShare(id)
		if !allowed {
			fail(w, 410, "下载次数已用完", nil)
			return
		}
		if p := r.URL.Query().Get("path"); p != "" {
			s.store.countFiles(id, []string{p})
		}
		writeJSON(w, 200, map[string]any{"success": true, "allowed": true})
	case "search":
		s.handleSearchCompat(w, r)
	case "storage_info":
		s.handleStorageInfoCompat(w, r)
	case "theme":
		s.handleTheme(w, r)
	case "folder_check":
		s.handleFolderCheck(w, r)
	case "folder_unlock":
		s.handleFolderUnlock(w, r)
	case "folder_encrypt":
		s.handleFolderEncrypt(w, r)
	case "offline_add":
		s.handleOfflineAdd(w, r)
	case "offline_list":
		s.handleOfflineList(w, r)
	case "offline_cancel":
		s.handleOfflineCancel(w, r)
	case "offline_delete":
		s.handleOfflineDelete(w, r)
	case "offline_retry":
		s.handleOfflineRetry(w, r)
	case "offline_torrent":
		s.handleOfflineTorrent(w, r)
	case "fix_permissions":
		ok(w, map[string]any{"ok": true})
	default:
		fail(w, 404, "未知操作", nil)
	}
}

// readPaths reads a path list from either the query string or a JSON body.
func readPaths(r *http.Request) ([]string, error) {
	var paths []string
	if q := r.URL.Query().Get("paths"); q != "" {
		for _, p := range strings.Split(q, ",") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
		return paths, nil
	}
	// Hidden form POST is used by the browser-native download path so the
	// response streams straight to disk instead of being buffered in a Blob.
	if err := r.ParseForm(); err == nil {
		if raw := r.Form.Get("_json"); raw != "" {
			var in struct {
				Paths []string `json:"paths"`
			}
			if json.Unmarshal([]byte(raw), &in) == nil {
				for _, p := range in.Paths {
					if p = strings.TrimSpace(p); p != "" {
						paths = append(paths, p)
					}
				}
				return paths, nil
			}
		}
	}
	if r.Body != nil {
		var in struct {
			Paths []string `json:"paths"`
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &in)
			for _, p := range in.Paths {
				if p = strings.TrimSpace(p); p != "" {
					paths = append(paths, p)
				}
			}
		}
	}
	return paths, nil
}

func expandPaths(root string, paths []string) []map[string]any {
	out := []map[string]any{}
	for _, rel := range paths {
		r, err := cleanRel(rel)
		if err != nil || r == "" {
			continue
		}
		full, err := joinPath(root, r)
		if err != nil {
			continue
		}
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			out = append(out, map[string]any{"name": filepath.Base(full), "path": r, "size": st.Size()})
			continue
		}
		folder := filepath.Base(full)
		_ = walkFiles(root, r, func(name, path string, info os.FileInfo) error {
			sub, err := filepath.Rel(full, filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				return nil
			}
			out = append(out, map[string]any{
				"name": folder + "/" + filepath.ToSlash(sub),
				"path": path,
				"size": info.Size(),
			})
			return nil
		})
	}
	return out
}

// serveMediaFile streams a file inline with full HTTP Range support. The
// browser can start playback as soon as the first bytes arrive and seek to
// any position via Range requests, without downloading the whole file.
func (s *Server) serveMediaFile(w http.ResponseWriter, r *http.Request, full string) {
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		fail(w, 404, "文件不存在", nil)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		fail(w, 500, "无法打开文件", nil)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", contentType(full))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// ServeContent honours Range/If-Range and emits 206 + Content-Range,
	// which is what enables progressive playback and seeking.
	http.ServeContent(w, r, filepath.Base(full), st.ModTime(), f)
}

// handleStream serves a logged-in user's media inline with Range support.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		badRequest(w, errors.New("invalid path"))
		return
	}
	full, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	s.serveMediaFile(w, r, full)
}

// shareStreamCompat serves a shared media file inline with Range support so
// visitors can play and seek without a full download. It mirrors the public
// download_share gate (share id / expiry / limit) but never forces a download
// and counts one logical download per playback session.
func (s *Server) shareStreamCompat(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shareNoPwd(r)
	if err != nil {
		fail(w, 403, err.Error(), nil)
		return
	}
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		rel = sh.Path
	}
	if !isInside(rel, sh.Path) {
		fail(w, 403, "路径不合法", nil)
		return
	}
	full, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	if st, err := os.Stat(full); err != nil || st.IsDir() {
		fail(w, 404, "文件不存在", nil)
		return
	}
	// Only the initial request counts; follow-up Range requests from the
	// player must not consume extra downloads.
	if countRequestOnce(r) {
		if !s.store.countShare(sh.ID) {
			fail(w, 410, "分享下载次数已用完", nil)
			return
		}
		s.store.countFiles(sh.ID, []string{rel})
	}
	s.serveMediaFile(w, r, full)
}

func (s *Server) handleDownloadFolderCompat(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		badRequest(w, errors.New("invalid path"))
		return
	}
	full, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	if _, err := os.Stat(full); err != nil {
		fail(w, 404, "文件或目录不存在", nil)
		return
	}
	if r.URL.Query().Get("mode") == "queue" {
		files := expandPaths(s.files, []string{rel})
		ok(w, files)
		return
	}
	name := filepath.Base(full)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(r, name+".zip"))
	_ = writeZip(w, s.files, []string{rel}, name)
}

func (s *Server) handleDeleteCompat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	rel, err := cleanRel(in.Path)
	if err != nil || rel == "" {
		badRequest(w, errors.New("invalid path"))
		return
	}
	full, err := joinPath(s.files, rel)
	if err != nil || full == s.files {
		badRequest(w, errors.New("invalid path"))
		return
	}
	_ = os.RemoveAll(full)
	ok(w, map[string]any{"success": true})
}

func (s *Server) handleBatchDeleteCompat(w http.ResponseWriter, r *http.Request) {
	paths, err := readPaths(r)
	if err != nil {
		badRequest(w, err)
		return
	}
	success, failed := 0, 0
	for _, rel := range paths {
		full, err := joinPath(s.files, rel)
		if err != nil || full == s.files {
			failed++
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			failed++
		} else {
			success++
		}
	}
	ok(w, map[string]any{"success": success, "failed": failed})
}

func (s *Server) handleMoveCompat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	newPath, err := moveIntoDir(s.files, in.Source, in.Target)
	if err != nil {
		fail(w, 400, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"path": newPath, "new_path": newPath})
}

func (s *Server) handleBatchMoveCompat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths  []string `json:"paths"`
		Target string   `json:"target"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	success, failed := 0, 0
	for _, src := range in.Paths {
		if _, err := moveIntoDir(s.files, src, in.Target); err != nil {
			failed++
		} else {
			success++
		}
	}
	ok(w, map[string]any{"success": success, "failed": failed})
}

func (s *Server) handleBatchDownloadCompat(w http.ResponseWriter, r *http.Request) {
	paths, err := readPaths(r)
	if err != nil {
		badRequest(w, err)
		return
	}
	ok(w, expandPaths(s.files, paths))
}

func (s *Server) handleBatchZipCompat(w http.ResponseWriter, r *http.Request) {
	paths, _ := readPaths(r)
	if len(paths) == 0 {
		fail(w, 400, "未选择文件", nil)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(r, "批量下载.zip"))
	_ = writeZip(w, s.files, paths, "")
}

// countRequestOnce ignores follow-up Range requests so a single download is
// counted once even when the browser resumes or previews media in chunks.
func countRequestOnce(r *http.Request) bool {
	rg := strings.TrimSpace(r.Header.Get("Range"))
	return rg == "" || strings.HasPrefix(rg, "bytes=0-")
}

func formatShareTime(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func (s *Server) handleListSharesCompat(w http.ResponseWriter, r *http.Request) {
	shares, err := s.store.listShares()
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	page := 1
	_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
	if page < 1 {
		page = 1
	}
	const pageSize = 50
	sort.Slice(shares, func(i, j int) bool { return shares[i].CreatedAt > shares[j].CreatedAt })
	start := (page - 1) * pageSize
	if start > len(shares) {
		start = len(shares)
	}
	end := start + pageSize
	if end > len(shares) {
		end = len(shares)
	}
	origin := "http://" + r.Host
	if r.TLS != nil {
		origin = "https://" + r.Host
	}
	items := []map[string]any{}
	for _, sh := range shares[start:end] {
		direct := sh.IsDirect || (sh.Password == "" && sh.ExpireAt == 0 && !sh.IsDir && sh.MaxDownloads == 0)
		url := origin + "/share.html?id=" + sh.ID
		if direct && !shareLimitReached(&sh) {
			url = origin + "/api.php?action=download_share&id=" + sh.ID
		}
		directInt, dirInt := 0, 0
		if direct {
			directInt = 1
		}
		if sh.IsDir {
			dirInt = 1
		}
		items = append(items, map[string]any{
			"id": sh.ID, "path": sh.Path, "password": sh.Password,
			"expire_at": formatShareTime(sh.ExpireAt), "is_dir": dirInt,
			"download_count": sh.DownloadCount, "max_downloads": sh.MaxDownloads,
			"created_at": formatShareTime(sh.CreatedAt),
			"url":        url, "is_direct": directInt,
			"expired": shareExpired(&sh) || shareLimitReached(&sh),
		})
	}
	writeJSON(w, 200, map[string]any{
		"success": true, "data": items, "total": len(shares), "page": page,
		"page_size": pageSize, "has_more": end < len(shares),
	})
}

func (s *Server) handleSearchCompat(w http.ResponseWriter, r *http.Request) {
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	if keyword == "" {
		fail(w, 400, "搜索关键词不能为空", nil)
		return
	}
	base, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil {
		badRequest(w, err)
		return
	}
	out := searchAll(s.files, base, keyword, 100)
	writeJSON(w, 200, map[string]any{
		"success": true, "data": map[string]any{"items": out, "total": len(out)},
	})
}

// searchAll mirrors the original PHP search: both files and folders are matched.
func searchAll(root, base, query string, limit int) []FileItem {
	out := []FileItem{}
	start := root
	if base != "" {
		if p, err := joinPath(root, base); err == nil {
			start = p
		}
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return out
	}
	_ = filepath.WalkDir(start, func(p string, d os.DirEntry, err error) error {
		if err != nil || len(out) >= limit {
			return nil
		}
		if p == start {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if !strings.Contains(strings.ToLower(d.Name()), q) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		item := FileItem{
			Name: d.Name(), Path: filepath.ToSlash(rel), IsDir: d.IsDir(),
			Size: info.Size(), Modified: timestamp(info.ModTime()),
			ModifiedTimestamp: info.ModTime().Unix(),
		}
		if d.IsDir() {
			size, _, latest := dirStatsDetailed(p)
			item.Size = size
			if !latest.IsZero() {
				item.Modified = timestamp(latest)
				item.ModifiedTimestamp = latest.Unix()
			}
		} else {
			item.Extension = strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
		}
		out = append(out, item)
		return nil
	})
	return out
}

func (s *Server) handleStorageInfoCompat(w http.ResponseWriter, r *http.Request) {
	info := storageInfo(s.files)
	size, files, dirs := dirStatsWithCounts(s.files)
	ok(w, map[string]any{
		"total_space": info.Total, "used_space": info.Used, "free_space": info.Free,
		"upload_size": size, "file_count": files, "dir_count": dirs,
	})
}

func dirStatsWithCounts(dir string) (int64, int, int) {
	var size int64
	var files, dirs int
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs++
			return nil
		}
		if info, e := d.Info(); e == nil {
			size += info.Size()
		}
		files++
		return nil
	})
	return size, files, dirs
}

// ---------- share compatibility (no password after the gate is passed) ----------

func (s *Server) shareNoPwd(r *http.Request) (*Share, error) {
	return s.shareNoPwdWithID(r, r.URL.Query().Get("id"))
}

func (s *Server) shareNoPwdWithID(r *http.Request, id string) (*Share, error) {
	sh, err := s.store.getShare(id)
	if err != nil {
		return nil, errors.New("分享不存在或已失效")
	}
	if shareExpired(sh) {
		return nil, errors.New("分享已过期")
	}
	if shareLimitReached(sh) {
		return nil, errors.New("分享下载次数已用完")
	}
	full, err := joinPath(s.files, sh.Path)
	if err != nil {
		return nil, errors.New("文件已被删除")
	}
	if _, err := os.Stat(full); err != nil {
		return nil, errors.New("文件已被删除")
	}
	return sh, nil
}

func (s *Server) shareVerifyCompat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	sh, err := s.store.getShare(in.ID)
	if err != nil {
		fail(w, 404, "分享不存在或已失效", nil)
		return
	}
	if shareExpired(sh) {
		fail(w, 410, "分享已过期", nil)
		return
	}
	if shareLimitReached(sh) {
		fail(w, 410, "分享下载次数已用完", nil)
		return
	}
	if !sharePasswordOK(sh, in.Password) {
		fail(w, 403, "密码错误", map[string]any{"need_password": true})
		return
	}
	full, err := joinPath(s.files, sh.Path)
	if err != nil {
		fail(w, 410, "文件已被删除", nil)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		fail(w, 410, "文件已被删除", nil)
		return
	}
	info := map[string]any{
		"need_password": sh.Password != "", "path": sh.Path, "is_dir": st.IsDir(),
		"name": filepath.Base(sh.Path), "expire_at": formatShareTime(sh.ExpireAt),
		"created_at": formatShareTime(sh.CreatedAt), "created": timestamp(st.ModTime()),
		"download_count": sh.DownloadCount, "max_downloads": sh.MaxDownloads,
		"size": st.Size(), "modified": timestamp(st.ModTime()), "type": "",
	}
	if st.IsDir() {
		size, count, latest := dirStatsDetailed(full)
		info["size"] = size
		info["file_count"] = count
		if !latest.IsZero() {
			info["modified"] = timestamp(latest)
		}
	} else {
		info["type"] = strings.ToLower(strings.TrimPrefix(filepath.Ext(full), "."))
	}
	ok(w, info)
}

func (s *Server) shareListFilesCompat(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shareNoPwd(r)
	if err != nil {
		fail(w, 403, err.Error(), nil)
		return
	}
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil {
		badRequest(w, err)
		return
	}
	if rel == "" {
		rel = sh.Path
	}
	if !isInside(rel, sh.Path) {
		fail(w, 403, "路径不合法", nil)
		return
	}
	items, err := listDir(s.files, rel)
	if err != nil {
		fail(w, 404, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"items": s.withFileCounts(sh.ID, items), "current_path": rel,
		"parent_path": parentRel(rel)})
}

// withFileCounts attaches per-file download counters to a share listing.
// Folder rows show the sum of all files below them.
func (s *Server) withFileCounts(shareID string, items []FileItem) []FileItem {
	counts := s.store.fileDownloadCounts(shareID)
	for i := range items {
		if !items[i].IsDir {
			items[i].DownloadCount = counts[items[i].Path]
			continue
		}
		total := 0
		prefix := items[i].Path + "/"
		for p, n := range counts {
			if strings.HasPrefix(p, prefix) {
				total += n
			}
		}
		items[i].DownloadCount = total
	}
	return items
}

func (s *Server) shareDownloadCompat(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shareNoPwd(r)
	if err != nil {
		fail(w, 403, err.Error(), nil)
		return
	}
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil || rel == "" {
		rel = sh.Path
	}
	if !isInside(rel, sh.Path) {
		fail(w, 403, "路径不合法", nil)
		return
	}
	full, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		fail(w, 404, "文件不存在", nil)
		return
	}
	if st.IsDir() && r.URL.Query().Get("mode") == "queue" {
		ok(w, expandPaths(s.files, []string{rel}))
		return
	}
	// Count on the actual transfer (not on a client-side click) so the limit
	// cannot be bypassed and Range requests do not inflate the counter.
	if !s.store.countShare(sh.ID) {
		fail(w, 410, "分享下载次数已用完", nil)
		return
	}
	if st.IsDir() {
		files := expandPaths(s.files, []string{rel})
		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f["path"].(string))
		}
		s.store.countFiles(sh.ID, paths)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", contentDisposition(r, filepath.Base(full)+".zip"))
		_ = writeZip(w, s.files, []string{rel}, filepath.Base(full))
		return
	}
	if countRequestOnce(r) {
		s.store.countFiles(sh.ID, []string{rel})
	}
	w.Header().Set("Content-Type", contentType(full))
	w.Header().Set("Content-Disposition", contentDisposition(r, filepath.Base(full)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
	http.ServeFile(w, r, full)
}

func (s *Server) shareBatchZipCompat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID    string   `json:"id"`
		Paths []string `json:"paths"`
	}
	// Hidden form POST streams the ZIP to disk instead of buffering it.
	if err := r.ParseForm(); err == nil {
		if raw := r.Form.Get("_json"); raw != "" {
			var formIn struct {
				ID    string   `json:"id"`
				Paths []string `json:"paths"`
			}
			if json.Unmarshal([]byte(raw), &formIn) == nil {
				in.ID, in.Paths = formIn.ID, formIn.Paths
			}
		}
	}
	if in.ID == "" {
		if err := readJSON(r, &in); err != nil {
			badRequest(w, err)
			return
		}
	}
	sh, err := s.shareNoPwdWithID(r, in.ID)
	if err != nil {
		fail(w, 403, err.Error(), nil)
		return
	}
	paths := in.Paths
	if len(paths) == 0 {
		paths = []string{sh.Path}
	}
	clean := []string{}
	for _, p := range paths {
		if isInside(p, sh.Path) {
			clean = append(clean, p)
		}
	}
	if !s.store.countShare(sh.ID) {
		fail(w, 410, "分享下载次数已用完", nil)
		return
	}
	expanded := expandPaths(s.files, clean)
	filePaths := make([]string, 0, len(expanded))
	for _, f := range expanded {
		filePaths = append(filePaths, f["path"].(string))
	}
	s.store.countFiles(sh.ID, filePaths)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(r, "分享下载.zip"))
	_ = writeZip(w, s.files, clean, "")
}
