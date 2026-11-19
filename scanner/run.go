package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxRunLogBytes = 64 << 10
	maxExportBytes = 32 << 20
)

// RunResult is one container run.
type RunResult struct {
	Export json.RawMessage
	Log    string
}

// RunContainer runs the extractor image on the jars of inputDir, in a container that has no
// network, no capabilities, a read-only root and bounded memory, CPU, processes and time.
func RunContainer(ctx context.Context, cfg Config, image Image, inputDir, outputDir, target string) (RunResult, error) {
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	name := "skmc-extract-" + hex.EncodeToString(suffix)
	args := DockerArgs(cfg, image, name, inputDir, outputDir, target)

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, cfg.Docker, args...)
	logs := &tailBuffer{max: maxRunLogBytes}
	cmd.Stdout = logs
	cmd.Stderr = logs
	// On timeout, the docker client is killed, not the container: kill it by name.
	cmd.Cancel = func() error {
		killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer killCancel()
		_ = exec.CommandContext(killCtx, cfg.Docker, "rm", "-f", name).Run()
		return cmd.Process.Kill()
	}
	runErr := cmd.Run()
	result := RunResult{Log: logs.String()}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return result, fail("TIMEOUT", "the server did not finish within %s", cfg.Timeout)
	}

	raw, readErr := readLimited(filepath.Join(outputDir, "export.json"), maxExportBytes)
	if readErr != nil {
		if runErr != nil {
			return result, fail("CONTAINER_FAILED", "%v", runErr)
		}
		return result, fail("NO_OUTPUT", "%v", readErr)
	}
	var probe struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Format string `json:"format"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return result, fail("INVALID_OUTPUT", "export.json: %v", err)
	}
	if probe.Error != nil {
		return result, fail(probe.Error.Code, "%s", probe.Error.Message)
	}
	if probe.Format != "skmc-syntax-export" {
		return result, fail("INVALID_OUTPUT", "export.json has an unknown format %q", probe.Format)
	}
	result.Export = raw
	return result, nil
}

// DockerArgs is the `docker run` command line, kept apart to be tested.
func DockerArgs(cfg Config, image Image, name, inputDir, outputDir, target string) []string {
	args := []string{
		"run", "--rm", "--name", name,
		"--network", "none",
		"--read-only",
		"--tmpfs", "/work:rw,exec,size=1g,mode=1777",
		"--tmpfs", "/tmp:rw,exec,size=256m,mode=1777",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "512",
		"--memory", cfg.Memory,
		"--memory-swap", cfg.Memory,
		"--cpus", cfg.CPUs,
		"--hostname", "skmc-extractor",
		"--add-host", "skmc-extractor:127.0.0.1",
		"--label", "fr.skript-mc.extractor=1",
	}
	if cfg.Runtime != "" {
		args = append(args, "--runtime", cfg.Runtime)
	}
	args = append(args,
		"-v", inputDir+":/input:ro",
		"-v", outputDir+":/output",
		"-e", "SKMC_EXPORT_TARGET="+target,
		image.Ref,
	)
	return args
}

func readLimited(path string, max int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), max)
	}
	return raw, nil
}

// tailBuffer keeps the last max bytes written: a server log's end says why it stopped.
type tailBuffer struct {
	buf bytes.Buffer
	max int
	cut bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		rest := append([]byte(nil), t.buf.Bytes()[over:]...)
		t.buf.Reset()
		t.buf.Write(rest)
		t.cut = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	s := strings.ToValidUTF8(t.buf.String(), "")
	if t.cut {
		return "[…]\n" + s
	}
	return s
}
