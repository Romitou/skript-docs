package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// The data this repository publishes (README § Format). FormatVersion changes only on an
// incompatible change.
const FormatVersion = 1

// Job is one version to extract, decided by `scanner plan` from Skript-MC's API and the state of
// this repository.
type Job struct {
	AddonID      uint      `json:"addonId"`
	AddonSlug    string    `json:"addonSlug"`
	AddonName    string    `json:"addonName"`
	VersionID    uint      `json:"versionId"`
	VersionLabel string    `json:"versionLabel"`
	VersionDate  time.Time `json:"versionDate"`
	JarURL       string    `json:"jarUrl"`
	Reason       string    `json:"reason"`
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

// VersionEntry is the state of one extracted version, in addons/<slug>/index.json.
type VersionEntry struct {
	Label        string     `json:"label"`
	VersionID    uint       `json:"versionId"`
	VersionDate  time.Time  `json:"versionDate"`
	JarURL       string     `json:"jarUrl"`
	JarSHA256    string     `json:"jarSha256,omitempty"`
	Status       string     `json:"status"` // "succeeded" | "failed"
	ErrorCode    string     `json:"errorCode,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	File         string     `json:"file,omitempty"` // the export, next to index.json
	Log          string     `json:"log,omitempty"`  // end of the server's log, failures only
	SyntaxCount  int        `json:"syntaxCount"`
	Plugin       PluginMeta `json:"plugin"`
	Image        string     `json:"image,omitempty"`
	Minecraft    string     `json:"minecraft,omitempty"`
	Skript       string     `json:"skript,omitempty"`
	Dependencies []string   `json:"dependencies"`
	Warnings     []string   `json:"warnings"`
	ExtractedAt  time.Time  `json:"extractedAt"`
	Toolchain    string     `json:"toolchain"` // hash of extractor/images.json
	DepsHash     string     `json:"depsHash"`  // hash of deps.json
}

// AddonIndex is addons/<slug>/index.json.
type AddonIndex struct {
	FormatVersion int            `json:"formatVersion"`
	ID            uint           `json:"id"`
	Slug          string         `json:"slug"`
	Name          string         `json:"name"`
	Latest        string         `json:"latest,omitempty"` // label of the newest version extracted successfully
	Versions      []VersionEntry `json:"versions"`         // newest version first
}

// RootIndex is index.json: one line per add-on, enough for a site to know what changed.
type RootIndex struct {
	FormatVersion int         `json:"formatVersion"`
	Addons        []RootAddon `json:"addons"`
}

type RootAddon struct {
	ID     uint        `json:"id"`
	Slug   string      `json:"slug"`
	Name   string      `json:"name"`
	Latest *RootLatest `json:"latest"`
	// Last is the newest version tried, successful or not.
	Last *RootLast `json:"last"`
}

type RootLatest struct {
	Label       string    `json:"label"`
	File        string    `json:"file"` // relative to the repository root
	SyntaxCount int       `json:"syntaxCount"`
	JarSHA256   string    `json:"jarSha256"`
	ExtractedAt time.Time `json:"extractedAt"`
}

type RootLast struct {
	Label     string `json:"label"`
	Status    string `json:"status"`
	ErrorCode string `json:"errorCode,omitempty"`
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
		return AddonIndex{FormatVersion: FormatVersion, Slug: slug}, nil
	}
	return index, err
}

// sortVersions puts the newest version first and points Latest at the newest successful one.
func (index *AddonIndex) sortVersions() {
	sort.SliceStable(index.Versions, func(i, j int) bool {
		return index.Versions[i].VersionDate.After(index.Versions[j].VersionDate)
	})
	index.Latest = ""
	for _, v := range index.Versions {
		if v.Status == "succeeded" {
			index.Latest = v.Label
			break
		}
	}
}
