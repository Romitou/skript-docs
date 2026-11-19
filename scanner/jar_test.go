package main

import (
	"archive/zip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseDescriptor(t *testing.T) {
	info, err := ParseDescriptor([]byte(`
name: MongoSK
version: 3.0.0
main: fr.romitou.mongosk.MongoSK
api-version: 26.1
depend: [Skript]
softdepend:
  - Vault
`))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "MongoSK" || info.Version != "3.0.0" || info.APIVersion != "26.1" {
		t.Fatalf("unexpected %+v", info)
	}
	if !info.DependsOnSkript() || !slices.Equal(info.SoftDepend, []string{"Vault"}) {
		t.Fatalf("unexpected dependencies %+v", info)
	}

	// YAML numbers (api-version: 1.13 is a float, version: 3 an int) keep their written form.
	info, err = ParseDescriptor([]byte("name: X\nversion: 3\napi-version: 1.13\nsoftdepend: [skript]\n"))
	if err != nil || info.Version != "3" || info.APIVersion != "1.13" || !info.DependsOnSkript() {
		t.Fatalf("unexpected %+v %v", info, err)
	}

	// paper-plugin.yml dependencies
	info, err = ParseDescriptor([]byte(`
name: PaperAddon
api-version: '1.21'
dependencies:
  server:
    Skript:
      load: BEFORE
    ProtocolLib:
      required: false
`))
	if err != nil || !slices.Equal(info.Depend, []string{"Skript"}) || !slices.Equal(info.SoftDepend, []string{"ProtocolLib"}) {
		t.Fatalf("unexpected %+v %v", info, err)
	}

	if _, err := ParseDescriptor([]byte("version: 1\n")); !isFailure(err, "NO_PLUGIN_YML") {
		t.Fatalf("a nameless plugin.yml must fail, got %v", err)
	}
}

func TestCheckURL(t *testing.T) {
	allowed := []string{"github.com", "objects.githubusercontent.com"}
	for _, ok := range []string{
		"https://github.com/Romitou/MongoSK/releases/download/v3.0.0/MongoSK-3.0.0.jar",
		"https://objects.githubusercontent.com/x",
	} {
		if err := checkURL(ok, allowed); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://github.com/a.jar",
		"https://evil.example/a.jar",
		"https://github.com.evil.example/a.jar",
		"file:///etc/passwd",
		"https://169.254.169.254/latest",
	} {
		if err := checkURL(bad, allowed); !isFailure(err, "HOST_NOT_ALLOWED") {
			t.Errorf("%s must be refused, got %v", bad, err)
		}
	}
}

func TestDownloadLimitsAndRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write(make([]byte, 2048))
		case "/away":
			http.Redirect(w, r, "https://evil.example/a.jar", http.StatusFound)
		default:
			w.Write([]byte("PK"))
		}
	}))
	defer server.Close()
	http.DefaultTransport = server.Client().Transport // trust the test certificate
	host := strings.TrimPrefix(server.URL, "https://")
	hostname := host[:strings.LastIndex(host, ":")]
	dir := t.TempDir()

	if _, err := Download(context.Background(), server.URL+"/big", filepath.Join(dir, "a"), []string{hostname}, 1024); !isFailure(err, "JAR_TOO_LARGE") {
		t.Errorf("expected JAR_TOO_LARGE, got %v", err)
	}
	if _, err := Download(context.Background(), server.URL+"/away", filepath.Join(dir, "b"), []string{hostname}, 1024); !isFailure(err, "HOST_NOT_ALLOWED") {
		t.Errorf("a redirect off the allowed hosts must be refused, got %v", err)
	}
	sum, err := Download(context.Background(), server.URL+"/ok", filepath.Join(dir, "c"), []string{hostname}, 1024)
	if err != nil || len(sum) != 64 {
		t.Errorf("expected a sha256, got %q %v", sum, err)
	}
}

func TestDockerArgsSandbox(t *testing.T) {
	cfg := Config{Memory: "1536m", CPUs: "2", Runtime: "runsc"}
	args := strings.Join(DockerArgs(cfg, Image{Ref: "img:26.3"}, "n", "/in", "/out", "MongoSK"), " ")
	for _, want := range []string{"--network none", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges",
		"--pids-limit 512", "--memory 1536m", "--runtime runsc", "-v /in:/input:ro", "SKMC_EXPORT_TARGET=MongoSK"} {
		if !strings.Contains(args, want) {
			t.Errorf("docker args lack %q: %s", want, args)
		}
	}
	if !strings.HasSuffix(args, "img:26.3") {
		t.Errorf("the image must come last: %s", args)
	}
}

func TestTailBuffer(t *testing.T) {
	buf := &tailBuffer{max: 10}
	buf.Write([]byte("0123456789"))
	buf.Write([]byte("abcdef"))
	if got := buf.String(); got != "[…]\n6789abcdef" {
		t.Errorf("got %q", got)
	}
}

func writeJar(t *testing.T, path, pluginYML string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zw := zip.NewWriter(file)
	w, _ := zw.Create("plugin.yml")
	w.Write([]byte(pluginYML))
	zw.Close()
}

func isFailure(err error, code string) bool {
	var f *Failure
	return errors.As(err, &f) && f.Code == code
}
