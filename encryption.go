package main

// Folder encryption: a hidden .encry file inside a folder stores a salted
// password hash. Every path inside an encrypted folder requires an unlock in
// the current session before the netdisk API will serve it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const encryFileName = ".encry"

// findEncryptedFolder walks from the netdisk root down to rel and returns the
// first relative folder path that contains a .encry file.
func (s *Server) findEncryptedFolder(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")
	if len(parts) > 0 && !strings.Contains(rel, "/") {
		// A bare name may be a file or a folder; both are handled the same
		// way: the parent is checked below and the item itself is checked
		// only if it is a folder.
	}
	prefix := ""
	for i, part := range parts {
		// The last segment is a file unless the path points to a folder.
		// Checking it as well is harmless: files never contain .encry.
		_ = i
		if prefix != "" {
			prefix += "/"
		}
		prefix += part
		full, err := joinPath(s.files, prefix)
		if err != nil {
			continue
		}
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(full, encryFileName)); err == nil {
			return prefix
		}
	}
	return ""
}

func (s *Server) folderUnlocked(r *http.Request, encRel string) bool {
	token := s.sessionToken(r)
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.unlocked[token]
	if m == nil {
		return false
	}
	exp, ok := m[encRel]
	if !ok {
		return false
	}
	if exp.Before(time.Now()) {
		delete(m, encRel)
		return false
	}
	return true
}

func (s *Server) folderLocked(r *http.Request, rel string) bool {
	enc := s.findEncryptedFolder(rel)
	return enc != "" && !s.folderUnlocked(r, enc)
}

func (s *Server) unlockFolder(r *http.Request, encRel, password string) bool {
	full, err := joinPath(s.files, encRel)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(full, encryFileName))
	if err != nil {
		return false
	}
	if !verifyPassword(strings.TrimSpace(string(b)), password) {
		return false
	}
	token := s.sessionToken(r)
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unlocked[token] == nil {
		s.unlocked[token] = map[string]time.Time{}
	}
	s.unlocked[token][encRel] = time.Now().Add(24 * time.Hour)
	return true
}

func (s *Server) requireFolderAccess(w http.ResponseWriter, r *http.Request, rel string) bool {
	enc := s.findEncryptedFolder(rel)
	if enc == "" || s.folderUnlocked(r, enc) {
		return true
	}
	fail(w, http.StatusLocked, "文件夹已加密，请输入密码", map[string]any{
		"need_folder_password": true,
		"encrypted":            true,
		"unlocked":             false,
		"folder":               enc,
		"path":                 rel,
	})
	return false
}

// folderGuard checks every path mentioned in an API request. It runs before
// the regular handlers so uploads, downloads, rename, move and shares all
// honour the same encryption gate.
func (s *Server) folderGuard(w http.ResponseWriter, r *http.Request, skip map[string]bool) bool {
	action := r.URL.Query().Get("action")
	if skip[action] {
		return true
	}
	paths := []string{}
	add := func(v string) {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				paths = append(paths, p)
			}
		}
	}
	for _, key := range []string{"path", "paths", "source", "target", "from", "to"} {
		if v := r.URL.Query().Get(key); v != "" {
			add(v)
		}
	}
	ct := r.Header.Get("Content-Type")
	if r.Body != nil && strings.Contains(ct, "application/json") {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err == nil && len(b) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(b))
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				for _, key := range []string{"path", "source", "target", "from", "to"} {
					if v, ok := m[key].(string); ok {
						add(v)
					}
				}
				if arr, ok := m["paths"].([]any); ok {
					for _, x := range arr {
						if v, ok := x.(string); ok {
							add(v)
						}
					}
				}
			}
		}
	} else if r.Body != nil && strings.Contains(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err == nil {
			if raw := r.Form.Get("_json"); raw != "" {
				var m struct {
					Paths []string `json:"paths"`
				}
				if json.Unmarshal([]byte(raw), &m) == nil {
					for _, p := range m.Paths {
						add(p)
					}
				}
			}
		}
	}
	for _, p := range paths {
		if !s.requireFolderAccess(w, r, p) {
			return false
		}
	}
	return true
}

func (s *Server) handleFolderCheck(w http.ResponseWriter, r *http.Request) {
	rel, err := cleanRel(r.URL.Query().Get("path"))
	if err != nil {
		badRequest(w, err)
		return
	}
	enc := s.findEncryptedFolder(rel)
	ok(w, map[string]any{
		"path": rel, "encrypted": enc != "", "folder": enc,
		"unlocked": enc == "" || s.folderUnlocked(r, enc),
	})
}

func (s *Server) handleFolderUnlock(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path     string `json:"path"`
		Password string `json:"password"`
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
	enc := s.findEncryptedFolder(rel)
	if enc == "" {
		ok(w, map[string]any{"unlocked": true, "encrypted": false})
		return
	}
	if !s.unlockFolder(r, enc, in.Password) {
		fail(w, http.StatusForbidden, "密码错误", map[string]any{"need_folder_password": true, "path": rel})
		return
	}
	ok(w, map[string]any{"unlocked": true, "encrypted": true, "folder": enc})
}

func (s *Server) handleFolderEncrypt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path     string `json:"path"`
		Password string `json:"password"`
		Remove   bool   `json:"remove"`
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
	if err != nil || !st.IsDir() {
		fail(w, 400, "只能加密文件夹", nil)
		return
	}
	marker := filepath.Join(full, encryFileName)
	if in.Remove {
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			fail(w, 500, err.Error(), nil)
			return
		}
		s.mu.Lock()
		for _, m := range s.unlocked {
			delete(m, rel)
		}
		s.mu.Unlock()
		ok(w, map[string]any{"encrypted": false})
		return
	}
	if strings.TrimSpace(in.Password) == "" {
		fail(w, 400, "密码不能为空", nil)
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	if err := os.WriteFile(marker, []byte(hash), 0600); err != nil {
		fail(w, 500, err.Error(), nil)
		return
	}
	ok(w, map[string]any{"encrypted": true, "folder": rel})
}
