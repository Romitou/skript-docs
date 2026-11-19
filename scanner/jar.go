package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Failure is an extraction failure with a stable code (README § Error codes), recorded in the
// version's entry.
type Failure struct {
	Code    string
	Message string
}

func (f *Failure) Error() string { return f.Code + ": " + f.Message }

func fail(code, format string, args ...any) *Failure {
	return &Failure{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Download fetches a release jar into path. Only https URLs on an allowed host are followed,
// redirects included, and the body is cut at maxBytes. Returns the jar's sha256.
func Download(ctx context.Context, rawURL, path string, allowedHosts []string, maxBytes int64) (string, error) {
	if err := checkURL(rawURL, allowedHosts); err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return checkURL(req.URL.String(), allowedHosts)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fail("DOWNLOAD_FAILED", "%v", err)
	}
	req.Header.Set("User-Agent", "skmc-syntax-extractor")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		var f *Failure
		if errors.As(err, &f) {
			return "", f
		}
		return "", fail("DOWNLOAD_FAILED", "%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fail("DOWNLOAD_FAILED", "HTTP %d for %s", resp.StatusCode, rawURL)
	}
	if resp.ContentLength > maxBytes {
		return "", fail("JAR_TOO_LARGE", "%d bytes, at most %d", resp.ContentLength, maxBytes)
	}
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", fail("DOWNLOAD_FAILED", "%v", err)
	}
	if n > maxBytes {
		return "", fail("JAR_TOO_LARGE", "more than %d bytes", maxBytes)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checkURL(rawURL string, allowedHosts []string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fail("HOST_NOT_ALLOWED", "invalid URL %q", rawURL)
	}
	if u.Scheme != "https" {
		return fail("HOST_NOT_ALLOWED", "%s is not https", rawURL)
	}
	host := strings.ToLower(u.Hostname())
	if !slices.Contains(allowedHosts, host) {
		return fail("HOST_NOT_ALLOWED", "%s is not an allowed download host", host)
	}
	return nil
}

// PluginInfo is what the scanner needs from a plugin's plugin.yml (or paper-plugin.yml).
type PluginInfo struct {
	Name       string
	Version    string
	APIVersion string
	Depend     []string
	SoftDepend []string
	Provides   []string
}

// DependsOnSkript tells a Skript add-on from any other plugin.
func (p PluginInfo) DependsOnSkript() bool {
	for _, dep := range append(append([]string{}, p.Depend...), p.SoftDepend...) {
		if strings.EqualFold(dep, "Skript") {
			return true
		}
	}
	return false
}

type bukkitYML struct {
	Name       string   `yaml:"name"`
	Version    any      `yaml:"version"`
	APIVersion any      `yaml:"api-version"`
	Depend     []string `yaml:"depend"`
	SoftDepend []string `yaml:"softdepend"`
	Provides   []string `yaml:"provides"`
	// paper-plugin.yml: dependencies.server.<name>.required (true by default)
	Dependencies struct {
		Server map[string]struct {
			Required *bool `yaml:"required"`
		} `yaml:"server"`
	} `yaml:"dependencies"`
}

const maxDescriptorBytes = 1 << 20

// InspectJar reads the plugin descriptor of a jar. plugin.yml wins over paper-plugin.yml, as on
// Paper when a jar has both... for the dependencies that matter here they agree anyway.
func InspectJar(path string) (PluginInfo, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return PluginInfo{}, fail("INVALID_JAR", "not a jar: %v", err)
	}
	defer reader.Close()
	var descriptor *zip.File
	for _, name := range []string{"plugin.yml", "paper-plugin.yml"} {
		for _, f := range reader.File {
			if f.Name == name {
				descriptor = f
				break
			}
		}
		if descriptor != nil {
			break
		}
	}
	if descriptor == nil {
		return PluginInfo{}, fail("NO_PLUGIN_YML", "the jar has no plugin.yml: is it a Bukkit/Paper plugin?")
	}
	rc, err := descriptor.Open()
	if err != nil {
		return PluginInfo{}, fail("INVALID_JAR", "%v", err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxDescriptorBytes))
	if err != nil {
		return PluginInfo{}, fail("INVALID_JAR", "%v", err)
	}
	return ParseDescriptor(raw)
}

func ParseDescriptor(raw []byte) (PluginInfo, error) {
	var y bukkitYML
	if err := yaml.Unmarshal(raw, &y); err != nil {
		return PluginInfo{}, fail("INVALID_JAR", "unreadable plugin.yml: %v", err)
	}
	info := PluginInfo{
		Name:       strings.TrimSpace(y.Name),
		Version:    scalar(y.Version),
		APIVersion: scalar(y.APIVersion),
		Depend:     y.Depend,
		SoftDepend: y.SoftDepend,
		Provides:   y.Provides,
	}
	for name, dep := range y.Dependencies.Server {
		if dep.Required == nil || *dep.Required {
			info.Depend = append(info.Depend, name)
		} else {
			info.SoftDepend = append(info.SoftDepend, name)
		}
	}
	slices.Sort(info.Depend)
	info.Depend = slices.Compact(info.Depend)
	slices.Sort(info.SoftDepend)
	info.SoftDepend = slices.Compact(info.SoftDepend)
	if info.Name == "" {
		return PluginInfo{}, fail("NO_PLUGIN_YML", "plugin.yml has no name")
	}
	return info, nil
}

// scalar renders a YAML value typed by the parser (1.21 is a float, 3 an int) as written.
func scalar(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", value), "0"), ".")
	default:
		return fmt.Sprint(value)
	}
}
