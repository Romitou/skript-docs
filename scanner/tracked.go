package main

import (
	"fmt"
	"regexp"
	"strings"
)

// TrackedAddon is an entry of addons.json: a GitHub repository whose latest release gets its
// syntaxes generated. Added and removed by pull request.
type TrackedAddon struct {
	Slug        string `json:"slug"`                  // folder under addons/, and the key consumers match on
	Name        string `json:"name"`                  // display name
	Repo        string `json:"repo"`                  // owner/name on github.com
	Asset       string `json:"asset,omitempty"`       // regexp picking the jar when a release has several
	Prereleases bool   `json:"prereleases,omitempty"` // also document pre-releases
}

type TrackedFile struct {
	Addons []TrackedAddon `json:"addons"`
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)

// LoadTracked reads and checks addons.json: valid slugs and repositories, no slug twice, asset
// patterns that compile.
func LoadTracked(path string) ([]TrackedAddon, error) {
	var file TrackedFile
	if err := readJSON(path, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	var problems []string
	for i, a := range file.Addons {
		where := fmt.Sprintf("entry %d (%q)", i, a.Slug)
		if !slugPattern.MatchString(a.Slug) {
			problems = append(problems, where+": slug must be lowercase letters, digits and dashes")
		}
		if seen[a.Slug] {
			problems = append(problems, where+": slug listed twice")
		}
		seen[a.Slug] = true
		if strings.TrimSpace(a.Name) == "" {
			problems = append(problems, where+": name is empty")
		}
		if !repoPattern.MatchString(a.Repo) {
			problems = append(problems, where+": repo must be owner/name")
		}
		if a.Asset != "" {
			if _, err := regexp.Compile(a.Asset); err != nil {
				problems = append(problems, where+": invalid asset pattern: "+err.Error())
			}
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s:\n  %s", path, strings.Join(problems, "\n  "))
	}
	return file.Addons, nil
}
