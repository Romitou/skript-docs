package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// GitHubAPI is the REST API root; replaced by a local server in tests.
var GitHubAPI = "https://api.github.com"

type Release struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

type Asset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
	UpdatedAt   string `json:"updated_at"`
}

var ErrRepoNotFound = errors.New("repository not found or private")

type GitHub struct {
	Token string
	HTTP  *http.Client
}

func NewGitHub() *GitHub {
	return &GitHub{Token: strings.TrimSpace(os.Getenv("GITHUB_TOKEN")), HTTP: &http.Client{Timeout: time.Minute}}
}

func (g *GitHub) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GitHubAPI+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "skript-docs-scanner")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrRepoNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, body)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(into)
}

// Releases lists a repository's most recent releases, newest first (GitHub's order).
func (g *GitHub) Releases(ctx context.Context, repo string) ([]Release, error) {
	var releases []Release
	return releases, g.get(ctx, "/repos/"+repo+"/releases?per_page=30", &releases)
}

// RepoExists checks a repository is public (validate, for pull requests).
func (g *GitHub) RepoExists(ctx context.Context, repo string) error {
	var out struct {
		Private bool `json:"private"`
	}
	if err := g.get(ctx, "/repos/"+repo, &out); err != nil {
		return err
	}
	if out.Private {
		return ErrRepoNotFound
	}
	return nil
}

var excludedJar = regexp.MustCompile(`(?i)-(sources|javadoc)\.jar$`)

// LatestRelease is the release to document: the newest published one (drafts never, pre-releases
// only when the entry asks for them) that has a usable jar.
func LatestRelease(addon TrackedAddon, releases []Release) (Release, Asset, error) {
	for _, rel := range releases {
		if rel.Draft || (rel.Prerelease && !addon.Prereleases) {
			continue
		}
		asset, err := PickAsset(addon, rel)
		if err != nil {
			// The newest release decides: an older one is not "the latest".
			return rel, Asset{}, err
		}
		return rel, asset, nil
	}
	return Release{}, Asset{}, errors.New("no published release")
}

// PickAsset chooses the plugin jar of a release: the assets matching the entry's asset pattern,
// or else every .jar but -sources/-javadoc; among several, the largest (usually the shaded one).
// Only files of the repository's own releases are accepted.
func PickAsset(addon TrackedAddon, rel Release) (Asset, error) {
	var pattern *regexp.Regexp
	if addon.Asset != "" {
		pattern = regexp.MustCompile(addon.Asset) // checked by LoadTracked
	}
	prefix := "https://github.com/" + addon.Repo + "/releases/download/"
	var best *Asset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		if !strings.HasPrefix(a.DownloadURL, prefix) {
			continue
		}
		if pattern != nil {
			if !pattern.MatchString(a.Name) {
				continue
			}
		} else if !strings.HasSuffix(strings.ToLower(a.Name), ".jar") || excludedJar.MatchString(a.Name) {
			continue
		}
		if best == nil || a.Size > best.Size {
			best = a
		}
	}
	if best == nil {
		return Asset{}, fmt.Errorf("release %s has no jar", rel.TagName)
	}
	return *best, nil
}
