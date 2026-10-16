// Package config loads server configuration from the environment.
// See CONTRACT.md §10. All variables have safe defaults for local dev.
package config

import (
	"os"
	"strings"
)

// Config is the complete server configuration.
type Config struct {
	Port          string   // PORT, default "8080"
	DatabasePath  string   // DATABASE_PATH, default "./data/forge-space.db"
	StaticDir     string   // STATIC_DIR, default "./web/dist"
	CORSOrigins   []string // CORS_ORIGINS, comma-separated, default ["*"]
	LiveKitURL    string   // LIVEKIT_URL, optional
	LiveKitKey    string   // LIVEKIT_API_KEY, optional
	LiveKitSecret string   // LIVEKIT_API_SECRET, optional
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment.
func Load() Config {
	origins := strings.Split(env("CORS_ORIGINS", "*"), ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}
	return Config{
		Port:          env("PORT", "8080"),
		DatabasePath:  env("DATABASE_PATH", "./data/forge-space.db"),
		StaticDir:     env("STATIC_DIR", "./web/dist"),
		CORSOrigins:   origins,
		LiveKitURL:    env("LIVEKIT_URL", ""),
		LiveKitKey:    env("LIVEKIT_API_KEY", ""),
		LiveKitSecret: env("LIVEKIT_API_SECRET", ""),
	}
}

// Addr returns the listen address.
func (c Config) Addr() string { return ":" + c.Port }

// MediaEnabled reports whether LiveKit voice/video is configured.
func (c Config) MediaEnabled() bool { return c.LiveKitURL != "" }

// CORSAllowed reports whether an Origin header value is permitted.
func (c Config) CORSAllowed(origin string) bool {
	for _, o := range c.CORSOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	return false
}
