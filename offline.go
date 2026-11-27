package main

// Offline downloads: HTTP(S)/FTP links are streamed by the server itself;
// magnet links and .torrent files use aria2c when it is installed on the host.
// Every task is persisted in SQLite and can be listed/cancelled from the UI.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const torrentScheme = "torrentfile://"

func (s *Server) handleOfflineAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL     string `json:"url"`
		Path    string `json:"path"`
		Extract bool   `json:"extract"`
	}
	if err := readJSON(r, &in); err != nil {
		badRequest(w, err)
		return
	}
	link := strings.TrimSpace(in.URL)
	if link == "" {
		fail(w, 400, "请输入下载链接", nil)
		return
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" {
		fail(w, 400, "链接格式不正确", nil)
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "magnet":
	default:
		fail(w, 400, "仅支持 http/https/ftp/magnet 链接", nil)
		return
	}
	rel, err := cleanRel(in.Path)
	if err != nil {
		badRequest(w, err)
		return
	}
	dir, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fail(w, 500, "无法创建下载目录", nil)
		return
	}
	if !s.requireFolderAccess(w, r, rel) {
		return
	}
	task, err := s.store.createOfflineTask(link, rel)
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	s.mu.Lock()
	s.offlineExtract[task.ID] = in.Extract
	s.mu.Unlock()
	go s.runOfflineTask(task.ID, dir, link, in.Extract)
	ok(w, map[string]any{"id": task.ID, "status": task.Status})
}

// handleOfflineTorrent accepts a .torrent file selected in the browser and
// turns it into a normal offline task backed by aria2.
func (s *Server) handleOfflineTorrent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Data    string `json:"data"`
		Extract bool   `json:"extract"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil {
		badRequest(w, err)
		return
	}
	name, err := safeFilename(in.Name)
	if err != nil || !strings.HasSuffix(strings.ToLower(name), ".torrent") {
		fail(w, 400, "请选择 .torrent 种子文件", nil)
		return
	}
	data, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil {
		fail(w, 400, "种子文件内容无效", nil)
		return
	}
	if len(data) == 0 || len(data) > 16<<20 {
		fail(w, 400, "种子文件大小必须在 16 MB 以内", nil)
		return
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != 'd' || !bytes.Contains(data, []byte("4:info")) {
		fail(w, 400, "不是有效的 .torrent 文件", nil)
		return
	}
	rel, err := cleanRel(in.Path)
	if err != nil {
		badRequest(w, err)
		return
	}
	dir, err := joinPath(s.files, rel)
	if err != nil {
		badRequest(w, err)
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		fail(w, 500, "无法创建下载目录", nil)
		return
	}
	if !s.requireFolderAccess(w, r, rel) {
		return
	}
	token, err := newToken()
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	savePath := filepath.Join(s.tmp, "torrent_"+token[:12]+".torrent")
	if err := os.WriteFile(savePath, data, 0600); err != nil {
		fail(w, 500, "保存种子文件失败", nil)
		return
	}
	link := torrentScheme + base64.RawURLEncoding.EncodeToString([]byte(savePath))
	task, err := s.store.createOfflineTask(link, rel)
	if err != nil {
		_ = os.Remove(savePath)
		fail(w, 500, err.Error(), nil)
		return
	}
	s.store.updateOfflineTask(task.ID, "", "", name, 0)
	s.mu.Lock()
	s.offlineExtract[task.ID] = in.Extract
	s.mu.Unlock()
	go s.runOfflineTask(task.ID, dir, link, in.Extract)
	ok(w, map[string]any{"id": task.ID, "status": task.Status})
}

// torrentFilePath resolves an internal torrentfile:// link and confines it
// to the server's temporary directory.
func (s *Server) torrentFilePath(link string) (string, bool) {
	if !strings.HasPrefix(link, torrentScheme) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, torrentScheme))
	if err != nil {
		return "", false
	}
	tmpAbs, err := filepath.Abs(s.tmp)
	if err != nil {
		return "", false
	}
	pathAbs, err := filepath.Abs(string(raw))
	if err != nil || (pathAbs != tmpAbs && !strings.HasPrefix(pathAbs, tmpAbs+string(os.PathSeparator))) {
		return "", false
	}
	return pathAbs, true
}

// cleanOldTorrentFiles removes torrent payloads left behind by finished tasks.
func cleanOldTorrentFiles(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "torrent_") || !strings.HasSuffix(name, ".torrent") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func (s *Server) handleOfflineList(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.listOfflineTasks()
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"items": tasks})
}

func (s *Server) handleOfflineCancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &in); err != nil || in.ID == "" {
		badRequest(w, errors.New("missing id"))
		return
	}
	s.mu.Lock()
	cancel := s.offlineCancel[in.ID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.store.updateOfflineTask(in.ID, "cancelled", "已取消", "", -1)
	ok(w, map[string]any{"id": in.ID})
}

func (s *Server) handleOfflineDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &in); err != nil || in.ID == "" {
		badRequest(w, errors.New("missing id"))
		return
	}
	s.mu.Lock()
	cancel := s.offlineCancel[in.ID]
	delete(s.offlineExtract, in.ID)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if old, err := s.store.getOfflineTask(in.ID); err == nil {
		if path, ok := s.torrentFilePath(old.URL); ok {
			_ = os.Remove(path)
		}
	}
	if err := s.store.deleteOfflineTask(in.ID); err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"id": in.ID})
}

func (s *Server) handleOfflineRetry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &in); err != nil || in.ID == "" {
		badRequest(w, errors.New("missing id"))
		return
	}
	old, err := s.store.getOfflineTask(in.ID)
	if err != nil {
		fail(w, 404, "任务不存在", nil)
		return
	}
	s.mu.Lock()
	extract := s.offlineExtract[in.ID]
	s.mu.Unlock()
	dir, err := joinPath(s.files, old.Path)
	if err != nil {
		badRequest(w, err)
		return
	}
	_ = os.MkdirAll(dir, 0755)
	task, err := s.store.createOfflineTask(old.URL, old.Path)
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	s.mu.Lock()
	s.offlineExtract[task.ID] = extract
	s.mu.Unlock()
	go s.runOfflineTask(task.ID, dir, old.URL, extract)
	ok(w, map[string]any{"id": task.ID})
}

func (s *Server) runOfflineTask(id, dir, link string, extract bool) {
	s.offlineSem <- struct{}{}
	defer func() { <-s.offlineSem }()

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.offlineCancel[id] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.offlineCancel, id)
		delete(s.offlineExtract, id)
		s.mu.Unlock()
	}()

	if t, err := s.store.getOfflineTask(id); err == nil && t.Status == "cancelled" {
		return
	}
	s.store.updateOfflineTask(id, "running", "准备下载", "", 0)

	// A magnet with no peers can stay in the metadata phase forever. Detect
	// ten minutes without any progress and surface a retryable failure.
	done := make(chan struct{})
	timedOut := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				t, err := s.store.getOfflineTask(id)
				if err != nil || t.Status != "running" {
					return
				}
				if time.Now().Unix()-t.UpdatedAt > 600 {
					close(timedOut)
					s.store.updateOfflineTask(id, "failed", "下载超时（可能没有可用种子），请重试", "", -1)
					cancel()
					return
				}
			}
		}
	}()

	aria2Link := ""
	if path, ok := s.torrentFilePath(link); ok {
		if _, statErr := os.Stat(path); statErr != nil {
			s.store.updateOfflineTask(id, "failed", "种子文件已丢失，请重新上传", "", -1)
			return
		}
		aria2Link = path
	}
	u, _ := url.Parse(link)
	scheme := strings.ToLower(u.Scheme)
	isTorrentURL := (scheme == "http" || scheme == "https") && strings.HasSuffix(strings.ToLower(u.Path), ".torrent")
	var fileName, message string
	var err error
	if aria2Link != "" || scheme == "magnet" || isTorrentURL || strings.HasSuffix(strings.ToLower(link), ".torrent") || scheme == "ftp" {
		target := link
		if aria2Link != "" {
			target = aria2Link
		}
		fileName, message, err = s.downloadWithAria2(ctx, id, dir, target)
	} else {
		fileName, message, err = s.downloadHTTP(ctx, id, dir, link)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			select {
			case <-timedOut:
				// The watchdog already wrote the timeout state.
			default:
				s.store.updateOfflineTask(id, "cancelled", "已取消", fileName, -1)
			}
			return
		}
		s.store.updateOfflineTask(id, "failed", err.Error(), fileName, -1)
		return
	}
	if extract && fileName != "" {
		full := filepath.Join(dir, fileName)
		if isArchive(full) {
			if e := extractArchive(full, filepath.Join(dir, archiveBase(fileName))); e != nil {
				message += "；解压失败：" + e.Error()
			} else {
				message += "；已解压"
			}
		}
	}
	s.store.updateOfflineTask(id, "done", message, fileName, 100)
}

// downloadHTTP streams a link straight to disk without buffering the file in
// memory. Progress is reported against Content-Length when the server provides it.
func (s *Server) downloadHTTP(ctx context.Context, id, dir, link string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "lywp-offline-download/1.0")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("服务器返回 HTTP %d", resp.StatusCode)
	}

	name := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, e := mime.ParseMediaType(cd); e == nil {
			name = params["filename"]
		}
	}
	if name == "" {
		name = cleanURLPath(resp.Request.URL.Path)
	}
	if name == "" || name == "/" {
		name = "download"
	}
	name, err = safeFilename(filepath.Base(name))
	if err != nil {
		name = "download"
	}

	tmp, err := os.CreateTemp(dir, ".streaming_*")
	if err != nil {
		return "", "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	buf := make([]byte, 1<<20)
	var written int64
	last := time.Now()
	total := resp.ContentLength
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, e := tmp.Write(buf[:n]); e != nil {
				tmp.Close()
				return "", "", e
			}
			written += int64(n)
			if time.Since(last) > 400*time.Millisecond {
				pct := 0
				if total > 0 {
					pct = int(written * 100 / total)
				}
				s.store.updateOfflineTask(id, "", fmt.Sprintf("已下载 %s", humanSize(written)), name, pct)
				last = time.Now()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			tmp.Close()
			return "", "", readErr
		}
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	target := filepath.Join(dir, name)
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return "", "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return "", "", err
	}
	return name, fmt.Sprintf("下载完成 · %s", humanSize(written)), nil
}

var aria2Percent = regexp.MustCompile(`\((\d+)%\)`)

// aria2Progress turns aria2's console summary lines into task progress.
type aria2Progress struct {
	mu           sync.Mutex
	buf          []byte
	seenProgress bool
	report       func(int, string)
}

func (p *aria2Progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, b...)
	// aria2 uses carriage returns to redraw its progress line, so splitting
	// only on \n kept every percentage hidden until the process exited.
	for {
		i := bytes.IndexAny(p.buf, "\r\n")
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(p.buf[:i]))
		p.buf = p.buf[i+1:]
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "metadata download complete") {
			p.report(0, "种子信息已获取，开始下载...")
			continue
		}
		if strings.Contains(lower, "download complete") || strings.Contains(line, "下载完成") {
			p.report(100, "下载完成")
			continue
		}
		if strings.Contains(lower, "metadata") && !p.seenProgress {
			p.report(0, "正在获取种子信息...")
			continue
		}
		if m := aria2Percent.FindStringSubmatch(line); len(m) == 2 {
			if pct, err := strconv.Atoi(m[1]); err == nil {
				p.seenProgress = true
				p.report(pct, fmt.Sprintf("下载中 %d%%", pct))
			}
		}
	}
	return len(b), nil
}

// ensureAria2 makes magnet/torrent/FTP downloads work out of the box. On
// Debian the installer already adds aria2; if it is missing on a running
// system, try to install it once automatically (the service runs as root).
func (s *Server) ensureAria2(ctx context.Context, id string) (string, error) {
	if bin, err := exec.LookPath("aria2c"); err == nil {
		return bin, nil
	} else if errors.Is(err, exec.ErrDot) && bin != "" {
		if abs, e := filepath.Abs(bin); e == nil {
			return abs, nil
		}
	}
	for _, p := range []string{"/usr/bin/aria2c", "/usr/local/bin/aria2c"} {
		if st, err := os.Stat(p); err == nil && st.Mode()&0111 != 0 {
			return p, nil
		}
	}
	const hint = "未找到 aria2；请在服务器执行 sudo apt-get install -y aria2 后重试"
	if !runningAsRoot() {
		return "", errors.New(hint)
	}
	apt, err := exec.LookPath("apt-get")
	if err != nil {
		return "", errors.New(hint)
	}
	installCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	env := append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	s.store.updateOfflineTask(id, "running", "正在自动安装 aria2，请稍候...", "", 0)
	cmd := exec.CommandContext(installCtx, apt, "install", "-y", "aria2")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.store.updateOfflineTask(id, "running", "正在更新软件源并重试安装 aria2...", "", 0)
		update := exec.CommandContext(installCtx, apt, "update", "-qq")
		update.Env = env
		_ = update.Run()
		cmd = exec.CommandContext(installCtx, apt, "install", "-y", "aria2")
		cmd.Env = env
		out2, err2 := cmd.CombinedOutput()
		if err2 != nil {
			if errors.Is(installCtx.Err(), context.Canceled) {
				return "", context.Canceled
			}
			msg := strings.TrimSpace(string(out2))
			if msg == "" {
				msg = strings.TrimSpace(string(out))
			}
			if msg == "" {
				msg = err2.Error()
			}
			return "", fmt.Errorf("自动安装 aria2 失败：%s；请手动执行 sudo apt-get install -y aria2", msg)
		}
	}
	if bin, err := exec.LookPath("aria2c"); err == nil {
		return bin, nil
	}
	return "", errors.New(hint)
}

// downloadWithAria2 delegates multi-file/magnet transfers to aria2c.
func (s *Server) downloadWithAria2(ctx context.Context, id, dir, link string) (string, string, error) {
	bin, err := s.ensureAria2(ctx, id)
	if err != nil {
		return "", "", err
	}
	start := time.Now().Add(-2 * time.Second)
	args := []string{
		"--dir=" + dir,
		"--continue=true",
		"--max-connection-per-server=8",
		"--split=8",
		"--min-split-size=4M",
		"--summary-interval=1",
		"--console-log-level=info",
		"--allow-overwrite=true",
		"--bt-tracker=udp://tracker.opentrackr.org:1337/announce,udp://open.stealth.si:80/announce,udp://tracker.torrent.eu.org:451/announce",
		link,
	}
	progress := &aria2Progress{report: func(pct int, msg string) {
		s.store.updateOfflineTask(id, "", msg, "", pct)
	}}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = progress
	cmd.Stderr = progress
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return "", "", context.Canceled
		}
		return "", "", fmt.Errorf("aria2c: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	names := []string{}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") || strings.HasPrefix(n, "aria2") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(start) {
			continue
		}
		names = append(names, n)
	}
	name := ""
	if len(names) == 1 {
		name = names[0]
	} else if len(names) > 1 {
		name = fmt.Sprintf("%d 个文件/文件夹", len(names))
	}
	return name, "下载完成", nil
}

func isArchive(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".zip", ".tar", ".tar.gz", ".tgz"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func archiveBase(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".zip", ".tar", ".tgz"} {
		if strings.HasSuffix(lower, ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

func extractArchive(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	lower := strings.ToLower(src)
	if strings.HasSuffix(lower, ".zip") {
		zr, err := zip.OpenReader(src)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if err := extractZipEntry(f, dst); err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(target, dst+string(os.PathSeparator)) && target != dst {
			continue
		}
		if hdr.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0755)
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, dst string) error {
	name := filepath.Clean(f.Name)
	if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
		return nil
	}
	target := filepath.Join(dst, name)
	if !strings.HasPrefix(target, dst+string(os.PathSeparator)) && target != dst {
		return nil
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0755)
	}
	_ = os.MkdirAll(filepath.Dir(target), 0755)
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
