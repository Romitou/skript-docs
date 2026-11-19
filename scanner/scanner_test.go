package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestLatestStableAndDecide(t *testing.T) {
	now := time.Now()
	versions := []APIVersion{
		{Label: "3.1.0-beta", ReleaseType: "prerelease", CreatedAt: now, DownloadURL: "https://github.com/o/r/releases/download/b/A.jar"},
		{Label: "3.0.1", ReleaseType: "release", CreatedAt: now.Add(-time.Hour), DownloadURL: "https://github.com/o/r/releases/tag/3.0.1"},
		{Label: "3.0.0", ReleaseType: "release", CreatedAt: now.Add(-2 * time.Hour), DownloadURL: "https://github.com/o/r/releases/download/3.0.0/A.jar"},
		{Label: "2.0.0", ReleaseType: "release", CreatedAt: now.Add(-3 * time.Hour), DownloadURL: "https://github.com/o/r/releases/download/2.0.0/A.jar"},
	}
	latest, ok := LatestStable(versions)
	if !ok || latest.Label != "3.0.0" {
		t.Fatalf("latest stable with a jar: got %+v", latest)
	}
	opts := PlanOptions{Toolchain: "t1", DepsHash: "d1"}
	entry := VersionEntry{Label: "3.0.0", JarURL: latest.DownloadURL, Status: "succeeded", Toolchain: "t1", DepsHash: "d1"}
	index := AddonIndex{Versions: []VersionEntry{entry}}
	check := func(what string, index AddonIndex, opts PlanOptions, want bool) {
		t.Helper()
		if _, got := Decide(index, latest, opts); got != want {
			t.Errorf("%s: got %v, want %v", what, got, want)
		}
	}
	check("never tried", AddonIndex{}, opts, true)
	check("succeeded", index, opts, false)
	check("succeeded, forced", index, PlanOptions{Toolchain: "t1", DepsHash: "d1", Force: true}, true)
	check("succeeded, new images", index, PlanOptions{Toolchain: "t2", DepsHash: "d1"}, false)
	failed := entry
	failed.Status = "failed"
	failedIndex := AddonIndex{Versions: []VersionEntry{failed}}
	check("failed, nothing changed", failedIndex, opts, false)
	check("failed, new deps", failedIndex, PlanOptions{Toolchain: "t1", DepsHash: "d2"}, true)
	check("failed, new images", failedIndex, PlanOptions{Toolchain: "t2", DepsHash: "d1"}, true)
	moved := entry
	moved.JarURL = "https://github.com/o/r/releases/download/3.0.0/Other.jar"
	check("same label, other jar", AddonIndex{Versions: []VersionEntry{moved}}, opts, true)
}

func TestMakePlan(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs/addons":
			json.NewEncoder(w).Encode([]APIAddon{{ID: 1, Name: "Skript", Slug: "skript"}, {ID: 2, Name: "MongoSK", Slug: "mongosk"},
				{ID: 3, Name: "Done", Slug: "done"}, {ID: 4, Name: "Bad", Slug: "../bad"}, {ID: 5, Name: "Down", Slug: "down"}})
		case "/docs/addons/1/versions", "/docs/addons/2/versions", "/docs/addons/3/versions", "/docs/addons/4/versions":
			json.NewEncoder(w).Encode([]APIVersion{{ID: 9, Label: "3.0.0", ReleaseType: "release", CreatedAt: time.Now(),
				DownloadURL: "https://github.com/o/r/releases/download/3.0.0/A.jar"}})
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer api.Close()
	data := t.TempDir()
	writeJSON(filepath.Join(addonDir(data, "done"), "index.json"), AddonIndex{Slug: "done", Versions: []VersionEntry{{
		Label: "3.0.0", JarURL: "https://github.com/o/r/releases/download/3.0.0/A.jar", Status: "succeeded"}}})

	plan, err := MakePlan(context.Background(), NewSkriptMC(api.URL), PlanOptions{Data: data, Toolchain: "t", DepsHash: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Jobs) != 1 || plan.Jobs[0].AddonSlug != "mongosk" || plan.Jobs[0].VersionID != 9 || plan.Jobs[0].Reason != "new version" {
		t.Fatalf("unexpected plan %+v", plan.Jobs)
	}
	excludedPath := filepath.Join(data, "excluded.json")
	os.WriteFile(excludedPath, []byte(`{"addons":["mongosk"]}`), 0o644)
	excluded, err := LoadExcluded(excludedPath)
	if err != nil {
		t.Fatal(err)
	}
	if plan, _ := MakePlan(context.Background(), NewSkriptMC(api.URL), PlanOptions{Data: data, Excluded: excluded}); len(plan.Jobs) != 0 {
		t.Fatalf("an excluded add-on must not be planned: %+v", plan.Jobs)
	}
	plan, _ = MakePlan(context.Background(), NewSkriptMC(api.URL), PlanOptions{Data: data, Only: "done", Force: true})
	if len(plan.Jobs) != 1 || plan.Jobs[0].AddonSlug != "done" {
		t.Fatalf("--addon done --force: %+v", plan.Jobs)
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

func TestPublish(t *testing.T) {
	data := t.TempDir()
	results := t.TempDir()
	date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	job := Job{AddonID: 2, AddonSlug: "mongosk", AddonName: "MongoSK", VersionID: 9, VersionLabel: "3.0.0",
		VersionDate: date, JarURL: "https://github.com/o/r/releases/download/3.0.0/A.jar"}
	plan := Plan{Toolchain: "t", DepsHash: "d", Jobs: []Job{job}}
	write := func(dir string, result Result, exportRaw []byte, log string) {
		os.MkdirAll(dir, 0o755)
		raw, _ := json.Marshal(result)
		os.WriteFile(filepath.Join(dir, "result.json"), raw, 0o644)
		if exportRaw != nil {
			os.WriteFile(filepath.Join(dir, "export.json"), exportRaw, 0o644)
		}
		if log != "" {
			os.WriteFile(filepath.Join(dir, "server.log"), []byte(log), 0o644)
		}
	}
	// A forged result for a job nobody planned, and one claiming another add-on's identity.
	forged := job
	forged.AddonSlug = "other"
	write(filepath.Join(results, "result-0"), Result{Job: forged, Status: "succeeded"}, export(t, nil), "")
	write(filepath.Join(results, "result-1"), Result{Job: job, Status: "succeeded", JarSHA256: strings.Repeat("b", 64),
		Plugin: PluginMeta{Name: "MongoSK", Version: "3.0.0", APIVersion: "26.1"}, Image: "img", Minecraft: "26.3", Skript: "2.17.1",
		Warnings: []string{"w"}}, export(t, nil), "")

	summary, err := Publish(data, plan, results, date)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(summary, "\n")
	if !strings.Contains(joined, "mongosk 3.0.0: 2 syntaxes") || !strings.Contains(joined, "unplanned") {
		t.Fatalf("summary: %s", joined)
	}
	if _, err := os.Stat(filepath.Join(data, "addons", "other")); !os.IsNotExist(err) {
		t.Fatal("an unplanned result must not be written")
	}
	var index AddonIndex
	readJSON(filepath.Join(data, "addons", "mongosk", "index.json"), &index)
	if index.Latest != "3.0.0" || index.ID != 2 || len(index.Versions) != 1 || index.Versions[0].File != "3.0.0.json" ||
		index.Versions[0].Toolchain != "t" || index.Versions[0].SyntaxCount != 2 {
		t.Fatalf("addon index %+v", index)
	}
	var root RootIndex
	readJSON(filepath.Join(data, "index.json"), &root)
	if len(root.Addons) != 1 || root.Addons[0].Latest == nil || root.Addons[0].Latest.File != "addons/mongosk/3.0.0.json" {
		t.Fatalf("root index %+v", root)
	}
	var published Export
	readJSON(filepath.Join(data, "addons", "mongosk", "3.0.0.json"), &published)
	if published.Stats.Total != 2 {
		t.Errorf("published export %+v", published.Stats)
	}

	// A forced retry that fails keeps the success.
	os.RemoveAll(results)
	write(filepath.Join(results, "r"), Result{Job: job, Status: "failed", ErrorCode: "TIMEOUT"}, nil, "log")
	summary, _ = Publish(data, plan, results, date)
	if !strings.Contains(strings.Join(summary, "\n"), "previous success is kept") {
		t.Fatalf("summary: %v", summary)
	}

	// A new version that fails: recorded with its log and a sanitized code; Latest stays.
	newer := job
	newer.VersionLabel, newer.VersionDate, newer.JarURL = "3.1.0", date.Add(time.Hour), "https://github.com/o/r/releases/download/3.1.0/A.jar"
	os.RemoveAll(results)
	write(filepath.Join(results, "r"), Result{Job: newer, Status: "failed", ErrorCode: "bad code; rm -rf", ErrorMessage: "x"}, nil, "the end of the log")
	Publish(data, Plan{Toolchain: "t", DepsHash: "d", Jobs: []Job{newer}}, results, date)
	readJSON(filepath.Join(data, "addons", "mongosk", "index.json"), &index)
	if index.Versions[0].Label != "3.1.0" || index.Versions[0].ErrorCode != "UNKNOWN" || index.Versions[0].Log != "3.1.0.log" || index.Latest != "3.0.0" {
		t.Fatalf("addon index after a failure %+v", index)
	}
	readJSON(filepath.Join(data, "index.json"), &root)
	if root.Addons[0].Last.Status != "failed" || root.Addons[0].Latest.Label != "3.0.0" {
		t.Fatalf("root index after a failure %+v %+v", root.Addons[0].Last, root.Addons[0].Latest)
	}

	// A planned job without result is reported, and nothing breaks.
	os.RemoveAll(results)
	summary, err = Publish(data, Plan{Jobs: []Job{newer}}, results, date)
	if err != nil || !strings.Contains(strings.Join(summary, "\n"), "no result") {
		t.Fatalf("missing result: %v %v", summary, err)
	}
}
