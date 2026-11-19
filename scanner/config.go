package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config of the extraction step, from the environment (see the README).
type Config struct {
	AllowedHosts []string
	MaxJarBytes  int64
	Timeout      time.Duration
	Runtime      string
	Memory       string
	CPUs         string
	Docker       string
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// DefaultAllowedHosts are where GitHub serves release assets (github.com redirects to the others).
const DefaultAllowedHosts = "github.com,objects.githubusercontent.com,release-assets.githubusercontent.com"

func LoadConfig() (Config, error) {
	cfg := Config{
		AllowedHosts: splitList(env("SCANNER_ALLOWED_HOSTS", DefaultAllowedHosts)),
		Runtime:      env("SCANNER_DOCKER_RUNTIME", ""),
		Memory:       env("SCANNER_MEMORY", "1536m"),
		CPUs:         env("SCANNER_CPUS", "2"),
		Docker:       env("SCANNER_DOCKER", "docker"),
	}
	var errs []error
	maxMB, err := strconv.Atoi(env("SCANNER_MAX_JAR_MB", "64"))
	if err != nil || maxMB <= 0 {
		errs = append(errs, fmt.Errorf("SCANNER_MAX_JAR_MB: not a positive number"))
	}
	cfg.MaxJarBytes = int64(maxMB) << 20
	if cfg.Timeout, err = time.ParseDuration(env("SCANNER_TIMEOUT", "4m")); err != nil {
		errs = append(errs, fmt.Errorf("SCANNER_TIMEOUT: %w", err))
	}
	return cfg, errors.Join(errs...)
}
