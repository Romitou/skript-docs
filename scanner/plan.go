package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

// PlanOptions are the flags of `scanner plan`.
type PlanOptions struct {
	Data      string          // repository root (addons/ lives there)
	Toolchain string          // hash of images.json
	DepsHash  string          // hash of deps.json
	Only      string          // one addon slug (workflow_dispatch), empty for all
	Force     bool            // re-extract even what succeeded
	Max       int             // jobs per run; the rest waits for the next run
	Excluded  map[string]bool // slugs never extracted (excluded.json)
}

// LoadExcluded reads excluded.json: add-ons whose author asked not to be listed here.
func LoadExcluded(path string) (map[string]bool, error) {
	var file struct {
		Addons []string `json:"addons"`
	}
	excluded := map[string]bool{}
	if err := readJSON(path, &file); err != nil {
		if os.IsNotExist(err) {
			return excluded, nil
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, slug := range file.Addons {
		excluded[strings.TrimSpace(slug)] = true
	}
	return excluded, nil
}

// IsJarURL reports whether a version's download link is a jar: https, path ending in .jar.
// Same rule as Skript-MC's API.
func IsJarURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Host != "" && strings.HasSuffix(strings.ToLower(u.Path), ".jar")
}

// LatestStable is the version to document: the newest stable release with a jar.
func LatestStable(versions []APIVersion) (APIVersion, bool) {
	sorted := append([]APIVersion(nil), versions...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.After(sorted[j].CreatedAt) })
	for _, v := range sorted {
		if v.ReleaseType != "prerelease" && IsJarURL(v.DownloadURL) && strings.TrimSpace(v.Label) != "" {
			return v, true
		}
	}
	return APIVersion{}, false
}

// Decide tells whether a version needs extracting, given what this repository already has for
// it: never tried; tried on another jar; failed, and the extractor or the dependency catalog
// changed since (a missing dependency may have been added); or forced.
func Decide(index AddonIndex, version APIVersion, opts PlanOptions) (string, bool) {
	for _, entry := range index.Versions {
		if entry.Label != version.Label {
			continue
		}
		switch {
		case entry.JarURL != strings.TrimSpace(version.DownloadURL):
			return "the version's jar changed", true
		case opts.Force:
			return "forced", true
		case entry.Status == "succeeded":
			return "", false
		case entry.Toolchain != opts.Toolchain:
			return "retry: the extractor images changed", true
		case entry.DepsHash != opts.DepsHash:
			return "retry: deps.json changed", true
		default:
			return "", false
		}
	}
	return "new version", true
}

// MakePlan lists the versions to extract. An addon the API cannot answer for is skipped (and
// reported on stderr), not fatal: the others still get done.
func MakePlan(ctx context.Context, api *SkriptMC, opts PlanOptions) (Plan, error) {
	plan := Plan{Toolchain: opts.Toolchain, DepsHash: opts.DepsHash, Jobs: []Job{}}
	addons, err := api.Addons(ctx)
	if err != nil {
		return plan, err
	}
	sort.Slice(addons, func(i, j int) bool { return addons[i].Slug < addons[j].Slug })
	for _, addon := range addons {
		if opts.Only != "" && addon.Slug != opts.Only {
			continue
		}
		// Skript's own documentation comes with each Skript release.
		if addon.Slug == "skript" || !slugPattern.MatchString(addon.Slug) || opts.Excluded[addon.Slug] {
			continue
		}
		versions, err := api.Versions(ctx, addon.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", addon.Slug, err)
			continue
		}
		version, ok := LatestStable(versions)
		if !ok {
			continue
		}
		index, err := loadAddonIndex(opts.Data, addon.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: unreadable index: %v\n", addon.Slug, err)
			continue
		}
		reason, needed := Decide(index, version, opts)
		if !needed {
			continue
		}
		plan.Jobs = append(plan.Jobs, Job{
			AddonID: addon.ID, AddonSlug: addon.Slug, AddonName: addon.Name,
			VersionID: version.ID, VersionLabel: strings.TrimSpace(version.Label), VersionDate: version.CreatedAt,
			JarURL: strings.TrimSpace(version.DownloadURL), Reason: reason,
		})
		if opts.Max > 0 && len(plan.Jobs) >= opts.Max {
			fmt.Fprintf(os.Stderr, "plan: %d jobs, the rest waits for the next run\n", opts.Max)
			break
		}
	}
	return plan, nil
}

// fileHash identifies a configuration file's content (empty for a missing file).
func fileHash(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}
