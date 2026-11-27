package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store keeps share/session/offline metadata in SQLite. Download counters use
// short transactions: a single UPDATE against the primary key is fast even on
// WAL, and doing it synchronously makes download limits exact under concurrency.
type Store struct {
	db *sql.DB
}

type Share struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Password      string `json:"password"`
	ExpireAt      int64  `json:"expire_at"`
	IsDir         bool   `json:"is_dir"`
	IsDirect      bool   `json:"is_direct"`
	DownloadCount int    `json:"download_count"`
	MaxDownloads  int    `json:"max_downloads"`
	CreatedAt     int64  `json:"created_at"`
}

type OfflineTask struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Message   string `json:"message,omitempty"`
	FileName  string `json:"file_name,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

var errDownloadLimit = errors.New("下载次数已用完")

func openStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	// WAL lets readers proceed while a write is in flight; NORMAL keeps the
	// write-ahead log small without risking corruption on a clean shutdown.
	dsn := filepath.Join(dataDir, "lywp.db") + "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS shares (
            id TEXT PRIMARY KEY,
            path TEXT NOT NULL,
            password TEXT,
            expire_at INTEGER NOT NULL DEFAULT 0,
            is_dir INTEGER NOT NULL DEFAULT 0,
            is_direct INTEGER NOT NULL DEFAULT 0,
            download_count INTEGER NOT NULL DEFAULT 0,
            max_downloads INTEGER NOT NULL DEFAULT 0,
            created_at INTEGER NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_shares_path ON shares(path)`,
		`CREATE TABLE IF NOT EXISTS file_downloads (
            share_id TEXT NOT NULL,
            path TEXT NOT NULL,
            count INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY (share_id, path)
        )`,
		`CREATE TABLE IF NOT EXISTS sessions (
            token TEXT PRIMARY KEY,
            created_at INTEGER NOT NULL,
            expire_at INTEGER NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS offline_tasks (
            id TEXT PRIMARY KEY,
            url TEXT NOT NULL,
            path TEXT NOT NULL DEFAULT '',
            status TEXT NOT NULL DEFAULT 'pending',
            progress INTEGER NOT NULL DEFAULT 0,
            message TEXT NOT NULL DEFAULT '',
            file_name TEXT NOT NULL DEFAULT '',
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL
        )`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	// Schema upgrades for databases created by older builds. SQLite has no
	// ADD COLUMN IF NOT EXISTS, so inspect table_info first.
	cols, err := s.columnNames("shares")
	if err != nil {
		return err
	}
	if !cols["max_downloads"] {
		if _, err := s.db.Exec(`ALTER TABLE shares ADD COLUMN max_downloads INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) columnNames(table string) (map[string]bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) createSession(days int) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	expire := int64(0) // 0 means the session never expires
	if days > 0 {
		expire = now + int64(days)*86400
	}
	_, err = s.db.Exec(`INSERT INTO sessions(token,created_at,expire_at) VALUES(?,?,?)`,
		token, now, expire)
	return token, err
}

func (s *Store) validSession(token string) bool {
	if token == "" {
		return false
	}
	var expire int64
	err := s.db.QueryRow(`SELECT expire_at FROM sessions WHERE token=?`, token).Scan(&expire)
	return err == nil && (expire == 0 || expire > time.Now().Unix())
}

func (s *Store) deleteSession(token string) {
	s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
}

func (s *Store) cleanupSessions() {
	s.db.Exec(`DELETE FROM sessions WHERE expire_at>0 AND expire_at<?`, time.Now().Unix())
}

const shareColumns = `id,path,password,expire_at,is_dir,is_direct,download_count,max_downloads,created_at`

func scanShare(sc interface{ Scan(...any) error }) (*Share, error) {
	var sh Share
	var pwd sql.NullString
	var exp, isDir, isDirect, created int64
	err := sc.Scan(&sh.ID, &sh.Path, &pwd, &exp, &isDir, &isDirect, &sh.DownloadCount, &sh.MaxDownloads, &created)
	if err != nil {
		return nil, err
	}
	if pwd.Valid {
		sh.Password = pwd.String
	}
	sh.ExpireAt = exp
	sh.IsDir = isDir != 0
	sh.IsDirect = isDirect != 0
	sh.CreatedAt = created
	return &sh, nil
}

func (s *Store) getShare(id string) (*Share, error) {
	return scanShare(s.db.QueryRow(
		`SELECT `+shareColumns+` FROM shares WHERE id=?`, id))
}

func (s *Store) createShare(path, password string, expireDays, maxDownloads int, isDir, direct bool) (*Share, error) {
	id, err := newToken()
	if err != nil {
		return nil, err
	}
	id = id[:12]
	var exp int64
	if expireDays > 0 {
		exp = time.Now().Unix() + int64(expireDays)*86400
	}
	var pwd any
	if password != "" {
		pwd = password
	}
	directInt, dirInt := 0, 0
	if direct {
		directInt = 1
	}
	if isDir {
		dirInt = 1
	}
	if maxDownloads < 0 {
		maxDownloads = 0
	}
	_, err = s.db.Exec(
		`INSERT INTO shares(id,path,password,expire_at,is_dir,is_direct,download_count,max_downloads,created_at)
         VALUES(?,?,?,?,?,?,0,?,?)`, id, path, pwd, exp, dirInt, directInt, maxDownloads, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	return s.getShare(id)
}

func (s *Store) findDirectShare(path string) (*Share, error) {
	return scanShare(s.db.QueryRow(
		`SELECT `+shareColumns+`
         FROM shares WHERE path=? AND is_direct=1 AND password IS NULL AND expire_at=0 AND max_downloads=0 LIMIT 1`, path))
}

func (s *Store) listShares() ([]Share, error) {
	rows, err := s.db.Query(`SELECT ` + shareColumns + ` FROM shares ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sh)
	}
	return out, rows.Err()
}

func (s *Store) deleteShare(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM shares WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM file_downloads WHERE share_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// countShare increments the share counter atomically and refuses to exceed
// max_downloads (0 means unlimited). Returns true when the download is allowed.
func (s *Store) countShare(id string) bool {
	if id == "" {
		return false
	}
	res, err := s.db.Exec(
		`UPDATE shares SET download_count=download_count+1
         WHERE id=? AND (max_downloads=0 OR download_count<max_downloads)`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *Store) countFiles(id string, paths []string) {
	if id == "" || len(paths) == 0 {
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(
		`INSERT INTO file_downloads(share_id,path,count) VALUES(?,?,1)
         ON CONFLICT(share_id,path) DO UPDATE SET count=count+1`)
	if err != nil {
		return
	}
	defer stmt.Close()
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		_, _ = stmt.Exec(id, p)
	}
	_ = tx.Commit()
}

func (s *Store) fileDownloadCounts(id string) map[string]int {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT path,count FROM file_downloads WHERE share_id=?`, id)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var n int
		if rows.Scan(&p, &n) == nil {
			out[p] = n
		}
	}
	return out
}

// ---------- offline tasks ----------

func (s *Store) createOfflineTask(url, rel string) (*OfflineTask, error) {
	id, err := newToken()
	if err != nil {
		return nil, err
	}
	id = id[:12]
	now := time.Now().Unix()
	_, err = s.db.Exec(
		`INSERT INTO offline_tasks(id,url,path,status,progress,message,file_name,created_at,updated_at)
         VALUES(?,?,?,'pending',0,'','',?,?)`, id, url, rel, now, now)
	if err != nil {
		return nil, err
	}
	return s.getOfflineTask(id)
}

func scanOfflineTask(sc interface{ Scan(...any) error }) (*OfflineTask, error) {
	var t OfflineTask
	var msg, name sql.NullString
	err := sc.Scan(&t.ID, &t.URL, &t.Path, &t.Status, &t.Progress, &msg, &name, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if msg.Valid {
		t.Message = msg.String
	}
	if name.Valid {
		t.FileName = name.String
	}
	return &t, nil
}

func (s *Store) getOfflineTask(id string) (*OfflineTask, error) {
	return scanOfflineTask(s.db.QueryRow(
		`SELECT id,url,path,status,progress,message,file_name,created_at,updated_at
         FROM offline_tasks WHERE id=?`, id))
}

func (s *Store) listOfflineTasks() ([]OfflineTask, error) {
	rows, err := s.db.Query(
		`SELECT id,url,path,status,progress,message,file_name,created_at,updated_at
         FROM offline_tasks ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OfflineTask{}
	for rows.Next() {
		t, err := scanOfflineTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) updateOfflineTask(id, status, message, fileName string, progress int) {
	if s == nil || id == "" {
		return
	}
	if status != "" {
		s.db.Exec(`UPDATE offline_tasks SET status=?,progress=?,message=?,file_name=?,updated_at=? WHERE id=?`,
			status, progress, message, fileName, time.Now().Unix(), id)
		return
	}
	s.db.Exec(`UPDATE offline_tasks SET progress=?,message=?,file_name=?,updated_at=? WHERE id=?`,
		progress, message, fileName, time.Now().Unix(), id)
}

func (s *Store) deleteOfflineTask(id string) error {
	_, err := s.db.Exec(`DELETE FROM offline_tasks WHERE id=?`, id)
	return err
}

// markInterruptedOfflineTasks clears tasks left in a non-terminal state by a
// previous server process, so the UI never shows "downloading" forever.
func (s *Store) markInterruptedOfflineTasks() {
	if s == nil || s.db == nil {
		return
	}
	_, _ = s.db.Exec(
		`UPDATE offline_tasks SET status='failed', message='服务重启，任务已中断，请点击重试', progress=-1, updated_at=?
         WHERE status IN ('pending','running')`, time.Now().Unix())
}

func (s *Store) close() {
	if s != nil && s.db != nil {
		s.db.Close()
	}
}

// shareExpired reports whether a share can still be used.
func shareExpired(sh *Share) bool {
	return sh.ExpireAt > 0 && sh.ExpireAt < time.Now().Unix()
}

// shareLimitReached reports whether the configured download cap was consumed.
func shareLimitReached(sh *Share) bool {
	return sh.MaxDownloads > 0 && sh.DownloadCount >= sh.MaxDownloads
}

func sharePasswordOK(sh *Share, provided string) bool {
	return sh.Password == "" || sh.Password == provided
}

var errNotFound = errors.New("not found")

func wrapErr(op string, err error) error {
	return fmt.Errorf("%s: %w", op, err)
}

// cleanURLPath is used by the offline downloader to build safe display names.
func cleanURLPath(s string) string {
	s = s[strings.LastIndex(s, "/")+1:]
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
