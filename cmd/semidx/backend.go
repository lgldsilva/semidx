package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lgldsilva/semidx/internal/clientconfig"
)

// Backend mode constants for --backend / SEMIDX_BACKEND.
const (
	backendAuto   = "auto"
	backendLocal  = "local"
	backendRemote = "remote"
)

// resolveUseRemote decides whether this CLI invocation talks to a remote
// semidx server. Precedence:
//
//  1. --local always forces local (ignores a saved login for this run)
//  2. --backend local|remote|auto (flag)
//  3. SEMIDX_BACKEND environment variable
//  4. auto: remote when clientconfig has a ServerURL, otherwise local
func resolveUseRemote(cc *clientconfig.Config, forceLocal bool, backendFlag string) (bool, error) {
	mode, err := resolveBackendMode(backendFlag)
	if err != nil {
		return false, err
	}
	hasServer := cc != nil && cc.ServerURL != ""

	if forceLocal || mode == backendLocal {
		return false, nil
	}
	if mode == backendRemote {
		if !hasServer {
			return false, fmt.Errorf("--backend remote requires a server: run `semidx login <url> --token ...` first")
		}
		return true, nil
	}
	// auto
	return hasServer, nil
}

// resolveBackendMode normalizes the flag/env value to auto|local|remote.
func resolveBackendMode(backendFlag string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(backendFlag))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(os.Getenv("SEMIDX_BACKEND")))
	}
	if mode == "" {
		return backendAuto, nil
	}
	switch mode {
	case backendAuto, backendLocal, backendRemote:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid --backend %q (use auto, local, or remote)", mode)
	}
}

// errIndexInRemoteMode explains why `semidx index` refused to run while a
// remote server is the active backend, and what to run instead.
func errIndexInRemoteMode(serverURL, projectPath string) error {
	if projectPath == "" {
		projectPath = "."
	}
	return fmt.Errorf(`index writes to a local store, but remote mode is active (%s).

To send files to the server for indexing:
  semidx push --project %s
  semidx index --to-server --project %s
  semidx repo add <git-url>          # server clones and indexes

To index on this machine instead (ignore saved login for this run):
  semidx --local index --project %s
  semidx --backend local index --project %s

Or disconnect the server: semidx logout`, serverURL, projectPath, projectPath, projectPath, projectPath)
}
