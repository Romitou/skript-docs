package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadImagesAndCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "images.json")
	os.WriteFile(path, []byte(`{"repository":"ghcr.io/x/y","images":[
		{"minecraft":"26.3","java":"25","skript":"2.17.1"},{"minecraft":"1.21.11","java":"21","skript":"2.17.1"}]}`), 0o644)
	images, err := LoadImages(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if images[0].Ref != "ghcr.io/x/y:1.21.11-skript2.17.1" || images[1].Ref != "ghcr.io/x/y:26.3-skript2.17.1" {
		t.Fatalf("unexpected images %+v", images)
	}
	if local, _ := LoadImages(path, "skmc-extractor"); local[1].Ref != "skmc-extractor:26.3-skript2.17.1" {
		t.Errorf("repository override: %+v", local)
	}
	refs := func(api string) []string {
		var out []string
		for _, image := range Candidates(images, api) {
			out = append(out, image.Minecraft)
		}
		return out
	}
	cases := map[string][]string{
		"": {"1.21.11", "26.3"}, "1.13": {"1.21.11", "26.3"}, "1.21": {"1.21.11", "26.3"},
		"26.1": {"26.3"}, "26.3": {"26.3"}, "27": nil,
	}
	for api, want := range cases {
		if got := refs(api); !slices.Equal(got, want) {
			t.Errorf("api-version %q: got %v, want %v", api, got, want)
		}
	}
}

func TestCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.json")
	sha := strings.Repeat("a", 64)
	os.WriteFile(path, []byte(fmt.Sprintf(`{"plugins":[
		{"name":"Vault","url":"https://example.com/vault.jar","sha256":"%[1]s"},
		{"name":"SkBee","url":"https://example.com/skbee.jar","sha256":"%[1]s"},
		{"name":"NBTAPI","url":"https://example.com/nbt.jar","sha256":"%[1]s"},
		{"name":"ProtocolLibFork","provides":["ProtocolLib"],"url":"https://example.com/plf.jar","sha256":"%[1]s"},
		{"name":"Broken","url":"https://example.com/broken.jar","sha256":"%[1]s"}]}`, sha)), 0o644)
	catalog, err := LoadCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := map[string]string{
		"Vault": "name: Vault\n", "SkBee": "name: SkBee\ndepend: [Skript, NBTAPI]\n",
		"NBTAPI": "name: NBTAPI\n", "ProtocolLibFork": "name: ProtocolLibFork\nprovides: [ProtocolLib]\n",
	}
	var fetched []string
	fetch := func(ctx context.Context, entry CatalogEntry, dir string) (string, error) {
		fetched = append(fetched, entry.Name)
		if entry.Name == "Broken" {
			return "", fail("DEPENDENCY_CHECKSUM", "mismatch")
		}
		jar := filepath.Join(dir, entry.Name+".jar")
		writeJar(t, jar, descriptors[entry.Name])
		return jar, nil
	}
	res, err := catalog.Resolve(context.Background(), PluginInfo{Name: "MyAddon", Depend: []string{"Skript", "SkBee"},
		SoftDepend: []string{"Vault", "WorldGuard", "ProtocolLib", "Broken"}}, dir, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Installed, []string{"SkBee", "Vault", "ProtocolLibFork", "NBTAPI"}) {
		t.Errorf("installed %v", res.Installed)
	}
	if !slices.Equal(res.MissingSoft, []string{"WorldGuard", "Broken (download failed)"}) || len(res.MissingHard) != 0 {
		t.Errorf("missing %v %v", res.MissingHard, res.MissingSoft)
	}
	if _, err := catalog.Resolve(context.Background(), PluginInfo{Name: "X", Depend: []string{"Broken"}}, dir, fetch); !isFailure(err, "DEPENDENCY_CHECKSUM") {
		t.Errorf("a hard dependency that cannot be fetched must fail, got %v", err)
	}
	res, _ = catalog.Resolve(context.Background(), PluginInfo{Name: "Y", Depend: []string{"Citizens"}}, dir, fetch)
	if !slices.Equal(res.MissingHard, []string{"Citizens"}) {
		t.Errorf("missing hard %v", res.MissingHard)
	}

	os.WriteFile(path, []byte(`{"plugins":[{"name":"Vault","url":"http://example.com/v.jar","sha256":"`+sha+`"}]}`), 0o644)
	if _, err := LoadCatalog(path); err == nil {
		t.Error("an http link must be refused")
	}
	if c, err := LoadCatalog(filepath.Join(dir, "missing.json")); err != nil || len(c.Plugins) != 0 {
		t.Error("a missing deps.json is an empty catalog")
	}
}

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{"3.0.0": "3.0.0", "1.0 beta/2": "1.0_beta_2", "../x": "_x", "": "_", "..": "_", "v2+build": "v2+build"} {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func export(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	e := map[string]any{
		"format": "skmc-syntax-export", "formatVersion": 1,
		"source": map[string]any{"plugin": "MongoSK", "version": "3.0.0", "server": "SENTINEL_UNKNOWN"},
		"expressions": []any{map[string]any{"id": "ExprMongoKeys", "name": "Mongo document keys", "documented": true,
			"patterns": []string{"mongo keys of %mongoskdocument%"}, "examples": []string{"\tindented"}, "unknownField": "SENTINEL_UNKNOWN"}},
		"types":    []any{map[string]any{"id": "mongoskdocument", "name": "MongoSK Document", "patterns": []string{"mongoskdocument"}}},
		"warnings": []string{"from the exporter"},
		"stats":    map[string]any{"total": 99, "undocumented": 0},
	}
	if mutate != nil {
		mutate(e)
	}
	raw, _ := json.Marshal(e)
	return raw
}

func TestSanitize(t *testing.T) {
	e, err := Sanitize(export(t, func(e map[string]any) {
		e["effects"] = []any{
			map[string]any{"id": strings.Repeat("a", 100), "name": "Long\x00", "patterns": []string{"x\x07y"}},
			map[string]any{"id": "", "patterns": []string{"x"}},
			map[string]any{"id": "mongoskdocument", "patterns": []string{"y"}},
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if e.Stats.Total != 4 || len(e.Effects) != 2 || len(e.Effects[0].ID) > 64 || e.Effects[0].Name != "Long" || e.Effects[0].Patterns[0] != "xy" {
		t.Fatalf("not sanitized: %+v", e)
	}
	if e.Types[0].ID != "mongoskdocument_type" || e.Expressions[0].Examples[0] != "\tindented" {
		t.Errorf("clash or examples: %+v %+v", e.Types, e.Expressions)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "SENTINEL_UNKNOWN") {
		t.Errorf("unknown fields must be dropped: %s", raw)
	}
	for name, bad := range map[string][]byte{
		"not json": []byte("{"), "other format": export(t, func(e map[string]any) { e["format"] = "x" }),
		"too many": export(t, func(e map[string]any) {
			items := make([]any, MaxSyntaxes+1)
			for i := range items {
				items[i] = map[string]any{"id": "x", "patterns": []string{"x"}}
			}
			e["effects"] = items
		}),
	} {
		if _, err := Sanitize(bad); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
