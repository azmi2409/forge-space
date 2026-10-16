package main

// Entry point: forge-space-mcp, the MCP server (stdio) that lets AI agents
// join forge-space worlds as first-class participants.
//
//	Config: FORGE_SPACE_URL (default http://localhost:8080),
//	        FORGE_SPACE_KEY (required, fspk_... agent key)

import (
	"fmt"
	"io"
	"os"
	"strings"
)

type Config struct {
	BaseURL string
	Key     string
}

func loadConfig() (Config, error) {
	base := os.Getenv("FORGE_SPACE_URL")
	if base == "" {
		base = "http://localhost:8080"
	}
	key := os.Getenv("FORGE_SPACE_KEY")
	if key == "" {
		return Config{}, fmt.Errorf("FORGE_SPACE_KEY is not set (required: an agent key in fspk_... format)")
	}
	if !strings.HasPrefix(key, "fspk_") {
		return Config{}, fmt.Errorf("FORGE_SPACE_KEY must start with \"fspk_\"")
	}
	return Config{BaseURL: base, Key: key}, nil
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "forge-space-mcp: "+err.Error())
		os.Exit(2)
	}
}

func run(r io.Reader, w io.Writer) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	NewMCPServer(cfg.BaseURL, cfg.Key).Serve(r, w)
	return nil
}
