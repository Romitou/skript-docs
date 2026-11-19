package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
)

// PlanOptions are the flags of `scanner plan`.
type PlanOptions struct {
	Data      string // repository root (addons/ lives there)
	Toolchain string // hash of images.json
	DepsHash  string // hash of deps.json
	Only      string // one addon slug (workflow_dispatch), empty for all
	Force     bool   // generate again even when nothing changed
	Max       int    // jobs per run; the rest waits for the next run
}

// Decide tells whether the latest release needs a new generation, given the add-on's newest one:
// a new release, or a new jar on the same release; new extractor images (the syntaxes a Skript
// version sees may differ); after a failure, a changed deps.json (a missing dependency may have
// been added); or forced. Nothing else: a generation is never repeated for nothing.
func Decide(index AddonIndex, rel Release, asset Asset, opts PlanOptions) (string, bool) {
	if len(index.Generations) == 0 {
		return "first generation", true
	}
	last := index.Generations[0]
	switch {
	case last.Release.Tag != rel.TagName:
		return "new release", true
	case last.AssetID != asset.ID || last.JarURL != asset.DownloadURL:
		return "new jar for the release", true
	case opts.Force:
		return "forced", true
	case last.Toolchain != opts.Toolchain:
		return "new extractor images", true
	case last.Status == "failed" && last.DepsHash != opts.DepsHash:
		return "retry: deps.json changed", true
	default:
		return "", false
	}
}

// MakePlan lists the generations to run. A repository GitHub cannot answer for, or whose latest
// release has no jar, is skipped and reported on stderr: the others still get done.
func MakePlan(ctx context.Context, github *GitHub, tracked []TrackedAddon, opts PlanOptions) (Plan, error) {
	plan := Plan{Toolchain: opts.Toolchain, DepsHash: opts.DepsHash, Jobs: []Job{}}
	sorted := append([]TrackedAddon(nil), tracked...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	if opts.Only != "" {
		found := false
		for _, a := range sorted {
			found = found || a.Slug == opts.Only
		}
		if !found {
			return plan, fmt.Errorf("%q is not in addons.json", opts.Only)
		}
	}
	for _, addon := range sorted {
		if opts.Only != "" && addon.Slug != opts.Only {
			continue
		}
		releases, err := github.Releases(ctx, addon.Repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s (%s): %v\n", addon.Slug, addon.Repo, err)
			continue
		}
		rel, asset, err := LatestRelease(addon, releases)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s (%s): %v\n", addon.Slug, addon.Repo, err)
			continue
		}
		index, err := loadAddonIndex(opts.Data, addon.Slug)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: unreadable index: %v\n", addon.Slug, err)
			continue
		}
		reason, needed := Decide(index, rel, asset, opts)
		if !needed {
			continue
		}
		plan.Jobs = append(plan.Jobs, Job{
			AddonSlug: addon.Slug, AddonName: addon.Name, Repo: addon.Repo,
			Release: ReleaseInfo{Tag: rel.TagName, Name: rel.Name, URL: rel.HTMLURL, Prerelease: rel.Prerelease, PublishedAt: rel.PublishedAt},
			AssetID: asset.ID, AssetName: asset.Name, JarURL: asset.DownloadURL, Reason: reason,
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
