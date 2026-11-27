package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	cfg        *Config
	store      *Store
	configPath string
	files      string
	tmp        string

	// Session-scoped unlocked folders: token -> absolute folder path -> expiry.
	mu       sync.Mutex
	unlocked map[string]map[string]time.Time

	// Offline download task coordination.
	offlineSem     chan struct{}
	offlineCancel  map[string]context.CancelFunc
	offlineExtract map[string]bool
}

func newServer(cfg *Config, store *Store, configPath string) *Server {
	files := cfg.FilesDir()
	_ = os.MkdirAll(files, 0755)
	tmp := filepath.Join(cfg.DataDir, "tmp")
	_ = os.MkdirAll(tmp, 0755)
	cleanOldTorrentFiles(tmp)
	return &Server{
		cfg: cfg, store: store, configPath: configPath, files: files, tmp: tmp,
		unlocked:       map[string]map[string]time.Time{},
		offlineSem:     make(chan struct{}, 2),
		offlineCancel:  map[string]context.CancelFunc{},
		offlineExtract: map[string]bool{},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

func fail(w http.ResponseWriter, status int, msg string, extra map[string]any) {
	body := map[string]any{"success": false, "message": msg}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

func badRequest(w http.ResponseWriter, err error) {
	msg := "请求参数错误"
	if err != nil {
		msg = err.Error()
	}
	fail(w, http.StatusBadRequest, msg, nil)
}

func (s *Server) sessionToken(r *http.Request) string {
	c, err := r.Cookie("lywp_session")
	if err != nil {
		return ""
	}
	return c.Value
}

func (s *Server) authenticated(r *http.Request) bool {
	return s.store.validSession(s.sessionToken(r))
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.authenticated(r) {
		fail(w, http.StatusUnauthorized, "未登录", nil)
		return false
	}
	return true
}

func setSessionCookie(w http.ResponseWriter, token string, days int) {
	expires := time.Now().AddDate(10, 0, 0)
	if days > 0 {
		expires = time.Now().Add(time.Duration(days) * 24 * time.Hour)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "lywp_session", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: expires,
	})
}

func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(dst)
}

// ---------- routing ----------

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/" || p == "/index.html":
		serveAsset(w, r, "web/index.html")
	case p == "/share.html":
		serveAsset(w, r, "web/share.html")
	case p == "/style.css":
		serveAsset(w, r, "web/style.css")
	case p == "/theme.css":
		serveAsset(w, r, "web/theme.css")
	case p == "/app.js":
		serveAsset(w, r, "web/app.js")
	case p == "/share.js":
		serveAsset(w, r, "web/share.js")
	case p == "/logo.svg" || p == "/logo.jpg":
		name := "web/logo.svg"
		if p == "/logo.jpg" {
			name = "web/logo.jpg"
		}
		serveAsset(w, r, name)
	case p == "/qrcode.min.js":
		serveAsset(w, r, "web/qrcode.min.js")
	case p == "/jszip.min.js":
		serveAsset(w, r, "web/jszip.min.js")
	case p == "/favicon.ico":
		serveAsset(w, r, "web/favicon.ico")
	case p == "/api.php":
		s.handleCompatAPI(w, r)
	case p == "/auth.php":
		s.handleAuthCompat(w, r)
	case strings.HasPrefix(p, "/api/auth/"):
		s.handleAuth(w, r, strings.TrimPrefix(p, "/api/auth/"))
	case strings.HasPrefix(p, "/api/manage-share/"):
		s.handleAPI(w, r, "share/"+strings.TrimPrefix(p, "/api/manage-share/"))
	case strings.HasPrefix(p, "/api/share/"):
		s.handleShareAPI(w, r, strings.TrimPrefix(p, "/api/share/"))
	case strings.HasPrefix(p, "/api/"):
		s.handleAPI(w, r, strings.TrimPrefix(p, "/api/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request, action string) {
	switch action {
	case "check":
		ok(w, map[string]any{"authenticated": s.authenticated(r), "name": s.cfg.Name})
	case "login":
		var in struct {
			Password string `json:"password"`
		}
		if err := readJSON(r, &in); err != nil {
			badRequest(w, err)
			return
		}
		if !verifyPassword(s.cfg.PasswordHash, in.Password) {
			fail(w, http.StatusUnauthorized, "密码错误", nil)
			return
		}
		token, err := s.store.createSession(s.cfg.SessionDays)
		if err != nil {
			fail(w, 500, err.Error(), nil)
			return
		}
		setSessionCookie(w, token, s.cfg.SessionDays)
		writeJSON(w, 200, map[string]any{"success": true, "message": "登录成功"})
	case "logout":
		s.store.deleteSession(s.sessionToken(r))
		http.SetCookie(w, &http.Cookie{Name: "lywp_session", Value: "", Path: "/", MaxAge: -1})
		writeJSON(w, 200, map[string]any{"success": true, "message": "已退出登录"})
	default:
		fail(w, 404, "未知操作", nil)
	}
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, action string) {
	if !s.requireAuth(w, r) {
		return
	}
	skip := map[string]bool{
		"folder/check": true, "folder/unlock": true, "folder/encrypt": true,
		"offline/list": true, "offline/cancel": true,
		"offline/delete": true, "offline/retry": true,
	}
	if !s.folderGuard(w, r, skip) {
		return
	}
	switch action {
	case "list":
		s.handleList(w, r)
	case "upload":
		s.handleUpload(w, r)
	case "download":
		s.handleDownload(w, r)
	case "zip":
		s.handleZip(w, r, false)
	case "mkdir":
		s.handleMkdir(w, r)
	case "rename":
		s.handleRename(w, r)
	case "delete":
		s.handleDelete(w, r)
	case "move":
		s.handleMove(w, r)
	case "search":
		s.handleSearch(w, r)
	case "storage":
		ok(w, storageInfo(s.files))
	case "theme":
		s.handleTheme(w, r)
	case "share/create":
		s.handleShareCreate(w, r)
	case "share/direct":
		s.handleShareDirect(w, r)
	case "share/list":
		s.handleShareList(w, r)
	case "share/delete":
		s.handleShareDelete(w, r)
	case "folder/check":
		s.handleFolderCheck(w, r)
	case "folder/unlock":
		s.handleFolderUnlock(w, r)
	case "folder/encrypt":
		s.handleFolderEncrypt(w, r)
	case "offline/add":
		s.handleOfflineAdd(w, r)
	case "offline/list":
		s.handleOfflineList(w, r)
	case "offline/cancel":
		s.handleOfflineCancel(w, r)
	case "offline/delete":
		s.handleOfflineDelete(w, r)
	case "offline/retry":
		s.handleOfflineRetry(w, r)
	default:
		fail(w, 404, "未知操作", nil)
	}
}

// handleTheme persists the chosen skin in config.json so the main page,
// login page, share pages and other visitors all use the same theme after a
// reload or a server restart.
func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "Method not allowed", nil)
		return
	}
	var in struct {
		Theme string `json:"theme"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	theme := normalizeTheme(in.Theme)
	if theme == "" {
		fail(w, http.StatusBadRequest, "未知的皮肤", nil)
		return
	}
	updated := *s.cfg
	updated.Theme = theme
	if err := saveConfig(s.configPath, &updated); err != nil {
		fail(w, http.StatusInternalServerError, "保存皮肤失败: "+err.Error(), nil)
		return
	}
	setCurrentTheme(theme)
	invalidateHTMLCache()
	ok(w, map[string]any{"theme": theme})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil {
		badRequest(w, err)
		return
	}
	items, err := listDir(s.files, rel)
	if err != nil {
		fail(w, 404, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"items": items, "current_path": rel, "parent_path": parentRel(rel)})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "Method not allowed", nil)
		return
	}
	q := r.URL.Query()
	rel, err := cleanRel(q.Get("path"))
	if err != nil {
		badRequest(w, err)
		return
	}
	name, err := safeFilename(q.Get("name"))
	if err != nil {
		badRequest(w, err)
		return
	}
	expect := int64(-1)
	if v := q.Get("size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			badRequest(w, errors.New("invalid size"))
			return
		}
		expect = n
	}
	dir, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fail(w, 500, "无法创建目标目录", nil)
		return
	}
	tmp, err := os.CreateTemp(dir, ".streaming_*")
	if err != nil {
		fail(w, 500, "无法创建临时文件", nil)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	// Read at most one byte beyond the declared size: enough to detect an
	// oversized body without ever buffering a whole file in memory.
	var src io.Reader = r.Body
	if expect >= 0 && expect < math.MaxInt64 {
		src = io.LimitReader(r.Body, expect+1)
	}
	buf := make([]byte, 4<<20)
	written, err := io.CopyBuffer(tmp, src, buf)
	if err != nil {
		tmp.Close()
		fail(w, 500, "读取上传内容失败", nil)
		return
	}
	if expect >= 0 {
		if written > expect {
			tmp.Close()
			fail(w, 500, "上传内容超过声明大小", nil)
			return
		}
		if written != expect {
			tmp.Close()
			fail(w, 500, fmt.Sprintf("上传不完整（收到 %d/%d 字节）", written, expect), nil)
			return
		}
	}
	if err := tmp.Close(); err != nil {
		fail(w, 500, "保存失败", nil)
		return
	}
	target := filepath.Join(dir, name)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		fail(w, 500, "无法覆盖已存在文件", nil)
		return
	}
	if err := os.Rename(tmpName, target); err != nil {
		fail(w, 500, "保存文件失败", nil)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "上传成功"})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
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
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		fail(w, 404, "文件不存在", nil)
		return
	}
	name := filepath.Base(full)
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Content-Disposition", contentDisposition(r, name))
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	http.ServeFile(w, r, full)
}

// mediaMIME maps streaming extensions to browser-friendly MIME types. Go's
// mime package does not know several common media containers, so we cover
// them explicitly to keep inline Range playback working across browsers.
var mediaMIME = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/mp4",
	".webm": "video/webm", ".ogv": "video/ogg", ".ogg": "video/ogg",
	".mkv": "video/x-matroska", ".avi": "video/x-msvideo",
	".flv": "video/x-flv", ".wmv": "video/x-ms-wmv",
	".ts": "video/mp2t", ".m2ts": "video/mp2t", ".mts": "video/mp2t",
	".3gp": "video/3gpp", ".3g2": "video/3gpp2",
	".rmvb": "video/vnd.rn-realvideo", ".rm": "video/vnd.rn-realvideo",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac",
	".wav": "audio/wav", ".flac": "audio/flac", ".oga": "audio/ogg",
	".opus": "audio/ogg", ".wma": "audio/x-ms-wma",
}

func contentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := mediaMIME[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func contentDisposition(r *http.Request, name string) string {
	escaped := strings.ReplaceAll(name, `"`, `%22`)
	if strings.Contains(r.UserAgent(), "Firefox") {
		return fmt.Sprintf(`attachment; filename*=UTF-8''%s`, urlPathEscape(name))
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, escaped, urlPathEscape(name))
}

func urlPathEscape(s string) string {
	replacer := strings.NewReplacer(" ", "%20", "#", "%23", "%", "%25")
	return replacer.Replace(s)
}

func (s *Server) handleZip(w http.ResponseWriter, r *http.Request, public bool) {
	pathsParam := r.URL.Query().Get("paths")
	if pathsParam == "" {
		pathsParam = r.URL.Query().Get("path")
	}
	paths := []string{}
	for _, p := range strings.Split(pathsParam, ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		badRequest(w, errors.New("missing paths"))
		return
	}
	prefix := ""
	if len(paths) > 1 {
		prefix = "lywp_files"
	} else if i := strings.LastIndex(paths[0], "/"); i >= 0 {
		prefix = paths[0][i+1:]
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(r, "download.zip"))
	if err := writeZip(w, s.files, paths, prefix); err != nil {
		fmt.Printf("zip error: %v\n", err)
	}
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	rel, err := cleanRel(in.Path)
	if err != nil {
		badRequest(w, err)
		return
	}
	name, err := safeFilename(in.Name)
	if err != nil {
		badRequest(w, err)
		return
	}
	if rel != "" {
		rel += "/" + name
	} else {
		rel = name
	}
	full, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	if _, err := os.Stat(full); err == nil {
		fail(w, 400, "名称已存在", nil)
		return
	}
	if err := os.MkdirAll(full, 0755); err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"path": rel})
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	newName, err := safeFilename(in.NewName)
	if err != nil {
		badRequest(w, err)
		return
	}
	dst := parentRel(in.Path)
	if dst != "" {
		dst += "/" + newName
	} else {
		dst = newName
	}
	if err := movePath(s.files, in.Path, dst); err != nil {
		fail(w, 400, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"path": dst})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	if err := movePath(s.files, in.From, in.To); err != nil {
		fail(w, 400, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"path": in.To})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paths []string `json:"paths"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	if len(in.Paths) == 0 {
		badRequest(w, errors.New("missing paths"))
		return
	}
	for _, rel := range in.Paths {
		full, err := joinPath(s.files, rel)
		if err != nil || full == s.files {
			continue
		}
		_ = os.RemoveAll(full)
	}
	ok(w, map[string]any{"deleted": len(in.Paths)})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	items, err := searchFiles(s.files, q, 500)
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"items": items, "query": q})
}

// ---------- shares ----------

func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path         string `json:"path"`
		Password     string `json:"password"`
		ExpireDays   int    `json:"expire_days"`
		MaxDownloads int    `json:"max_downloads"`
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
	if err != nil {
		badRequest(w, err)
		return
	}
	st, err := os.Stat(full)
	if err != nil {
		fail(w, 404, "文件不存在", nil)
		return
	}
	sh, err := s.store.createShare(rel, in.Password, in.ExpireDays, in.MaxDownloads, st.IsDir(), false)
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"id": sh.ID, "url": "/share.html?id=" + sh.ID,
		"expire_at": sh.ExpireAt, "max_downloads": sh.MaxDownloads})
}

func (s *Server) handleShareDirect(w http.ResponseWriter, r *http.Request) {
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
	if err != nil {
		badRequest(w, err)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		fail(w, 400, "直链仅支持文件", nil)
		return
	}
	sh, err := s.store.findDirectShare(rel)
	if err != nil {
		sh, err = s.store.createShare(rel, "", 0, 0, false, true)
	}
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"id": sh.ID, "url": "/api/share/download?id=" + sh.ID})
}

func (s *Server) handleShareList(w http.ResponseWriter, r *http.Request) {
	shares, err := s.store.listShares()
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	if shares == nil {
		shares = []Share{}
	}
	ok(w, map[string]any{"items": shares})
}

func (s *Server) handleShareDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	if err := s.store.deleteShare(in.ID); err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"id": in.ID})
}

func (s *Server) handleShareAPI(w http.ResponseWriter, r *http.Request, action string) {
	switch action {
	case "verify":
		s.shareVerify(w, r)
	case "list":
		s.shareListFiles(w, r)
	case "download":
		s.shareDownload(w, r)
	case "zip":
		s.shareZip(w, r)
	case "count":
		if r.Method == http.MethodPost {
			var in struct {
				ID string `json:"id"`
			}
			if err := readJSON(r, &in); err == nil {
				s.store.countShare(in.ID)
			}
			ok(w, nil)
			return
		}
		s.store.countShare(r.URL.Query().Get("id"))
		ok(w, nil)
	default:
		fail(w, 404, "未知操作", nil)
	}
}

func (s *Server) shareFromRequest(r *http.Request) (*Share, error) {
	sh, err := s.store.getShare(r.URL.Query().Get("id"))
	if err != nil {
		return nil, errors.New("分享不存在或已失效")
	}
	if shareExpired(sh) {
		return nil, errors.New("分享已过期")
	}
	if shareLimitReached(sh) {
		return nil, errors.New("分享下载次数已用完")
	}
	if !sharePasswordOK(sh, r.URL.Query().Get("password")) {
		return nil, errors.New("密码错误")
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

func (s *Server) shareVerify(w http.ResponseWriter, r *http.Request) {
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
	data := map[string]any{
		"need_password": sh.Password != "", "path": sh.Path, "is_dir": st.IsDir(),
		"name": filepath.Base(sh.Path), "download_count": sh.DownloadCount,
		"max_downloads": sh.MaxDownloads,
		"created_at":    sh.CreatedAt, "expire_at": sh.ExpireAt,
		"size": st.Size(), "modified": timestamp(st.ModTime()),
	}
	if st.IsDir() {
		size, count, latest := dirStatsDetailed(full)
		data["size"] = size
		data["file_count"] = count
		if !latest.IsZero() {
			data["modified"] = timestamp(latest)
		}
	} else {
		data["extension"] = strings.ToLower(strings.TrimPrefix(filepath.Ext(full), "."))
	}
	ok(w, data)
}

func (s *Server) shareListFiles(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shareFromRequest(r)
	if err != nil {
		fail(w, 403, err.Error(), nil)
		return
	}
	if !sh.IsDir {
		fail(w, 400, "该分享不是文件夹", nil)
		return
	}
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil {
		badRequest(w, err)
		return
	}
	if rel != "" && !isInside(rel, sh.Path) {
		fail(w, 403, "路径不合法", nil)
		return
	}
	if rel == "" {
		rel = sh.Path
	}
	items, err := listDir(s.files, rel)
	if err != nil {
		fail(w, 404, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"items": s.withFileCounts(sh.ID, items), "current_path": rel,
		"parent_path": parentRel(rel), "share_path": sh.Path})
}

func (s *Server) shareDownload(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shareFromRequest(r)
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
	if err != nil || st.IsDir() {
		fail(w, 404, "文件不存在", nil)
		return
	}
	if !s.store.countShare(sh.ID) {
		fail(w, 410, "分享下载次数已用完", nil)
		return
	}
	if countRequestOnce(r) {
		s.store.countFiles(sh.ID, []string{rel})
	}
	w.Header().Set("Content-Type", contentType(full))
	w.Header().Set("Content-Disposition", contentDisposition(r, filepath.Base(full)))
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	http.ServeFile(w, r, full)
}

func (s *Server) shareZip(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shareFromRequest(r)
	if err != nil {
		fail(w, 403, err.Error(), nil)
		return
	}
	pathsParam := r.URL.Query().Get("paths")
	if pathsParam == "" {
		pathsParam = sh.Path
	}
	paths := []string{}
	for _, p := range strings.Split(pathsParam, ",") {
		p = strings.TrimSpace(p)
		if p != "" && isInside(p, sh.Path) {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		badRequest(w, errors.New("missing paths"))
		return
	}
	if !s.store.countShare(sh.ID) {
		fail(w, 410, "分享下载次数已用完", nil)
		return
	}
	expanded := expandPaths(s.files, paths)
	filePaths := make([]string, 0, len(expanded))
	for _, f := range expanded {
		filePaths = append(filePaths, f["path"].(string))
	}
	s.store.countFiles(sh.ID, filePaths)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(r, "download.zip"))
	_ = writeZip(w, s.files, paths, "")
}
