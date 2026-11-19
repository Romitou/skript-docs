package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Image is an extractor image (extractor/image) and the versions its server runs.
type Image struct {
	Minecraft string
	Skript    string
	Ref       string
}

// ImagesFile is extractor/images.json, also read by the images workflow, which builds and
// publishes one image per entry.
type ImagesFile struct {
	Repository string `json:"repository"`
	Images     []struct {
		Minecraft string `json:"minecraft"`
		Java      string `json:"java"`
		Skript    string `json:"skript"`
	} `json:"images"`
}

// ImageTag is the tag of an image: "26.3-skript2.17.1".
func ImageTag(minecraft, skript string) string {
	return minecraft + "-skript" + skript
}

// LoadImages reads images.json, oldest Minecraft first; images are named <repository>:<tag>. repository overrides the file's
// (SCANNER_IMAGE_REPOSITORY, to test with locally built images).
func LoadImages(path, repository string) ([]Image, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file ImagesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if repository == "" {
		repository = file.Repository
	}
	var images []Image
	for _, spec := range file.Images {
		if parseVersion(spec.Minecraft) == nil || spec.Skript == "" {
			return nil, fmt.Errorf("%s: invalid image %+v", path, spec)
		}
		images = append(images, Image{
			Minecraft: spec.Minecraft,
			Skript:    spec.Skript,
			Ref:       repository + ":" + ImageTag(spec.Minecraft, spec.Skript),
		})
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("%s: no image", path)
	}
	sort.SliceStable(images, func(i, j int) bool {
		return compareVersions(images[i].Minecraft, images[j].Minecraft) < 0
	})
	return images, nil
}

// Candidates lists the images to try for a plugin's api-version, best first: the oldest server
// the plugin accepts (Paper keeps older plugins working, but removes APIs over time), then the
// newer ones. Without an api-version (legacy plugin), the oldest image comes first. A plugin
// asking for a newer API than every image gets none.
func Candidates(images []Image, apiVersion string) []Image {
	if strings.TrimSpace(apiVersion) == "" || parseVersion(apiVersion) == nil {
		return append([]Image(nil), images...)
	}
	var out []Image
	for _, image := range images {
		if versionAtLeast(image.Minecraft, apiVersion) {
			out = append(out, image)
		}
	}
	return out
}

// versionAtLeast reports whether server satisfies api: "1.21.11" satisfies "1.21", "26.3"
// satisfies "26.1" and "1.21", "1.21.11" does not satisfy "26.1".
func versionAtLeast(server, api string) bool {
	s, a := parseVersion(server), parseVersion(api)
	for i := 0; i < len(a); i++ {
		var sv int
		if i < len(s) {
			sv = s[i]
		}
		if sv != a[i] {
			return sv > a[i]
		}
	}
	return true
}

func compareVersions(a, b string) int {
	va, vb := parseVersion(a), parseVersion(b)
	for i := 0; i < len(va) || i < len(vb); i++ {
		var x, y int
		if i < len(va) {
			x = va[i]
		}
		if i < len(vb) {
			y = vb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) []int {
	v = strings.Trim(strings.TrimSpace(v), `"'`)
	if v == "" {
		return nil
	}
	var parts []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		parts = append(parts, n)
	}
	return parts
}
