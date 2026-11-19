package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The data this repository publishes (README § Les données). FormatVersion changes only on an
// incompatible change.
const FormatVersion = 1

// ReleaseInfo identifies the GitHub release a generation documents.
type ReleaseInfo struct {
	Tag         string    `json:"tag"`
	Name        string    `json:"name,omitempty"`
	URL         string    `json:"url"`
	Prerelease  bool      `json:"prerelease,omitempty"`
	PublishedAt time.Time `json:"publishedAt"`
}

// Job is one generation to run, decided by `scanner plan` from addons.json, the repositories'
// releases and this repository's history.
type Job struct {
	AddonSlug string      `json:"addonSlug"`
	AddonName string      `json:"addonName"`
	Repo      string      `json:"repo"`
	Release   ReleaseInfo `json:"release"`
	AssetID   int64       `json:"assetId"`
	AssetName string      `json:"assetName"`
	JarURL    string      `json:"jarUrl"`
	Reason    string      `json:"reason"`
}

// Plan is the output of `scanner plan`, handed to `scanner publish`: only results matching a
// planned job are published.
type Plan struct {
	Toolchain string `json:"toolchain"`
	DepsHash  string `json:"depsHash"`
	Jobs      []Job  `json:"jobs"`
}

// PluginMeta is what the jar's plugin.yml says.
type PluginMeta struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
}

// Generation is one run of the extraction on a release, kept forever in
// addons/<slug>/index.json, newest first, with its files next to it.
type Generation struct {
	ID           string      `json:"id"` // 20261119-165207-3.0.0: date, time (UTC) and tag
	Release      ReleaseInfo `json:"release"`
	AssetID      int64       `json:"assetId"`
	AssetName    string      `json:"assetName"`
	JarURL       string      `json:"jarUrl"`
	JarSHA256    string      `json:"jarSha256,omitempty"`
	Reason       string      `json:"reason"`
	Status       string      `json:"status"` // "succeeded" | "failed"
	ErrorCode    string      `json:"errorCode,omitempty"`
	ErrorMessage string      `json:"errorMessage,omitempty"`
	File         string      `json:"file,omitempty"` // the export, next to index.json
	Log          string      `json:"log,omitempty"`  // end of the server's log, failures only
	SyntaxCount  int         `json:"syntaxCount"`
	Plugin       PluginMeta  `json:"plugin"`
	Image        string      `json:"image,omitempty"`
	Minecraft    string      `json:"minecraft,omitempty"`
	Skript       string      `json:"skript,omitempty"`
	Dependencies []string    `json:"dependencies"`
	Warnings     []string    `json:"warnings"`
	GeneratedAt  time.Time   `json:"generatedAt"`
	Toolchain    string      `json:"toolchain"` // hash of extractor/images.json
	DepsHash     string      `json:"depsHash"`  // hash of deps.json
}

// AddonIndex is addons/<slug>/index.json.
type AddonIndex struct {
	FormatVersion int          `json:"formatVersion"`
	Slug          string       `json:"slug"`
	Name          string       `json:"name"`
	Repo          string       `json:"repo"`
	Latest        string       `json:"latest,omitempty"` // id of the newest successful generation
	Generations   []Generation `json:"generations"`      // newest first
}

// RootIndex is index.json: one line per add-on, enough for a consumer to know what changed.
type RootIndex struct {
	FormatVersion int         `json:"formatVersion"`
	Addons        []RootAddon `json:"addons"`
}

type RootAddon struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Repo    string `json:"repo"`
	Tracked bool   `json:"tracked"` // still in addons.json
	// Latest is the newest successful generation; Last the newest one, successful or not.
	Latest *RootLatest `json:"latest"`
	Last   *RootLast   `json:"last"`
}

type RootLatest struct {
	Generation  string    `json:"generation"`
	Release     string    `json:"release"`
	File        string    `json:"file"` // relative to the repository root
	SyntaxCount int       `json:"syntaxCount"`
	JarSHA256   string    `json:"jarSha256"`
	GeneratedAt time.Time `json:"generatedAt"`
}

type RootLast struct {
	Generation string `json:"generation"`
	Release    string `json:"release"`
	Status     string `json:"status"`
	ErrorCode  string `json:"errorCode,omitempty"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

func addonDir(data, slug string) string {
	return filepath.Join(data, "addons", slug)
}

func readJSON(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// writeJSON writes indented JSON with a final newline, only when the content changed (no commit
// for nothing).
func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if old, err := os.ReadFile(path); err == nil && string(old) == string(raw) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func loadAddonIndex(data, slug string) (AddonIndex, error) {
	var index AddonIndex
	err := readJSON(filepath.Join(addonDir(data, slug), "index.json"), &index)
	if os.IsNotExist(err) {
		return AddonIndex{FormatVersion: FormatVersion, Slug: slug, Generations: []Generation{}}, nil
	}
	return index, err
}

// latestOf is the newest successful generation, if any.
func (index AddonIndex) latestOf() *Generation {
	for i := range index.Generations {
		if index.Generations[i].Status == "succeeded" {
			return &index.Generations[i]
		}
	}
	return nil
}
