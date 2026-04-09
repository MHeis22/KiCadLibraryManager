package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Repository represents a single Git library
type Repository struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// HistoryItem tracks an integration event for undo purposes
type HistoryItem struct {
	ID           string   `json:"id"`
	Timestamp    int64    `json:"timestamp"`
	Filename     string   `json:"filename"`
	Category     string   `json:"category"`
	RepoName     string   `json:"repoName"`
	AddedFiles   []string `json:"addedFiles"`
	SymbolMaster string   `json:"symbolMaster"`
	SymbolBackup string   `json:"symbolBackup"`
}

// Config represents the user's saved settings
type Config struct {
	BaseLibPath  string        `json:"baseLibPath"`
	WatchDir     string        `json:"watchDir"`
	Repositories []Repository  `json:"repositories"`
	Categories   []string      `json:"categories"`
	History      []HistoryItem `json:"history"`
	AutoStart    bool          `json:"autoStart"`
}

func getConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	appDir := filepath.Join(configDir, "KiCadLibMgr")
	os.MkdirAll(appDir, os.ModePerm)
	return filepath.Join(appDir, "config.json")
}

func LoadConfig() Config {
	path := getConfigPath()
	data, err := os.ReadFile(path)

	homeDir, _ := os.UserHomeDir()
	defaultWatchDir := filepath.Join(homeDir, "Downloads")

	// Default configuration
	defaultConfig := Config{
		BaseLibPath:  "",
		WatchDir:     defaultWatchDir,
		Repositories: []Repository{{Name: "CustomLibs", URL: ""}},
		Categories:   []string{"MCU", "Regulators", "Connectors", "Passives", "OpAmps"},
		History:      []HistoryItem{},
		AutoStart:    false,
	}

	if err != nil {
		return defaultConfig
	}

	var c Config
	err = json.Unmarshal(data, &c)
	if err != nil || len(c.Categories) == 0 {
		return defaultConfig
	}

	// Ensure at least one repo exists for safety
	if len(c.Repositories) == 0 {
		c.Repositories = defaultConfig.Repositories
	}

	// Ensure watch dir defaults if somehow empty
	if c.WatchDir == "" {
		c.WatchDir = defaultWatchDir
	}

	return c
}

func SaveConfig(c Config) error {
	path := getConfigPath()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
