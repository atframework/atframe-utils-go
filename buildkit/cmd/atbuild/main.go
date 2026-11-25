// Copyright 2026 atframework
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atframework/atframe-utils-go/buildkit"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	ctx := context.Background()
	rootDir, err := findRepoRoot()
	if err != nil {
		fatalf("locate repo root: %v", err)
	}

	cfg, err := buildkit.LoadConfig(rootDir)
	if err != nil {
		fatalf("load config: %v", err)
	}

	sub := os.Args[1]
	args := os.Args[2:]
	switch sub {
	case "proto":
		err = cmdProto(ctx, rootDir, cfg, args)
	case "build":
		err = cmdBuild(ctx, rootDir, args)
	case "test":
		err = cmdTest(ctx, rootDir, args)
	case "lint":
		err = cmdLint(ctx, rootDir, cfg, args)
	case "verify-standalone":
		err = cmdVerifyStandalone(ctx, rootDir, cfg, args)
	case "clean":
		err = cmdClean(rootDir, cfg)
	case "doctor":
		err = cmdDoctor(ctx, rootDir, cfg)
	case "-h", "--help", "help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		printUsage()
		os.Exit(2)
	}

	if err != nil {
		fatalf("%v", err)
	}
}

func printUsage() {
	fmt.Print(`atbuild - cross-platform Go build & codegen CLI for atframework repositories

Usage:
  atbuild <command> [flags]

Commands:
  proto              Generate protobuf and *_mutable.pb.go code (incremental SHA-256 cached)
  build              Compile Go packages (go build ./...)
  test               Run Go unit tests (supports --affected, --race, --standalone)
  lint               Run golangci-lint with strict exit code propagation
  verify-standalone  Run GOWORK=off build/test across standalone modules
  clean              Remove atbuild cache artifacts
  doctor             Print resolved Go, protoc, and plugin versions/paths
`)
}

func cmdProto(ctx context.Context, rootDir string, cfg *buildkit.Config, args []string) error {
	fs := flag.NewFlagSet("proto", flag.ExitOnError)
	force := fs.Bool("force", false, "force regeneration ignoring cache")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(cfg.ProtoTargets) == 0 {
		fmt.Println("[atbuild] no proto_targets configured in atbuild.yaml")
		return nil
	}

	toolsDir := resolveUnder(rootDir, cfg.ToolsDir)
	cacheDir := resolveUnder(rootDir, cfg.CacheDir)
	cache, err := buildkit.OpenCacheStore(filepath.Join(cacheDir, "manifest.json"))
	if err != nil {
		return err
	}

	protocBin, err := buildkit.EnsureProtoc(ctx, cfg.ProtocVersion, toolsDir)
	if err != nil {
		return err
	}
	binFallback := filepath.Join(toolsDir, "bin")
	goPlugin, err := buildkit.ResolveGoTool(ctx, rootDir, "protoc-gen-go", cfg.ToolsModFile, "google.golang.org/protobuf/cmd/protoc-gen-go", binFallback)
	if err != nil {
		return err
	}
	mutPlugin, err := buildkit.ResolveGoTool(ctx, rootDir, "protoc-gen-mutable", cfg.ToolsModFile, "github.com/atframework/atframe-utils-go/protoc-gen-mutable", binFallback)
	if err != nil {
		return err
	}

	modDirs, err := buildkit.ResolveModuleDirs(ctx, rootDir)
	if err != nil {
		return err
	}

	runner := &buildkit.ProtoRunner{
		RootDir:          rootDir,
		ProtocBin:        protocBin,
		ProtocVersion:    cfg.ProtocVersion,
		ProtocGenGoBin:   goPlugin,
		ProtocGenMutable: mutPlugin,
		ModuleDirs:       modDirs,
		Cache:            cache,
		Force:            *force,
	}
	return runner.RunTargets(ctx, cfg.ProtoTargets)
}

func cmdBuild(ctx context.Context, rootDir string, args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	standalone := fs.Bool("standalone", false, "run with GOWORK=off")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pkgs := fs.Args()
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	cmdArgs := append([]string{"build"}, pkgs...)
	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = rootDir
	if *standalone {
		cmd.Env = append(os.Environ(), "GOWORK=off")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdTest(ctx context.Context, rootDir string, args []string) error {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	affected := fs.String("affected", "", "run tests only for packages affected since git ref")
	race := fs.Bool("race", false, "enable race detector")
	verbose := fs.Bool("v", false, "verbose test output")
	timeout := fs.String("timeout", "10m", "go test timeout")
	standalone := fs.Bool("standalone", false, "run with GOWORK=off")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return buildkit.RunTests(ctx, buildkit.TestOptions{
		WorkDir:       rootDir,
		Packages:      fs.Args(),
		AffectedSince: *affected,
		Race:          *race,
		Verbose:       *verbose,
		Timeout:       *timeout,
		NoWork:        *standalone,
	})
}

func cmdLint(ctx context.Context, rootDir string, cfg *buildkit.Config, args []string) error {
	fs := flag.NewFlagSet("lint", flag.ExitOnError)
	reportDir := fs.String("report-dir", "", "optional directory to write lint report")
	configPath := fs.String("config", cfg.LintConfig, "path to .golangci.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfgFile := *configPath
	if cfgFile == "" {
		candidate := filepath.Join(rootDir, ".golangci.yaml")
		if _, err := os.Stat(candidate); err == nil {
			cfgFile = candidate
		}
	} else if !filepath.IsAbs(cfgFile) {
		cfgFile = filepath.Join(rootDir, cfgFile)
	}

	return buildkit.RunLint(ctx, buildkit.LintOptions{
		WorkDir:    rootDir,
		ConfigPath: cfgFile,
		ReportDir:  *reportDir,
		Packages:   fs.Args(),
	})
}

func cmdVerifyStandalone(ctx context.Context, rootDir string, cfg *buildkit.Config, args []string) error {
	fs := flag.NewFlagSet("verify-standalone", flag.ExitOnError)
	runTests := fs.Bool("test", false, "also run GOWORK=off go test ./...")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dirs := make([]string, 0, len(cfg.StandaloneModules)+1)
	if len(cfg.StandaloneModules) == 0 {
		dirs = append(dirs, rootDir)
	} else {
		for _, m := range cfg.StandaloneModules {
			dirs = append(dirs, resolveUnder(rootDir, m))
		}
	}
	return buildkit.VerifyStandalone(ctx, dirs, *runTests)
}

func cmdClean(rootDir string, cfg *buildkit.Config) error {
	cacheDir := resolveUnder(rootDir, cfg.CacheDir)
	fmt.Printf("[atbuild] removing %s\n", cacheDir)
	return os.RemoveAll(cacheDir)
}

func cmdDoctor(ctx context.Context, rootDir string, cfg *buildkit.Config) error {
	goVer, _ := exec.CommandContext(ctx, "go", "version").Output()
	fmt.Printf("RepoRoot: %s\n", rootDir)
	fmt.Printf("Go:       %s\n", strings.TrimSpace(string(goVer)))
	if p, err := exec.LookPath("protoc"); err == nil {
		fmt.Printf("protoc:   %s\n", p)
	} else {
		fmt.Printf("protoc:   (managed in %s on demand)\n", resolveUnder(rootDir, cfg.ToolsDir))
	}
	if p, err := exec.LookPath("golangci-lint"); err == nil {
		fmt.Printf("lint:     %s\n", p)
	} else {
		fmt.Printf("lint:     missing\n")
	}
	return nil
}

func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		for _, marker := range []string{"atbuild.yaml", "go.mod"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd, nil
		}
		dir = parent
	}
}

func resolveUnder(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(root, filepath.FromSlash(p)))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[atbuild] ERROR: "+format+"\n", args...)
	os.Exit(1)
}
