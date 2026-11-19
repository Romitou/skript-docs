package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The add-ons and versions come from Skript-MC's public API: the versions there are moderated
// (validated links, versions removed by a maintainer stay removed) and synced from the GitHub
// releases already. This repository only reads them.

type APIAddon struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type APIVersion struct {
	ID          uint      `json:"id"`
	Label       string    `json:"label"`
	DownloadURL string    `json:"downloadUrl"`
	ReleaseType string    `json:"releaseType"`
	CreatedAt   time.Time `json:"createdAt"`
}

type SkriptMC struct {
	Base string
	HTTP *http.Client
}

func NewSkriptMC(base string) *SkriptMC {
	return &SkriptMC{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: time.Minute}}
}

func (s *SkriptMC) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "skript-docs-scanner")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, body)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(into)
}

func (s *SkriptMC) Addons(ctx context.Context) ([]APIAddon, error) {
	var addons []APIAddon
	return addons, s.get(ctx, "/docs/addons", &addons)
}

// Versions lists an addon's public (validated) versions, newest first.
func (s *SkriptMC) Versions(ctx context.Context, addonID uint) ([]APIVersion, error) {
	var versions []APIVersion
	return versions, s.get(ctx, fmt.Sprintf("/docs/addons/%d/versions", addonID), &versions)
}
