package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Result is what `scanner extract` writes in its output directory (result.json), next to the
// export (export.json) and the end of the server's log (server.log). `scanner publish` treats
// all three as untrusted: the job that wrote them ran the add-on's code.
type Result struct {
	Job          Job        `json:"job"`
	Status       string     `json:"status"` // "succeeded" | "failed"
	ErrorCode    string     `json:"errorCode,omitempty"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	JarSHA256    string     `json:"jarSha256,omitempty"`
	Plugin       PluginMeta `json:"plugin"`
	Image        string     `json:"image,omitempty"`
	Minecraft    string     `json:"minecraft,omitempty"`
	Skript       string     `json:"skript,omitempty"`
	Dependencies []string   `json:"dependencies"`
	Warnings     []string   `json:"warnings"`
	DurationMs   int64      `json:"durationMs"`
	ExtractedAt  time.Time  `json:"extractedAt"`
}

// Extract runs one job and writes its result in out. Only an unwritable output is an error:
// every extraction failure is a result, with a code.
func Extract(ctx context.Context, cfg Config, images []Image, catalog Catalog, job Job, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	start := time.Now()
	result := Result{Job: job, Status: "failed", Dependencies: []string{}, Warnings: []string{}}
	log, export, err := extract(ctx, cfg, images, catalog, job, &result)
	result.DurationMs = time.Since(start).Milliseconds()
	result.ExtractedAt = time.Now().UTC().Truncate(time.Second)
	if err != nil {
		var f *Failure
		if !errors.As(err, &f) {
			f = &Failure{Code: "SCANNER_ERROR", Message: err.Error()}
		}
		result.ErrorCode, result.ErrorMessage = f.Code, truncate(f.Message, 2000)
	} else {
		result.Status = "succeeded"
		if err := os.WriteFile(filepath.Join(out, "export.json"), export, 0o644); err != nil {
			return err
		}
	}
	if log != "" {
		if err := os.WriteFile(filepath.Join(out, "server.log"), []byte(log), 0o644); err != nil {
			return err
		}
	}
	raw, _ := json.MarshalIndent(result, "", "  ")
	return os.WriteFile(filepath.Join(out, "result.json"), raw, 0o644)
}

func extract(ctx context.Context, cfg Config, images []Image, catalog Catalog, job Job, result *Result) (string, []byte, error) {
	dir, err := os.MkdirTemp("", "skript-docs-job-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	inputDir := filepath.Join(dir, "input")
	depsDir := filepath.Join(dir, "deps")
	for _, d := range []string{inputDir, depsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", nil, err
		}
	}
	// The container user (uid 1000) reads the input and writes the output.
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", nil, err
	}

	jarPath := filepath.Join(inputDir, "addon.jar")
	sum, err := Download(ctx, job.JarURL, jarPath, cfg.AllowedHosts, cfg.MaxJarBytes)
	if err != nil {
		return "", nil, err
	}
	result.JarSHA256 = sum
	plugin, err := InspectJar(jarPath)
	if err != nil {
		return "", nil, err
	}
	result.Plugin = PluginMeta{Name: plugin.Name, Version: plugin.Version, APIVersion: plugin.APIVersion}
	if strings.EqualFold(plugin.Name, "Skript") {
		return "", nil, fail("SKRIPT_ITSELF", "this jar is Skript itself, whose documentation comes with each Skript release")
	}
	if !plugin.DependsOnSkript() {
		return "", nil, fail("NOT_A_SKRIPT_ADDON", "%s does not depend on Skript (plugin.yml depend/softdepend)", plugin.Name)
	}

	deps, err := catalog.Resolve(ctx, plugin, depsDir, FetchEntry(cfg.MaxJarBytes))
	if err != nil {
		return "", nil, err
	}
	if len(deps.MissingHard) > 0 {
		return "", nil, fail("MISSING_DEPENDENCY", "%s requires %s, absent from deps.json", plugin.Name, strings.Join(deps.MissingHard, ", "))
	}
	if len(deps.MissingSoft) > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"Optional dependencies absent from deps.json: %s. The syntaxes %s only registers when they are installed are missing.",
			strings.Join(deps.MissingSoft, ", "), plugin.Name))
	}
	for _, depJar := range deps.Jars {
		if err := copyFile(depJar, filepath.Join(inputDir, filepath.Base(depJar))); err != nil {
			return "", nil, err
		}
	}
	result.Dependencies = append(result.Dependencies, deps.Installed...)

	candidates := Candidates(images, plugin.APIVersion)
	if len(candidates) == 0 {
		return "", nil, fail("NO_COMPATIBLE_IMAGE", "%s needs Minecraft %s or newer, no extractor image is that recent", plugin.Name, plugin.APIVersion)
	}
	var lastErr error
	var log string
	for i, image := range candidates {
		if i >= 2 {
			break // the closest image and the next one: beyond, the plugin is broken, not old
		}
		outputDir := filepath.Join(dir, fmt.Sprintf("output-%d", i))
		if err := os.MkdirAll(outputDir, 0o777); err != nil {
			return "", nil, err
		}
		if err := os.Chmod(outputDir, 0o777); err != nil {
			return "", nil, err
		}
		result.Image, result.Minecraft, result.Skript = image.Ref, image.Minecraft, image.Skript
		run, err := RunContainer(ctx, cfg, image, inputDir, outputDir, plugin.Name)
		log = run.Log
		if err == nil {
			return log, run.Export, nil
		}
		lastErr = err
		var f *Failure
		// Only a plugin the server refused (api-version too new: not even loaded) or that did not
		// enable may work on another server version.
		if !errors.As(err, &f) || (f.Code != "TARGET_NOT_ENABLED" && f.Code != "TARGET_NOT_FOUND") {
			return log, nil, err
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s did not load on Minecraft %s (%s)", plugin.Name, image.Minecraft, f.Code))
	}
	return log, nil, lastErr
}

func truncate(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && strings.ToValidUTF8(cut, "�") != cut {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}
