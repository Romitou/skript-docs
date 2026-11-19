package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Catalog is deps.json: the plugins add-ons may depend on (Vault, ProtocolLib, other Skript
// add-ons...), by download link and checksum. The repository never holds their jars (their
// licences rarely allow redistributing them): they are downloaded when an add-on needs them, and
// refused if the checksum differs. The extraction containers have no network: what an add-on
// needs must be listed here.
type Catalog struct {
	Plugins []CatalogEntry `json:"plugins"`
	byName  map[string]int
}

type CatalogEntry struct {
	Name     string   `json:"name"`     // plugin.yml name
	Provides []string `json:"provides"` // plugin.yml provides
	URL      string   `json:"url"`
	SHA256   string   `json:"sha256"`
	Note     string   `json:"note,omitempty"` // where the link comes from, licence...
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadCatalog reads deps.json; a missing file is an empty catalog.
func LoadCatalog(path string) (Catalog, error) {
	catalog := Catalog{byName: map[string]int{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return catalog, nil
	}
	if err != nil {
		return catalog, err
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return catalog, fmt.Errorf("%s: %w", path, err)
	}
	catalog.byName = map[string]int{}
	for i, entry := range catalog.Plugins {
		u, err := url.Parse(entry.URL)
		if entry.Name == "" || err != nil || u.Scheme != "https" || !sha256Pattern.MatchString(entry.SHA256) {
			return catalog, fmt.Errorf("%s: entry %d (%q) needs a name, an https url and a lowercase sha256", path, i, entry.Name)
		}
		for _, name := range append([]string{entry.Name}, entry.Provides...) {
			if _, taken := catalog.byName[strings.ToLower(name)]; !taken {
				catalog.byName[strings.ToLower(name)] = i
			}
		}
	}
	return catalog, nil
}

// Resolution is what a plugin gets installed next to it.
type Resolution struct {
	Jars        []string // downloaded jars to install
	Installed   []string // their plugin names
	MissingHard []string // required but absent: the plugin will not enable
	MissingSoft []string // optional and absent: some syntaxes may be missing
}

// Fetcher downloads a catalog entry into dir and returns the jar's path. Download checks the
// checksum.
type Fetcher func(ctx context.Context, entry CatalogEntry, dir string) (string, error)

// FetchEntry is the real Fetcher: https only, from the entry's own host, checksum required.
func FetchEntry(maxBytes int64) Fetcher {
	return func(ctx context.Context, entry CatalogEntry, dir string) (string, error) {
		u, _ := url.Parse(entry.URL)
		path := filepath.Join(dir, safeFileName(entry.Name)+".jar")
		sum, err := Download(ctx, entry.URL, path, []string{strings.ToLower(u.Hostname())}, maxBytes)
		if err != nil {
			return "", fail("MISSING_DEPENDENCY", "%s could not be downloaded: %v", entry.Name, err)
		}
		if sum != entry.SHA256 {
			return "", fail("DEPENDENCY_CHECKSUM", "%s: the downloaded jar's sha256 is %s, deps.json says %s", entry.Name, sum, entry.SHA256)
		}
		return path, nil
	}
}

// Resolve downloads what plugin needs: its hard dependencies (and theirs), and its soft
// dependencies the catalog has. Skript itself is in every image.
func (c Catalog) Resolve(ctx context.Context, plugin PluginInfo, dir string, fetch Fetcher) (Resolution, error) {
	var res Resolution
	seen := map[string]bool{strings.ToLower(plugin.Name): true, "skript": true}
	type want struct {
		name string
		hard bool
	}
	var queue []want
	for _, d := range plugin.Depend {
		queue = append(queue, want{d, true})
	}
	for _, d := range plugin.SoftDepend {
		queue = append(queue, want{d, false})
	}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		key := strings.ToLower(w.name)
		if seen[key] {
			continue
		}
		seen[key] = true
		index, ok := c.byName[key]
		if !ok {
			if w.hard {
				res.MissingHard = append(res.MissingHard, w.name)
			} else {
				res.MissingSoft = append(res.MissingSoft, w.name)
			}
			continue
		}
		entry := c.Plugins[index]
		seen[strings.ToLower(entry.Name)] = true
		path, err := fetch(ctx, entry, dir)
		if err != nil {
			if !w.hard {
				res.MissingSoft = append(res.MissingSoft, w.name+" (download failed)")
				continue
			}
			return res, err
		}
		info, err := InspectJar(path)
		if err != nil {
			return res, fail("MISSING_DEPENDENCY", "%s from deps.json is not a plugin: %v", entry.Name, err)
		}
		res.Jars = append(res.Jars, path)
		res.Installed = append(res.Installed, info.Name)
		// A dependency's own hard dependencies are needed for it to enable.
		for _, d := range info.Depend {
			queue = append(queue, want{d, w.hard})
		}
	}
	return res, nil
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

// safeFileName turns a version label or plugin name into a file name: "1.0 beta/2" →
// "1.0_beta_2". Never empty, never hidden, never "..".
func safeFileName(s string) string {
	s = unsafeFileChars.ReplaceAllString(strings.TrimSpace(s), "_")
	s = strings.TrimLeft(s, ".")
	if s == "" {
		s = "_"
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
