// Command repos turns repos.yaml, the one list of repositories the dot works on,
// into the files the other tools read: awf-gh.yaml, tend.list and repos.map.
//
//	repos generate   write the generated files (only the ones that changed)
//	repos check      exit 1 when a generated file is out of date, write nothing
//	repos import     build a first repos.yaml from the three existing files
//	repos features   list the keys a repository accepts
//	repos db         the database side of repos.yaml: import, export, check, list (ADR 0024)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencode-dots/internal/repos"
	"opencode-dots/internal/store"
	"opencode-dots/internal/workitem"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	sub := ""
	if cmd == "db" {
		// `repos db import -workspace x`: the flags follow the subcommand.
		if len(args) == 0 {
			usage()
			os.Exit(2)
		}
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("repos "+cmd, flag.ExitOnError)
	dir := fs.String("dir", ".", "repository root: holds repos.yaml and receives the generated files")
	awfURL := fs.String("awf-url", "", "awf_url of the generated awf-gh.yaml (default: keep the current one)")
	statePath := fs.String("state-path", "", "state_path of the generated awf-gh.yaml (default: keep the current one)")
	force := fs.Bool("force", false, "import: overwrite an existing repos.yaml")
	dbPath := fs.String("db", "", "db: the database file (default: $STATE_DIR/dots.db, else <dir>/state/dots.db)")
	workspace := fs.String("workspace", "default", "db: the workspace of the projects")
	prune := fs.Bool("prune", false, "db import: delete the projects the file omits, unless work items name them")
	fromDB := fs.Bool("from-db", false, "generate, check: build the files from the database instead of repos.yaml (ADR 0024, step 2)")
	ev := workitem.GitHubEvent{}
	linked := fs.String("linked", "", "db observe: issue numbers GitHub links to the pull request, comma separated")
	fs.StringVar(&ev.Repo, "repo", "", "db observe, db work-items: the repository")
	fs.StringVar(&ev.Kind, "kind", "", "db observe: the event (pr-opened, issue-opened, ...)")
	fs.IntVar(&ev.Number, "number", 0, "db observe: the issue or pull request number")
	fs.StringVar(&ev.Branch, "branch", "", "db observe: the head branch of a pull request")
	fs.StringVar(&ev.Actor, "actor", "", "db observe: the GitHub login that caused the event")
	fs.StringVar(&ev.Title, "title", "", "db observe: the title, kept on a new work item")
	fs.Usage = func() { usage(); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	override := repos.Settings{AwfURL: *awfURL, StatePath: *statePath}

	switch cmd {
	case "features":
		fmt.Println(strings.Join(repos.FeatureDocs(), "\n"))
	case "generate", "check":
		fail(generate(*dir, override, cmd == "check", *fromDB, *dbPath, *workspace))
	case "import":
		fail(importFiles(*dir, *force))
	case "db":
		nums, err := parseNumbers(*linked)
		if err != nil {
			fail(err)
		}
		ev.LinkedIssues = nums
		fail(dbCommand(sub, *dir, *dbPath, *workspace, *prune, ev))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: repos generate|check|import|features [-dir DIR] [-awf-url URL] [-state-path PATH] [-force]")
	fmt.Fprintln(os.Stderr, "       repos db import|export|check|list|work-items [-dir DIR] [-db FILE] [-workspace NAME] [-prune]")
	fmt.Fprintln(os.Stderr, "       repos db observe -repo R -kind K -number N -actor LOGIN [-branch B] [-linked 12,13] [-title T]")
	fmt.Fprintln(os.Stderr, "       repos generate|check -from-db   build the files from the database (ADR 0024, step 2)")
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "repos:", err)
		os.Exit(1)
	}
}

func generate(dir string, override repos.Settings, checkOnly, fromDB bool, dbPath, workspace string) error {
	var files map[string][]byte
	if fromDB {
		st, err := store.Open(resolveDB(dir, dbPath))
		if err != nil {
			return err
		}
		defer st.Close()
		if files, err = repos.BuildFromStore(context.Background(), st, workspace, dir, override); err != nil {
			return err
		}
	} else {
		data, err := os.ReadFile(filepath.Join(dir, repos.SourceFile))
		if err != nil {
			return fmt.Errorf("%w (run `repos import` to build it from awf-gh.yaml, tend.list and repos.map)", err)
		}
		if files, err = repos.Build(data, dir, override); err != nil {
			return err
		}
	}
	warnStaleWatchers(dir, files)
	if checkOnly {
		if stale := repos.Diff(dir, files); len(stale) > 0 {
			return fmt.Errorf("out of date, run `make repos`: %s", strings.Join(stale, ", "))
		}
		fmt.Println("repos: generated files are up to date")
		return nil
	}
	changed, err := repos.Write(dir, files)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Println("repos: nothing changed")
		return nil
	}
	fmt.Println("repos: wrote", strings.Join(changed, ", "))
	return nil
}

func importFiles(dir string, force bool) error {
	target := filepath.Join(dir, repos.SourceFile)
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("%s exists, pass -force to replace it", target)
	}
	data, err := repos.Import(dir)
	if err != nil {
		return err
	}
	// Prove the import before writing it: the file must parse and generate.
	if _, err := repos.Build(data, dir, repos.Settings{}); err != nil {
		return fmt.Errorf("the imported file does not generate: %w", err)
	}
	if old, err := os.ReadFile(target); err == nil {
		if err := os.WriteFile(target+"."+time.Now().Format("20060102-150405")+".bak", old, 0o600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return err
	}
	fmt.Println("repos: wrote", repos.SourceFile, "- read it, then run `repos generate`; the generated files replace the three you had")
	return nil
}

// warnStaleWatchers names a watcher file that this run does not generate any
// more: it stays on disk and keeps driving awf-gh until it is removed.
func warnStaleWatchers(dir string, files map[string][]byte) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if _, generated := files[name]; generated || !repos.IsWatcherFile(name) {
			continue
		}
		fmt.Fprintf(os.Stderr, "repos: %s is no longer generated; it is left as it is and still drives awf-gh: remove it to stop that watcher\n", name)
	}
}

// dbCommand is the database side of repos.yaml (ADR 0024, step 1). repos.yaml stays the
// source: `import` loads it, `check` compares what the database would generate with what
// the file generates, `export` and `list` read the database back.
func dbCommand(sub, dir, dbPath, workspace string, prune bool, ev workitem.GitHubEvent) error {
	dbPath = resolveDB(dir, dbPath)
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	switch sub {
	case "import":
		data, err := os.ReadFile(filepath.Join(dir, repos.SourceFile))
		if err != nil {
			return err
		}
		res, err := repos.ImportToStore(ctx, st, workspace, data, prune)
		if err != nil {
			return err
		}
		fmt.Printf("repos: %d added, %d updated, %d unchanged in workspace %q (%s)\n", res.Added, res.Updated, res.Unchanged, workspace, dbPath)
		if len(res.Pruned) > 0 {
			fmt.Println("repos: pruned", strings.Join(res.Pruned, ", "))
		}
		if len(res.Kept) > 0 {
			fmt.Println("repos: kept, work items name them:", strings.Join(res.Kept, ", "))
		}
		return nil
	case "export":
		out, err := repos.ExportYAML(ctx, st, workspace)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	case "check":
		data, err := os.ReadFile(filepath.Join(dir, repos.SourceFile))
		if err != nil {
			return err
		}
		differ, err := repos.DiffStoreAndFile(ctx, st, workspace, data, dir)
		if err != nil {
			return err
		}
		if len(differ) > 0 {
			return fmt.Errorf("the database and repos.yaml generate different files: %s (run `repos db import`)", strings.Join(differ, ", "))
		}
		fmt.Println("repos: the database and repos.yaml generate the same files")
		return nil
	case "observe":
		res, err := workitem.Observe(ctx, st, workspace, ev)
		if err != nil {
			return err
		}
		state := "existing"
		if res.Created {
			state = "created"
		}
		fmt.Printf("work_item=%s %s refs=%s\n", res.WorkItem.ID, state, strings.Join(refList(res.WorkItem), ","))
		return nil
	case "work-items":
		projects, err := st.Projects(ctx, workspace)
		if err != nil {
			return err
		}
		for _, p := range projects {
			if ev.Repo != "" && p.Repo != ev.Repo {
				continue
			}
			items, err := st.WorkItemsByProject(ctx, p.ID)
			if err != nil {
				return err
			}
			for _, w := range items {
				fmt.Printf("%s\t%s\t%s\t%s\t%s\n", w.ID, p.Repo, w.State, strings.Join(refList(w), ","), w.Title)
			}
		}
		return nil
	case "list":
		projects, err := st.Projects(ctx, workspace)
		if err != nil {
			return err
		}
		for _, p := range projects {
			keys := make([]string, 0, len(p.Features))
			for k := range p.Features {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Printf("%s\t%s\n", p.Repo, strings.Join(keys, ","))
		}
		return nil
	default:
		return fmt.Errorf("repos db: unknown subcommand %q (import, export, check, list, observe, work-items)", sub)
	}
}

// resolveDB is the database file: the flag, else $STATE_DIR/dots.db, else <dir>/state/dots.db.
func resolveDB(dir, dbPath string) string {
	if dbPath != "" {
		return dbPath
	}
	state := os.Getenv("STATE_DIR")
	if state == "" {
		state = filepath.Join(dir, "state")
	}
	return filepath.Join(state, "dots.db")
}

func parseNumbers(list string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("-linked: %q is not an issue number", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func refList(w store.WorkItem) []string {
	out := make([]string, 0, len(w.Refs))
	for _, r := range w.Refs {
		out = append(out, r.Ref)
	}
	return out
}
