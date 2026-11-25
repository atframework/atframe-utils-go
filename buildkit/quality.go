// Copyright 2026 atframework
package buildkit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// LintOptions controls golangci-lint execution.
type LintOptions struct {
	WorkDir    string
	ConfigPath string
	ReportDir  string
	Packages   []string // Defaults to ["./..."]
}

// RunLint executes golangci-lint with strict non-zero exit code propagation
// and optional report output.
func RunLint(ctx context.Context, opts LintOptions) error {
	lintBin, err := exec.LookPath("golangci-lint")
	if err != nil {
		return fmt.Errorf("golangci-lint not found on PATH (install with `mise install` or `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.0`): %w", err)
	}

	pkgs := opts.Packages
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	args := []string{"run"}
	if opts.ConfigPath != "" {
		args = append(args, "--config", opts.ConfigPath)
	}
	args = append(args, pkgs...)

	fmt.Printf("[lint] running golangci-lint in %s (%s)\n", opts.WorkDir, strings.Join(pkgs, " "))
	cmd := exec.CommandContext(ctx, lintBin, args...)
	cmd.Dir = opts.WorkDir
	var combined bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &combined)
	cmd.Stderr = io.MultiWriter(os.Stderr, &combined)
	runErr := cmd.Run()

	if opts.ReportDir != "" {
		if mkErr := os.MkdirAll(opts.ReportDir, 0o755); mkErr == nil {
			reportPath := filepath.Join(opts.ReportDir, "golangci-lint-report.txt")
			header := fmt.Sprintf("golangci-lint Report - %s\nWorkDir: %s\n============================================\n",
				time.Now().Format(time.RFC3339), opts.WorkDir)
			_ = os.WriteFile(reportPath, append([]byte(header), combined.Bytes()...), 0o644)
		}
	}

	if runErr != nil {
		return fmt.Errorf("golangci-lint failed in %s: %w", opts.WorkDir, runErr)
	}
	return nil
}

// TestOptions controls go test execution.
type TestOptions struct {
	WorkDir       string
	Packages      []string
	AffectedSince string
	Race          bool
	Verbose       bool
	Timeout       string
	NoWork        bool // Sets GOWORK=off for standalone verification
}

// RunTests executes go test with optional affected-package pruning and strict exit checks.
func RunTests(ctx context.Context, opts TestOptions) error {
	pkgs := opts.Packages
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}

	if opts.AffectedSince != "" {
		affected, err := AffectedPackages(ctx, opts.WorkDir, opts.AffectedSince, pkgs)
		if err != nil {
			return fmt.Errorf("compute affected packages: %w", err)
		}
		if len(affected) == 0 {
			fmt.Printf("[test] no affected Go packages since %s, skipping\n", opts.AffectedSince)
			return nil
		}
		fmt.Printf("[test] %d affected packages since %s\n", len(affected), opts.AffectedSince)
		pkgs = affected
	}

	args := []string{"test"}
	if opts.Verbose {
		args = append(args, "-v")
	}
	if opts.Race {
		args = append(args, "-race")
	}
	timeout := opts.Timeout
	if timeout == "" {
		timeout = "10m"
	}
	args = append(args, "-timeout="+timeout)
	args = append(args, pkgs...)

	fmt.Printf("[test] running `go %s` in %s\n", strings.Join(args[:min(len(args), 5)], " "), opts.WorkDir)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = opts.WorkDir
	if opts.NoWork {
		cmd.Env = append(os.Environ(), "GOWORK=off")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go test failed in %s: %w", opts.WorkDir, err)
	}
	return nil
}

// VerifyStandalone runs `GOWORK=off go build ./...` (and optionally `go test ./...`)
// inside each listed module directory to verify standalone compilability.
func VerifyStandalone(ctx context.Context, moduleDirs []string, runTests bool) error {
	for _, dir := range moduleDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		if !fileExists(filepath.Join(absDir, "go.mod")) {
			return fmt.Errorf("standalone verify target missing go.mod: %s", absDir)
		}

		fmt.Printf("[verify-standalone] GOWORK=off go build ./... in %s\n", absDir)
		buildCmd := exec.CommandContext(ctx, "go", "build", "./...")
		buildCmd.Dir = absDir
		buildCmd.Env = append(os.Environ(), "GOWORK=off")
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			return fmt.Errorf("standalone build failed in %s: %w", absDir, err)
		}

		if runTests {
			if err := RunTests(ctx, TestOptions{
				WorkDir: absDir,
				NoWork:  true,
				Timeout: "5m",
			}); err != nil {
				return fmt.Errorf("standalone test failed in %s: %w", absDir, err)
			}
		}
	}
	return nil
}

// CopyFile copies a single file, creating parent directories and preserving permissions.
func CopyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", dst, closeErr)
	}
	return nil
}

// CopyDir recursively copies all files from srcDir into dstDir.
func CopyDir(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return CopyFile(p, target)
	})
}

// CopyTopLevelFiles copies only regular files directly under srcDir into dstDir.
func CopyTopLevelFiles(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := CopyFile(filepath.Join(srcDir, entry.Name()), filepath.Join(dstDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// RemoveMatchingFiles deletes files under dir matching pred.
func RemoveMatchingFiles(dir string, pred func(path string, name string) bool) (int, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0, nil
	}
	var toRemove []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if pred(p, d.Name()) {
			toRemove = append(toRemove, p)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	slices.Sort(toRemove)
	for _, p := range toRemove {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return len(toRemove), nil
}
