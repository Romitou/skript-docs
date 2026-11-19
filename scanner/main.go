// Command scanner keeps this repository's syntax data up to date (see the README):
//
//	scanner validate [--check-repos]
//	scanner plan     [--addon SLUG] [--force] [--max N] [--github-output FILE] > plan.json
//	scanner extract  --job JSON --out DIR
//	scanner publish  --plan plan.json --results DIR
//
// validate checks addons.json, deps.json and images.json; plan lists the tracked add-ons whose
// latest GitHub release needs a new generation; extract runs one in a throwaway, network-less
// Paper + Skript container; publish checks the results and adds them under addons/ as new
// generations. The workflow runs extract in a job with no write
// access: it executes the add-on's code. publish, which may commit, never does.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "validate":
		err = runValidate(ctx, os.Args[2:])
	case "plan":
		err = runPlan(ctx, os.Args[2:])
	case "extract":
		err = runExtract(ctx, os.Args[2:])
	case "publish":
		err = runPublish(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "scanner:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: scanner validate|plan|extract|publish [flags] (see the README)")
	os.Exit(2)
}

// Paths shared by the commands, relative to the repository root.
type paths struct {
	data, tracked, images, deps string
}

func pathFlags(fs *flag.FlagSet) *paths {
	p := &paths{}
	fs.StringVar(&p.data, "data", ".", "repository root (addons/, index.json)")
	fs.StringVar(&p.tracked, "addons", "addons.json", "tracked add-ons")
	fs.StringVar(&p.images, "images", "extractor/images.json", "extractor images")
	fs.StringVar(&p.deps, "deps", "deps.json", "dependency catalog")
	return p
}

func runValidate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	p := pathFlags(fs)
	checkRepos := fs.Bool("check-repos", false, "also check each repository is public on GitHub")
	_ = fs.Parse(args)

	tracked, err := LoadTracked(p.tracked)
	if err != nil {
		return err
	}
	if _, err := LoadCatalog(p.deps); err != nil {
		return err
	}
	if _, err := LoadImages(p.images, ""); err != nil {
		return err
	}
	if *checkRepos {
		github := NewGitHub()
		var failed []string
		for _, a := range tracked {
			if err := github.RepoExists(ctx, a.Repo); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %s: %v", a.Slug, a.Repo, err))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("repositories:\n  %s", strings.Join(failed, "\n  "))
		}
	}
	fmt.Printf("%d add-ons tracked, configuration valid\n", len(tracked))
	return nil
}

func runPlan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	p := pathFlags(fs)
	only := fs.String("addon", "", "only this add-on (slug)")
	force := fs.Bool("force", false, "generate again even when nothing changed")
	max := fs.Int("max", 20, "jobs per run")
	githubOutput := fs.String("github-output", "", "also write matrix= and count= to this file ($GITHUB_OUTPUT)")
	_ = fs.Parse(args)

	tracked, err := LoadTracked(p.tracked)
	if err != nil {
		return err
	}
	if _, err := LoadCatalog(p.deps); err != nil {
		return err
	}
	if _, err := LoadImages(p.images, ""); err != nil {
		return err
	}
	plan, err := MakePlan(ctx, NewGitHub(), tracked, PlanOptions{
		Data: p.data, Toolchain: fileHash(p.images), DepsHash: fileHash(p.deps),
		Only: strings.TrimSpace(*only), Force: *force, Max: *max,
	})
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(plan, "", "  ")
	fmt.Println(string(raw))
	for _, job := range plan.Jobs {
		fmt.Fprintf(os.Stderr, "plan: %s %s (%s)\n", job.AddonSlug, job.Release.Tag, job.Reason)
	}
	if *githubOutput != "" {
		matrix, _ := json.Marshal(plan.Jobs)
		f, err := os.OpenFile(*githubOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := fmt.Fprintf(f, "matrix=%s\ncount=%d\n", matrix, len(plan.Jobs)); err != nil {
			return err
		}
	}
	return nil
}

func runExtract(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	p := pathFlags(fs)
	jobJSON := fs.String("job", "", "the job, as planned (JSON)")
	out := fs.String("out", "result", "output directory")
	_ = fs.Parse(args)

	var job Job
	if err := json.Unmarshal([]byte(*jobJSON), &job); err != nil {
		return fmt.Errorf("--job: %w", err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	images, err := LoadImages(p.images, env("SCANNER_IMAGE_REPOSITORY", ""))
	if err != nil {
		return err
	}
	catalog, err := LoadCatalog(p.deps)
	if err != nil {
		return err
	}
	if err := Extract(ctx, cfg, images, catalog, job, *out); err != nil {
		return err
	}
	var result Result
	_ = readJSON(*out+"/result.json", &result)
	fmt.Fprintf(os.Stderr, "extract: %s %s: %s %s %s\n", job.AddonSlug, job.Release.Tag, result.Status, result.ErrorCode, result.ErrorMessage)
	return nil
}

func runPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	p := pathFlags(fs)
	planPath := fs.String("plan", "plan.json", "the plan of this run")
	results := fs.String("results", "results", "directory holding the extraction results")
	_ = fs.Parse(args)

	var plan Plan
	if err := readJSON(*planPath, &plan); err != nil {
		return fmt.Errorf("--plan: %w", err)
	}
	tracked, err := LoadTracked(p.tracked)
	if err != nil {
		return err
	}
	summary, err := Publish(p.data, tracked, plan, *results, time.Now())
	for _, line := range summary {
		fmt.Println(line)
	}
	return err
}
