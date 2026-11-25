// Command repos turns repos.yaml, the one list of repositories the dot works on,
// into the files the other tools read: awf-gh.yaml, tend.list and repos.map.
//
//	repos generate   write the generated files (only the ones that changed)
//	repos check      exit 1 when a generated file is out of date, write nothing
//	repos import     build a first repos.yaml from the three existing files
//	repos features   list the keys a repository accepts
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opencode-dots/internal/repos"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet("repos "+cmd, flag.ExitOnError)
	dir := fs.String("dir", ".", "repository root: holds repos.yaml and receives the generated files")
	awfURL := fs.String("awf-url", "", "awf_url of the generated awf-gh.yaml (default: keep the current one)")
	statePath := fs.String("state-path", "", "state_path of the generated awf-gh.yaml (default: keep the current one)")
	force := fs.Bool("force", false, "import: overwrite an existing repos.yaml")
	fs.Usage = func() { usage(); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	override := repos.Settings{AwfURL: *awfURL, StatePath: *statePath}

	switch cmd {
	case "features":
		fmt.Println(strings.Join(repos.FeatureDocs(), "\n"))
	case "generate", "check":
		fail(generate(*dir, override, cmd == "check"))
	case "import":
		fail(importFiles(*dir, *force))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: repos generate|check|import|features [-dir DIR] [-awf-url URL] [-state-path PATH] [-force]")
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "repos:", err)
		os.Exit(1)
	}
}

func generate(dir string, override repos.Settings, checkOnly bool) error {
	data, err := os.ReadFile(filepath.Join(dir, repos.SourceFile))
	if err != nil {
		return fmt.Errorf("%w (run `repos import` to build it from awf-gh.yaml, tend.list and repos.map)", err)
	}
	files, err := repos.Build(data, dir, override)
	if err != nil {
		return err
	}
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
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return err
	}
	fmt.Println("repos: wrote", repos.SourceFile, "- read it, then run `repos generate`; the generated files replace the three you had")
	return nil
}
