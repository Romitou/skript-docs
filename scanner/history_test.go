package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadTracked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "addons.json")
	os.WriteFile(path, []byte(`{"addons":[{"slug":"mongosk","name":"MongoSK","repo":"Romitou/MongoSK","asset":"^MongoSK-.*\\.jar$"}]}`), 0o644)
	tracked, err := LoadTracked(path)
	if err != nil || len(tracked) != 1 || tracked[0].Repo != "Romitou/MongoSK" {
		t.Fatalf("%+v %v", tracked, err)
	}
	os.WriteFile(path, []byte(`{"addons":[
		{"slug":"Bad Slug","name":"x","repo":"a/b"},
		{"slug":"dup","name":"x","repo":"a/b"},{"slug":"dup","name":"x","repo":"a/b"},
		{"slug":"norepo","name":"x","repo":"https://github.com/a/b"},
		{"slug":"noname","name":" ","repo":"a/b"},
		{"slug":"badasset","name":"x","repo":"a/b","asset":"("}]}`), 0o644)
	_, err = LoadTracked(path)
	for _, want := range []string{"Bad Slug", "listed twice", "repo must be owner/name", "name is empty", "invalid asset pattern"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in %v", want, err)
		}
	}
}

func asset(id int64, name string, size int64, repo string) Asset {
	return Asset{ID: id, Name: name, Size: size, DownloadURL: "https://github.com/" + repo + "/releases/download/v/" + name}
}

func TestLatestReleaseAndPickAsset(t *testing.T) {
	addon := TrackedAddon{Slug: "x", Name: "X", Repo: "o/r"}
	releases := []Release{
		{TagName: "v4-draft", Draft: true, Assets: []Asset{asset(1, "X.jar", 1, "o/r")}},
		{TagName: "v3.1-beta", Prerelease: true, Assets: []Asset{asset(2, "X.jar", 1, "o/r")}},
		{TagName: "v3.0", Assets: []Asset{
			asset(3, "X-3.0-sources.jar", 50, "o/r"), asset(4, "X-3.0.jar", 10, "o/r"), asset(5, "X-3.0-all.jar", 30, "o/r"),
			asset(6, "notes.txt", 99, "o/r"), asset(7, "Other.jar", 999, "evil/repo"),
		}},
		{TagName: "v2.0", Assets: []Asset{asset(8, "X.jar", 1, "o/r")}},
	}
	rel, a, err := LatestRelease(addon, releases)
	if err != nil || rel.TagName != "v3.0" || a.ID != 5 {
		t.Fatalf("expected v3.0 and its largest jar, got %s %+v %v", rel.TagName, a, err)
	}
	addon.Prereleases = true
	if rel, _, _ := LatestRelease(addon, releases); rel.TagName != "v3.1-beta" {
		t.Errorf("with prereleases: got %s", rel.TagName)
	}
	addon.Prereleases, addon.Asset = false, `^X-[0-9.]+\.jar$`
	if _, a, _ := LatestRelease(addon, releases); a.ID != 4 {
		t.Errorf("asset pattern: got %+v", a)
	}
	// The newest release decides, even without a jar: an older one is not the latest.
	noJar := []Release{{TagName: "v5"}, releases[2]}
	if _, _, err := LatestRelease(TrackedAddon{Repo: "o/r"}, noJar); err == nil || !strings.Contains(err.Error(), "v5 has no jar") {
		t.Errorf("expected the newest release's error, got %v", err)
	}
	if _, _, err := LatestRelease(addon, nil); err == nil {
		t.Error("no release must be an error")
	}
}

func TestDecide(t *testing.T) {
	rel := Release{TagName: "v3.0"}
	a := asset(5, "X.jar", 1, "o/r")
	opts := PlanOptions{Toolchain: "t1", DepsHash: "d1"}
	last := Generation{Release: ReleaseInfo{Tag: "v3.0"}, AssetID: 5, JarURL: a.DownloadURL, Status: "succeeded", Toolchain: "t1", DepsHash: "d1"}
	with := func(g Generation) AddonIndex { return AddonIndex{Generations: []Generation{g}} }
	check := func(what string, index AddonIndex, opts PlanOptions, want bool) {
		t.Helper()
		if _, got := Decide(index, rel, a, opts); got != want {
			t.Errorf("%s: got %v, want %v", what, got, want)
		}
	}
	check("no generation", AddonIndex{}, opts, true)
	check("same release, nothing changed", with(last), opts, false)
	check("forced", with(last), PlanOptions{Toolchain: "t1", DepsHash: "d1", Force: true}, true)
	check("new images", with(last), PlanOptions{Toolchain: "t2", DepsHash: "d1"}, true)
	check("new deps after a success", with(last), PlanOptions{Toolchain: "t1", DepsHash: "d2"}, false)
	older := last
	older.Release.Tag = "v2.0"
	check("new release", with(older), opts, true)
	reuploaded := last
	reuploaded.AssetID = 4
	check("new jar on the release", with(reuploaded), opts, true)
	failed := last
	failed.Status = "failed"
	check("failed, nothing changed", with(failed), opts, false)
	check("failed, new deps", with(failed), PlanOptions{Toolchain: "t1", DepsHash: "d2"}, true)
}

func TestMakePlan(t *testing.T) {
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/new/releases":
			json.NewEncoder(w).Encode([]Release{{TagName: "v1", HTMLURL: "https://github.com/o/new/releases/tag/v1", Assets: []Asset{asset(1, "New.jar", 1, "o/new")}}})
		case "/repos/o/done/releases":
			json.NewEncoder(w).Encode([]Release{{TagName: "v2", Assets: []Asset{asset(2, "Done.jar", 1, "o/done")}}})
		case "/repos/o/nojar/releases":
			json.NewEncoder(w).Encode([]Release{{TagName: "v1"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer github.Close()
	previous := GitHubAPI
	GitHubAPI = github.URL
	defer func() { GitHubAPI = previous }()

	data := t.TempDir()
	writeJSON(filepath.Join(addonDir(data, "done"), "index.json"), AddonIndex{Slug: "done", Generations: []Generation{{
		Release: ReleaseInfo{Tag: "v2"}, AssetID: 2, JarURL: asset(2, "Done.jar", 1, "o/done").DownloadURL, Status: "succeeded", Toolchain: "t"}}})
	tracked := []TrackedAddon{{Slug: "new", Name: "New", Repo: "o/new"}, {Slug: "done", Name: "Done", Repo: "o/done"},
		{Slug: "nojar", Name: "NoJar", Repo: "o/nojar"}, {Slug: "gone", Name: "Gone", Repo: "o/gone"}}

	plan, err := MakePlan(context.Background(), &GitHub{HTTP: http.DefaultClient}, tracked, PlanOptions{Data: data, Toolchain: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Jobs) != 1 || plan.Jobs[0].AddonSlug != "new" || plan.Jobs[0].Release.Tag != "v1" || plan.Jobs[0].AssetID != 1 ||
		plan.Jobs[0].Reason != "first generation" {
		t.Fatalf("unexpected plan %+v", plan.Jobs)
	}
	plan, _ = MakePlan(context.Background(), &GitHub{HTTP: http.DefaultClient}, tracked, PlanOptions{Data: data, Toolchain: "t", Only: "done", Force: true})
	if len(plan.Jobs) != 1 || plan.Jobs[0].Reason != "forced" {
		t.Fatalf("--addon done --force: %+v", plan.Jobs)
	}
	if _, err := MakePlan(context.Background(), &GitHub{HTTP: http.DefaultClient}, tracked, PlanOptions{Only: "unknown"}); err == nil {
		t.Error("--addon with an untracked slug must fail")
	}
}

func TestPublishKeepsHistory(t *testing.T) {
	data := t.TempDir()
	tracked := []TrackedAddon{{Slug: "mongosk", Name: "MongoSK", Repo: "Romitou/MongoSK"}}
	job := Job{AddonSlug: "mongosk", AddonName: "MongoSK", Repo: "Romitou/MongoSK",
		Release: ReleaseInfo{Tag: "v3.0.0", URL: "https://github.com/Romitou/MongoSK/releases/tag/v3.0.0"},
		AssetID: 5, AssetName: "MongoSK-3.0.0.jar", JarURL: "https://github.com/Romitou/MongoSK/releases/download/v3.0.0/MongoSK-3.0.0.jar",
		Reason: "first generation"}
	run := func(at time.Time, jobs []Job, results map[string]Result, exports map[string][]byte, logs map[string]string) []string {
		t.Helper()
		dir := t.TempDir()
		for name, result := range results {
			os.MkdirAll(filepath.Join(dir, name), 0o755)
			raw, _ := json.Marshal(result)
			os.WriteFile(filepath.Join(dir, name, "result.json"), raw, 0o644)
			if exports[name] != nil {
				os.WriteFile(filepath.Join(dir, name, "export.json"), exports[name], 0o644)
			}
			if logs[name] != "" {
				os.WriteFile(filepath.Join(dir, name, "server.log"), []byte(logs[name]), 0o644)
			}
		}
		summary, err := Publish(data, tracked, Plan{Toolchain: "t", DepsHash: "d", Jobs: jobs}, dir, at)
		if err != nil {
			t.Fatal(err)
		}
		return summary
	}
	readIndex := func() AddonIndex {
		var index AddonIndex
		readJSON(filepath.Join(data, "addons", "mongosk", "index.json"), &index)
		return index
	}
	t1 := time.Date(2026, 11, 19, 16, 52, 7, 0, time.UTC)

	// A forged result for another add-on is ignored; the planned one becomes generation 1.
	forged := job
	forged.AddonSlug = "other"
	summary := run(t1, []Job{job}, map[string]Result{
		"result-0": {Job: forged, Status: "succeeded"},
		"result-1": {Job: job, Status: "succeeded", JarSHA256: strings.Repeat("a", 64), Plugin: PluginMeta{Name: "MongoSK"}},
	}, map[string][]byte{"result-0": export(t, nil), "result-1": export(t, nil)}, nil)
	if !strings.Contains(strings.Join(summary, "\n"), "unplanned") {
		t.Errorf("summary %v", summary)
	}
	if _, err := os.Stat(filepath.Join(data, "addons", "other")); !os.IsNotExist(err) {
		t.Fatal("an unplanned result must not be written")
	}
	index := readIndex()
	first := "20261119-165207-v3.0.0"
	if index.Latest != first || len(index.Generations) != 1 || index.Generations[0].File != first+".json" || index.Repo != "Romitou/MongoSK" {
		t.Fatalf("after generation 1: %+v", index)
	}

	// Generation 2, same release, new images, fails: kept next to the first, which stays latest.
	t2 := t1.Add(6 * time.Hour)
	retry := job
	retry.Reason = "new extractor images"
	run(t2, []Job{retry}, map[string]Result{"r": {Job: retry, Status: "failed", ErrorCode: "bad; code", ErrorMessage: "boom"}},
		nil, map[string]string{"r": "end of the log"})
	index = readIndex()
	second := "20261119-225207-v3.0.0"
	if len(index.Generations) != 2 || index.Generations[0].ID != second || index.Generations[0].ErrorCode != "UNKNOWN" ||
		index.Generations[0].Log != second+".log" || index.Latest != first {
		t.Fatalf("after generation 2: %+v", index)
	}
	for _, f := range []string{first + ".json", second + ".log"} {
		if _, err := os.Stat(filepath.Join(data, "addons", "mongosk", f)); err != nil {
			t.Errorf("%s must be kept: %v", f, err)
		}
	}

	// Generation 3, a new release: latest moves, all history stays.
	t3 := t2.Add(time.Hour)
	next := job
	next.Release.Tag, next.AssetID, next.JarURL = "v3.1.0", 9, "https://github.com/Romitou/MongoSK/releases/download/v3.1.0/MongoSK-3.1.0.jar"
	run(t3, []Job{next}, map[string]Result{"r": {Job: next, Status: "succeeded"}}, map[string][]byte{"r": export(t, nil)}, nil)
	index = readIndex()
	if len(index.Generations) != 3 || index.Latest != "20261119-235207-v3.1.0" {
		t.Fatalf("after generation 3: %+v", index)
	}
	var root RootIndex
	readJSON(filepath.Join(data, "index.json"), &root)
	if len(root.Addons) != 1 || !root.Addons[0].Tracked || root.Addons[0].Latest.Release != "v3.1.0" ||
		root.Addons[0].Latest.File != "addons/mongosk/20261119-235207-v3.1.0.json" || root.Addons[0].Last.Status != "succeeded" {
		t.Fatalf("root index %+v", root.Addons[0])
	}

	// Removed from addons.json: the history stays, marked untracked.
	tracked = nil
	run(t3, nil, nil, nil, nil)
	readJSON(filepath.Join(data, "index.json"), &root)
	if root.Addons[0].Tracked || root.Addons[0].Latest == nil {
		t.Fatalf("untracked add-on: %+v", root.Addons[0])
	}

	// A planned job without result is reported.
	if summary := run(t3, []Job{next}, nil, nil, nil); !strings.Contains(strings.Join(summary, "\n"), "no result") {
		t.Errorf("summary %v", summary)
	}
}
