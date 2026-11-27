package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is written by install.sh through the `init-config` subcommand.
type Config struct {
	Name         string `json:"name"`
	Port         int    `json:"port"`
	PasswordHash string `json:"password_hash"` // hex(salt):hex(sha256(salt+password))
	DataDir      string `json:"data_dir"`      // database, sessions and internal temp files
	StorageRoot  string `json:"storage_root"`  // netdisk root: every uploaded file lives here
	SessionDays  int    `json:"session_days"`
	Theme        string `json:"theme,omitempty"` // site-wide skin, also used by share pages
}

// validThemes is the server-side source of truth for skin IDs. The browser
// offers the same list, but never trusts client input when persisting it.
var validThemes = map[string]bool{
	"ocean": true, "emerald": true, "amber": true, "rose": true, "violet": true,
	"square": true, "pill": true, "outline": true, "flat": true, "raised": true,
	"tech": true, "mono": true, "paper": true, "slate": true, "lime": true,
	"cloud": true, "cream": true, "mint": true,
	"frost": true, "aurora": true, "smoke": true, "glass-square": true,
}

const defaultTheme = "ocean"

func normalizeTheme(id string) string {
	id = strings.TrimSpace(id)
	if !validThemes[id] {
		return ""
	}
	return id
}

// FilesDir returns the directory exposed as the netdisk root.
// Older configs without storage_root keep using <data_dir>/files.
func (c *Config) FilesDir() string {
	if strings.TrimSpace(c.StorageRoot) != "" {
		return c.StorageRoot
	}
	return filepath.Join(c.DataDir, "files")
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", c.Port)
	}
	if strings.TrimSpace(c.PasswordHash) == "" {
		return nil, fmt.Errorf("password hash is empty")
	}
	if c.DataDir == "" {
		c.DataDir = "/var/lib/lywp"
	}
	if c.SessionDays < 0 {
		c.SessionDays = 0 // 0 = permanent login
	}
	if normalizeTheme(c.Theme) == "" {
		c.Theme = defaultTheme
	}
	c.DataDir, err = filepath.Abs(c.DataDir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.StorageRoot) == "" {
		c.StorageRoot = filepath.Join(c.DataDir, "files")
	}
	c.StorageRoot, err = filepath.Abs(c.StorageRoot)
	if err != nil {
		return nil, err
	}
	// Fail early with a clear message instead of silently creating files in
	// the wrong place if the chosen root was removed or is not writable.
	if err := os.MkdirAll(c.StorageRoot, 0755); err != nil {
		return nil, fmt.Errorf("create netdisk root %s: %w", c.StorageRoot, err)
	}
	return &c, nil
}

// hashPassword returns "salt:hash" so no plaintext password is ever persisted.
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(append([]byte{}, salt...), []byte(password)...))
	return fmt.Sprintf("%s:%s", hex.EncodeToString(salt), hex.EncodeToString(sum[:])), nil
}

func verifyPassword(stored, password string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	sum := sha256.Sum256(append(append([]byte{}, salt...), []byte(password)...))
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

// initConfig is called by install.sh with secrets supplied through the
// environment, keeping them out of the process list.
func initConfigFromEnv() error {
	path := os.Getenv("LYWP_CONFIG")
	if path == "" {
		return fmt.Errorf("LYWP_CONFIG is required")
	}
	password := os.Getenv("LYWP_PASSWORD")
	if password == "" {
		return fmt.Errorf("LYWP_PASSWORD is required")
	}
	var port int
	if _, err := fmt.Sscanf(os.Getenv("LYWP_PORT"), "%d", &port); err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("invalid LYWP_PORT")
	}
	name := sanitizeSiteName(os.Getenv("LYWP_NAME"))
	if name == "" {
		name = "老杨网盘"
	}
	dir := os.Getenv("LYWP_DATA_DIR")
	if dir == "" {
		dir = "/var/lib/lywp"
	}
	storage := os.Getenv("LYWP_STORAGE_ROOT")
	if storage == "" {
		storage = filepath.Join(dir, "files")
	}
	if !filepath.IsAbs(storage) {
		return fmt.Errorf("LYWP_STORAGE_ROOT must be an absolute path")
	}
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	theme := normalizeTheme(os.Getenv("LYWP_THEME"))
	if theme == "" {
		theme = defaultTheme
	}
	c := Config{Name: name, Port: port, PasswordHash: h, DataDir: dir, StorageRoot: storage, SessionDays: 0, Theme: theme}
	if err := saveConfig(path, &c); err != nil {
		return err
	}
	fmt.Printf("config written to %s\n", path)
	return nil
}

// saveConfig writes the config atomically so a skin change can never leave a
// half-written config behind for the next server restart.
func saveConfig(path string, c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lywp-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
