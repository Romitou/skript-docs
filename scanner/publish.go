package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxResultBytes = 1 << 20
	maxLogBytes    = 16 << 10
)

var (
	errorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	jarSHAPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func jobKey(j Job) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", j.AddonSlug, j.Release.Tag, j.AssetID, j.JarURL)
}

// GenerationID names a generation and its files: date and time (UTC), then the release tag.
func GenerationID(at time.Time, tag string) string {
	return at.UTC().Format("20060102-150405") + "-" + safeFileName(tag)
}

// Publish adds the results of a run to the repository's data, as new generations: nothing is ever
// replaced. Each result must match a job of the plan (computed before any add-on code ran), and is
// checked and cleaned before being written; the identity of what is published (add-on, release,
// jar) always comes from the plan, never from the result. Returns one line per job.
func Publish(data string, tracked []TrackedAddon, plan Plan, resultsDir string, now time.Time) ([]string, error) {
	planned := map[string]Job{}
	for _, job := range plan.Jobs {
		planned[jobKey(job)] = job
	}
	var summary []string
	var resultFiles []string
	if resultsDir != "" {
		_ = filepath.WalkDir(resultsDir, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "result.json" {
				resultFiles = append(resultFiles, path)
			}
			return nil
		})
	}
	sort.Strings(resultFiles)
	done := map[string]bool{}
	for _, path := range resultFiles {
		line, err := publishOne(data, plan, planned, done, filepath.Dir(path), now)
		if err != nil {
			return summary, err
		}
		summary = append(summary, line)
	}
	for key, job := range planned {
		if !done[key] {
			summary = append(summary, fmt.Sprintf("%s %s: no result (the extraction job did not finish), tried again next run", job.AddonSlug, job.Release.Tag))
		}
	}
	if err := writeRootIndex(data, tracked); err != nil {
		return summary, err
	}
	// A commit at least once a month: GitHub disables the schedule of a public repository after 60
	// days without activity.
	return summary, writeJSON(filepath.Join(data, "status.json"), map[string]any{
		"formatVersion": FormatVersion,
		"lastRunMonth":  now.UTC().Format("2006-01"),
	})
}

func publishOne(data string, plan Plan, planned map[string]Job, done map[string]bool, dir string, now time.Time) (string, error) {
	raw, err := readLimited(filepath.Join(dir, "result.json"), maxResultBytes)
	if err != nil {
		return fmt.Sprintf("%s: unreadable result, ignored: %v", dir, err), nil
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Sprintf("%s: invalid result, ignored: %v", dir, err), nil
	}
	key := jobKey(result.Job)
	job, ok := planned[key]
	if !ok || done[key] {
		return fmt.Sprintf("%s: result for an unplanned or already published job, ignored", dir), nil
	}
	done[key] = true
	if !slugPattern.MatchString(job.AddonSlug) {
		return fmt.Sprintf("%s: invalid slug, ignored", job.AddonSlug), nil
	}

	gen := Generation{
		ID:           GenerationID(now, job.Release.Tag),
		Release:      job.Release,
		AssetID:      job.AssetID,
		AssetName:    clean(job.AssetName, 255),
		JarURL:       job.JarURL,
		Reason:       job.Reason,
		Status:       "failed",
		Plugin:       PluginMeta{Name: clean(result.Plugin.Name, 128), Version: clean(result.Plugin.Version, 128), APIVersion: clean(result.Plugin.APIVersion, 32)},
		Image:        clean(result.Image, 255),
		Minecraft:    clean(result.Minecraft, 32),
		Skript:       clean(result.Skript, 32),
		Dependencies: lines(result.Dependencies, 50),
		Warnings:     lines(result.Warnings, maxWarnings),
		GeneratedAt:  now.UTC().Truncate(time.Second),
		Toolchain:    plan.Toolchain,
		DepsHash:     plan.DepsHash,
	}
	if jarSHAPattern.MatchString(result.JarSHA256) {
		gen.JarSHA256 = result.JarSHA256
	}
	folder := addonDir(data, job.AddonSlug)

	var export Export
	if result.Status == "succeeded" {
		rawExport, err := readLimited(filepath.Join(dir, "export.json"), 32<<20)
		if err == nil {
			export, err = Sanitize(rawExport)
		}
		switch {
		case err != nil:
			gen.ErrorCode, gen.ErrorMessage = "INVALID_EXPORT", truncate(err.Error(), 2000)
		case export.Stats.Total == 0:
			gen.ErrorCode, gen.ErrorMessage = "NO_SYNTAX", "The add-on registered no syntax on this server."
		default:
			gen.Status = "succeeded"
			gen.SyntaxCount = export.Stats.Total
			gen.File = gen.ID + ".json"
			gen.Warnings = append(gen.Warnings, export.Warnings...)
			if len(gen.Warnings) > maxWarnings {
				gen.Warnings = gen.Warnings[:maxWarnings]
			}
		}
	} else {
		gen.ErrorCode = result.ErrorCode
		if !errorCodePattern.MatchString(gen.ErrorCode) {
			gen.ErrorCode = "UNKNOWN"
		}
		gen.ErrorMessage = truncate(clean(result.ErrorMessage, 2000), 2000)
	}

	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	if gen.Status == "succeeded" {
		if err := writeJSON(filepath.Join(folder, gen.File), export); err != nil {
			return "", err
		}
	} else if log, err := readLimited(filepath.Join(dir, "server.log"), 1<<20); err == nil && len(log) > 0 {
		gen.Log = gen.ID + ".log"
		if err := os.WriteFile(filepath.Join(folder, gen.Log), []byte(tail(string(log), maxLogBytes)), 0o644); err != nil {
			return "", err
		}
	}

	index, err := loadAddonIndex(data, job.AddonSlug)
	if err != nil {
		return "", fmt.Errorf("%s: %w", job.AddonSlug, err)
	}
	index.FormatVersion = FormatVersion
	index.Slug, index.Name, index.Repo = job.AddonSlug, clean(job.AddonName, 128), job.Repo
	index.Generations = append([]Generation{gen}, index.Generations...)
	index.Latest = ""
	if latest := index.latestOf(); latest != nil {
		index.Latest = latest.ID
	}
	if err := writeJSON(filepath.Join(folder, "index.json"), index); err != nil {
		return "", err
	}
	if gen.Status == "succeeded" {
		return fmt.Sprintf("%s %s: %d syntaxes (%s)", job.AddonSlug, job.Release.Tag, gen.SyntaxCount, job.Reason), nil
	}
	return fmt.Sprintf("%s %s: failed, %s (%s)", job.AddonSlug, job.Release.Tag, gen.ErrorCode, job.Reason), nil
}

// writeRootIndex rebuilds index.json from every addons/<slug>/index.json. An add-on removed from
// addons.json keeps its history, marked untracked.
func writeRootIndex(data string, tracked []TrackedAddon) error {
	isTracked := map[string]bool{}
	for _, a := range tracked {
		isTracked[a.Slug] = true
	}
	root := RootIndex{FormatVersion: FormatVersion, Addons: []RootAddon{}}
	paths, _ := filepath.Glob(filepath.Join(data, "addons", "*", "index.json"))
	sort.Strings(paths)
	for _, path := range paths {
		var index AddonIndex
		if err := readJSON(path, &index); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		line := RootAddon{Slug: index.Slug, Name: index.Name, Repo: index.Repo, Tracked: isTracked[index.Slug]}
		if len(index.Generations) > 0 {
			last := index.Generations[0]
			line.Last = &RootLast{Generation: last.ID, Release: last.Release.Tag, Status: last.Status, ErrorCode: last.ErrorCode}
		}
		if latest := index.latestOf(); latest != nil {
			line.Latest = &RootLatest{
				Generation:  latest.ID,
				Release:     latest.Release.Tag,
				File:        "addons/" + index.Slug + "/" + latest.File,
				SyntaxCount: latest.SyntaxCount,
				JarSHA256:   latest.JarSHA256,
				GeneratedAt: latest.GeneratedAt,
			}
		}
		root.Addons = append(root.Addons, line)
	}
	return writeJSON(filepath.Join(data, "index.json"), root)
}

func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[len(s)-max:]
	for len(cut) > 0 && strings.ToValidUTF8(cut, "�") != cut {
		cut = cut[1:]
	}
	return "[…]\n" + cut
}
