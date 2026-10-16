// forge-space-server: REST API + WebSocket realtime + static client serving.
// Serves STATIC_DIR with an SPA fallback (non-/api paths that aren't real
// files return web/dist/index.html). A missing STATIC_DIR is a warning, not a
// crash: the server runs API-only.
package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azmi2409/forge-space/internal/api"
	"github.com/azmi2409/forge-space/internal/config"
	"github.com/azmi2409/forge-space/internal/livekitx"
	"github.com/azmi2409/forge-space/internal/realtime"
	"github.com/azmi2409/forge-space/internal/store"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	lk := &livekitx.Client{URL: cfg.LiveKitURL, Key: cfg.LiveKitKey, Secret: cfg.LiveKitSecret}
	if lk.Enabled() {
		log.Printf("livekit: enabled (%s)", cfg.LiveKitURL)
	} else {
		log.Printf("livekit: disabled (LIVEKIT_URL unset) — voice/video off, everything else works")
	}

	hub := realtime.New(st)
	go hub.Run()
	defer hub.Close()
	// Dev default CORS "*" → skip WS origin check; locked-down origins → enforce.
	realtime.SetInsecureWS(len(cfg.CORSOrigins) == 1 && cfg.CORSOrigins[0] == "*")

	srv := api.New(st, hub, lk, cfg)
	handler := withStatic(srv.Router(), cfg.StaticDir)

	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("forge-space listening on %s (db=%s)", cfg.Addr(), cfg.DatabasePath)
	log.Fatal(httpServer.ListenAndServe())
}

// withStatic wraps the API router with static file serving + SPA fallback.
// Requests under /api/* always go to the API. Anything else: serve the real
// file from STATIC_DIR when it exists, else STATIC_DIR/index.html (history-API
// routing: /, /s/:slug, /s/:slug/edit). Missing STATIC_DIR → API-only mode.
func withStatic(apiHandler http.Handler, staticDir string) http.Handler {
	info, err := os.Stat(staticDir)
	if err != nil || !info.IsDir() {
		log.Printf("warn: STATIC_DIR %q missing or not a directory — running API-only", staticDir)
		return apiHandler
	}
	index := filepath.Join(staticDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		log.Printf("warn: %q not found — running API-only", index)
		return apiHandler
	}
	fileServer := http.FileServer(http.Dir(staticDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// Real file? serve it. Otherwise the SPA entrypoint.
		clean := filepath.Clean(r.URL.Path)
		if strings.Contains(clean, "..") {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(staticDir, filepath.FromSlash(clean))
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}
