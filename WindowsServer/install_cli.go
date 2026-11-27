//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// installFromFlags is the CLI replacement for the old GUI setup wizard. All
// settings can be supplied on the command line; the password is hashed before
// it reaches config.json.
func installFromFlags(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	name := fs.String("name", "老杨网盘", "netdisk name shown in the UI")
	password := fs.String("password", "", "access password (required)")
	storage := fs.String("storage", "", "netdisk storage root (default: <user home>\\LYWP)")
	port := fs.Int("port", 8080, "HTTP port (1-65535)")
	configPath := fs.String("config", "", "config path (default: %APPDATA%\\LYWP\\config.json)")
	theme := fs.String("theme", defaultTheme, "initial skin id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *password == "" {
		return fmt.Errorf("-password is required")
	}
	if *port <= 0 || *port > 65535 {
		return fmt.Errorf("invalid -port %d", *port)
	}
	cleanName := sanitizeSiteName(*name)
	if cleanName == "" {
		cleanName = "老杨网盘"
	}
	if strings.TrimSpace(*storage) == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return fmt.Errorf("cannot determine user home, please pass -storage")
		}
		*storage = filepath.Join(home, "LYWP")
	}
	root, err := filepath.Abs(*storage)
	if err != nil {
		return fmt.Errorf("invalid storage path: %w", err)
	}
	if st, err := os.Stat(root); err == nil && !st.IsDir() {
		return fmt.Errorf("storage path is a file: %s", root)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("create storage root: %w", err)
	}
	selectedTheme := normalizeTheme(*theme)
	if selectedTheme == "" {
		selectedTheme = defaultTheme
	}
	hash, err := hashPassword(*password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if strings.TrimSpace(*configPath) == "" {
		*configPath = defaultConfigPath()
	}
	configPathAbs, err := filepath.Abs(*configPath)
	if err != nil {
		return fmt.Errorf("invalid config path: %w", err)
	}
	dataDir := filepath.Join(filepath.Dir(configPathAbs), "data")
	for _, dir := range []string{dataDir, filepath.Join(dataDir, "tmp")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}
	}
	cfg := Config{
		Name: cleanName, Port: *port, PasswordHash: hash,
		DataDir: dataDir, StorageRoot: root, SessionDays: 0, Theme: selectedTheme,
	}
	if err := saveConfig(configPathAbs, &cfg); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Printf("config written to %s\n", configPathAbs)
	fmt.Printf("name=%s port=%d storage=%s theme=%s\n", cleanName, *port, root, selectedTheme)
	return nil
}
