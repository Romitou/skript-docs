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
	return j.AddonSlug + "\x00" + j.VersionLabel + "\x00" + j.JarURL
}

// Publish writes the results of a run into the repository's data: each result must match a job of
// the plan (computed before any add-on code ran), and is checked and cleaned before being written.
// The identity of what is published (add-on, version, jar) always comes from the plan, never from
// the result. Returns one line per result.
func Publish(data string, plan Plan, resultsDir string, now time.Time) ([]string, error) {
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
			summary = append(summary, fmt.Sprintf("%s %s: no result (the extraction job did not finish), retried next run", job.AddonSlug, job.VersionLabel))
		}
	}
	if err := writeRootIndex(data); err != nil {
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

	entry := VersionEntry{
		Label:        job.VersionLabel,
		VersionID:    job.VersionID,
		VersionDate:  job.VersionDate,
		JarURL:       job.JarURL,
		Status:       "failed",
		Plugin:       PluginMeta{Name: clean(result.Plugin.Name, 128), Version: clean(result.Plugin.Version, 128), APIVersion: clean(result.Plugin.APIVersion, 32)},
		Image:        clean(result.Image, 255),
		Minecraft:    clean(result.Minecraft, 32),
		Skript:       clean(result.Skript, 32),
		Dependencies: lines(result.Dependencies, 50),
		Warnings:     lines(result.Warnings, maxWarnings),
		ExtractedAt:  now.UTC().Truncate(time.Second),
		Toolchain:    plan.Toolchain,
		DepsHash:     plan.DepsHash,
	}
	if jarSHAPattern.MatchString(result.JarSHA256) {
		entry.JarSHA256 = result.JarSHA256
	}
	base := safeFileName(job.VersionLabel)
	folder := addonDir(data, job.AddonSlug)

	var export Export
	if result.Status == "succeeded" {
		rawExport, err := readLimited(filepath.Join(dir, "export.json"), 32<<20)
		if err == nil {
			export, err = Sanitize(rawExport)
		}
		switch {
		case err != nil:
			entry.ErrorCode, entry.ErrorMessage = "INVALID_EXPORT", truncate(err.Error(), 2000)
		case export.Stats.Total == 0:
			entry.ErrorCode, entry.ErrorMessage = "NO_SYNTAX", "The add-on registered no syntax on this server."
		default:
			entry.Status = "succeeded"
			entry.SyntaxCount = export.Stats.Total
			entry.File = base + ".json"
			entry.Warnings = append(entry.Warnings, export.Warnings...)
			if len(entry.Warnings) > maxWarnings {
				entry.Warnings = entry.Warnings[:maxWarnings]
			}
		}
	} else {
		entry.ErrorCode = result.ErrorCode
		if !errorCodePattern.MatchString(entry.ErrorCode) {
			entry.ErrorCode = "UNKNOWN"
		}
		entry.ErrorMessage = truncate(clean(result.ErrorMessage, 2000), 2000)
	}

	index, err := loadAddonIndex(data, job.AddonSlug)
	if err != nil {
		return "", fmt.Errorf("%s: %w", job.AddonSlug, err)
	}
	position := -1
	for i, existing := range index.Versions {
		if existing.Label == job.VersionLabel {
			position = i
		}
	}
	// A failed retry never replaces a success on the same jar (a forced run that hit a flaky
	// download, for instance).
	if position >= 0 && entry.Status == "failed" {
		if old := index.Versions[position]; old.Status == "succeeded" && old.JarURL == entry.JarURL {
			return fmt.Sprintf("%s %s: failed (%s), the previous success is kept", job.AddonSlug, job.VersionLabel, entry.ErrorCode), nil
		}
	}

	logPath := filepath.Join(folder, base+".log")
	exportPath := filepath.Join(folder, base+".json")
	if entry.Status == "succeeded" {
		if err := writeJSON(exportPath, export); err != nil {
			return "", err
		}
		_ = os.Remove(logPath)
	} else {
		_ = os.Remove(exportPath)
		if log, err := readLimited(filepath.Join(dir, "server.log"), 1<<20); err == nil && len(log) > 0 {
			if err := os.MkdirAll(folder, 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(logPath, []byte(tail(string(log), maxLogBytes)), 0o644); err != nil {
				return "", err
			}
			entry.Log = base + ".log"
		}
	}

	if position >= 0 {
		index.Versions[position] = entry
	} else {
		index.Versions = append(index.Versions, entry)
	}
	index.FormatVersion = FormatVersion
	index.ID, index.Slug, index.Name = job.AddonID, job.AddonSlug, clean(job.AddonName, 128)
	index.sortVersions()
	if err := writeJSON(filepath.Join(folder, "index.json"), index); err != nil {
		return "", err
	}
	if entry.Status == "succeeded" {
		return fmt.Sprintf("%s %s: %d syntaxes", job.AddonSlug, job.VersionLabel, entry.SyntaxCount), nil
	}
	return fmt.Sprintf("%s %s: failed (%s)", job.AddonSlug, job.VersionLabel, entry.ErrorCode), nil
}

// writeRootIndex rebuilds index.json from every addons/<slug>/index.json.
func writeRootIndex(data string) error {
	root := RootIndex{FormatVersion: FormatVersion, Addons: []RootAddon{}}
	paths, _ := filepath.Glob(filepath.Join(data, "addons", "*", "index.json"))
	sort.Strings(paths)
	for _, path := range paths {
		var index AddonIndex
		if err := readJSON(path, &index); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		line := RootAddon{ID: index.ID, Slug: index.Slug, Name: index.Name}
		if len(index.Versions) > 0 {
			last := index.Versions[0]
			line.Last = &RootLast{Label: last.Label, Status: last.Status, ErrorCode: last.ErrorCode}
		}
		for _, v := range index.Versions {
			if v.Label == index.Latest && v.Status == "succeeded" {
				line.Latest = &RootLatest{
					Label:       v.Label,
					File:        "addons/" + index.Slug + "/" + v.File,
					SyntaxCount: v.SyntaxCount,
					JarSHA256:   v.JarSHA256,
					ExtractedAt: v.ExtractedAt,
				}
				break
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
