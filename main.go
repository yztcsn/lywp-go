package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	htmlpkg "html"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

//go:embed web/*
var webFS embed.FS

// siteName is injected into the embedded HTML pages so the name chosen at
// install time is visible without an extra API request.
var siteName = "老杨网盘"

// siteTheme is the server-wide skin. It is stored in config.json and injected
// into every HTML page, including public share pages, before first paint.
var (
	themeMu   sync.RWMutex
	siteTheme = defaultTheme
)

func currentTheme() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return siteTheme
}

func setCurrentTheme(id string) {
	themeMu.Lock()
	siteTheme = id
	themeMu.Unlock()
}

func invalidateHTMLCache() {
	assetCache.Delete("web/index.html")
	assetCache.Delete("web/share.html")
}

// sanitizeSiteName removes replacement characters and control bytes that can
// appear when a name is typed through a non-UTF-8 terminal. They used to show
// up as black-diamond question marks on the login page.
func sanitizeSiteName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

type cachedAsset struct {
	etag string
	body []byte
}

var assetCache sync.Map

func serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	v, ok := assetCache.Load(name)
	if !ok {
		b, err := webFS.ReadFile(name)
		if err != nil {
			http.Error(w, "asset not found", http.StatusNotFound)
			return
		}
		if strings.HasSuffix(name, ".html") {
			b = bytes.ReplaceAll(b, []byte("老杨网盘"), []byte(htmlpkg.EscapeString(siteName)))
			theme := htmlpkg.EscapeString(currentTheme())
			b = bytes.ReplaceAll(b, []byte(`<html lang="zh-CN">`),
				[]byte(`<html lang="zh-CN" data-theme="`+theme+`">`))
		}
		sum := sha256.Sum256(b)
		v = cachedAsset{etag: `"` + hex.EncodeToString(sum[:8]) + `"`, body: b}
		assetCache.Store(name, v)
	}
	asset := v.(cachedAsset)
	w.Header().Set("ETag", asset.etag)
	if r != nil && r.Header.Get("If-None-Match") == asset.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if strings.HasSuffix(name, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	_, _ = w.Write(asset.body)
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "serve":
		configPath := "/etc/lywp/config.json"
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "--config" && i+1 < len(os.Args) {
				configPath = os.Args[i+1]
				i++
			}
		}
		if err := run(configPath); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "hash":
		if len(os.Args) < 3 {
			fmt.Println("usage: lywp-server hash <password>")
			os.Exit(2)
		}
		h, err := hashPassword(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(h)
	case "init-config":
		if err := initConfigFromEnv(); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Println(`lywp - lightweight Go netdisk

commands:
  serve --config <path>   start the HTTP server
  hash <password>         print a salted password hash
  init-config             write config from LYWP_* environment variables`)
}

func run(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	if clean := sanitizeSiteName(cfg.Name); clean != "" {
		siteName = clean
	}
	setCurrentTheme(cfg.Theme)
	store, err := openStore(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.close()
	store.markInterruptedOfflineTasks()
	srv := newServer(cfg, store, configPath)
	go cleanTempLoop(cfg.FilesDir())

	// No ReadTimeout / WriteTimeout: large uploads and downloads may take
	// arbitrarily long. Only header parsing and idle keep-alives are bounded.
	httpServer := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", cfg.Port),
		Handler:           srv,
		ReadHeaderTimeout: 60 * time.Second,
		IdleTimeout:       5 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		store.cleanupSessions()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			store.cleanupSessions()
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("%s listening on :%d (data: %s, netdisk root: %s)", cfg.Name, cfg.Port, cfg.DataDir, cfg.FilesDir())
		errCh <- httpServer.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return httpServer.Shutdown(ctx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// cleanTempLoop removes streaming files left behind by interrupted uploads.
func cleanTempLoop(filesDir string) {
	clean := func() {
		_ = filepath.Walk(filesDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasPrefix(info.Name(), ".streaming_") && time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.Remove(path)
			}
			return nil
		})
	}
	clean()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		clean()
	}
}
