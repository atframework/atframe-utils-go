// Copyright 2026 atframework
package buildkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type goModuleInfo struct {
	Path    string        `json:"Path"`
	Dir     string        `json:"Dir"`
	Replace *goModuleInfo `json:"Replace"`
}

// ResolveModuleDirs runs `go list -m -json all` to map Go module paths to their
// resolved on-disk directories (whether in $GOMODCACHE during standalone builds
// or local directories when go.work / replace is active).
func ResolveModuleDirs(ctx context.Context, workDir string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list -m -json all in %s: %w\n%s", workDir, err, stderr.String())
	}

	dirs := make(map[string]string)
	dec := json.NewDecoder(&stdout)
	for {
		var m goModuleInfo
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go module list: %w", err)
		}
		dir := m.Dir
		if m.Replace != nil && m.Replace.Dir != "" {
			dir = m.Replace.Dir
		}
		if m.Path != "" && dir != "" {
			dirs[m.Path] = filepath.Clean(dir)
		}
	}
	return dirs, nil
}

// EnsureModuleDir returns the directory for modPath, running `go mod download`
// if the module is known in go.mod but not yet unpacked in the module cache.
func EnsureModuleDir(ctx context.Context, workDir, modPath string, knownDirs map[string]string) (string, error) {
	if dir, ok := knownDirs[modPath]; ok && dir != "" {
		if _, err := os.Stat(dir); err == nil {
			return dir, nil
		}
	}

	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", modPath)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err == nil {
		dir := strings.TrimSpace(string(out))
		if dir != "" {
			knownDirs[modPath] = filepath.Clean(dir)
			return filepath.Clean(dir), nil
		}
	}

	dl := exec.CommandContext(ctx, "go", "mod", "download", "-json", modPath)
	dl.Dir = workDir
	dlOut, dlErr := dl.Output()
	if dlErr != nil {
		return "", fmt.Errorf("resolve Go module %s in %s: %w", modPath, workDir, dlErr)
	}
	var dlInfo struct {
		Dir string `json:"Dir"`
	}
	if err := json.Unmarshal(dlOut, &dlInfo); err != nil || dlInfo.Dir == "" {
		return "", fmt.Errorf("module %s has no local dir after download", modPath)
	}
	knownDirs[modPath] = filepath.Clean(dlInfo.Dir)
	return filepath.Clean(dlInfo.Dir), nil
}

// ExpandProtoPath expands a proto path specification:
//   - "mod:<module-path>/<subpath>" or "mod:<module-path>" resolves via Go module graph
//   - relative paths resolve relative to baseDir
//   - absolute paths are returned cleaned
func ExpandProtoPath(ctx context.Context, spec, baseDir, modRoot string, modDirs map[string]string) (string, error) {
	if !strings.HasPrefix(spec, "mod:") {
		if filepath.IsAbs(spec) {
			return filepath.Clean(spec), nil
		}
		return filepath.Clean(filepath.Join(baseDir, spec)), nil
	}

	rest := strings.TrimPrefix(spec, "mod:")
	// Match longest known module path prefix first.
	bestMod := ""
	for modPath := range modDirs {
		if rest == modPath || strings.HasPrefix(rest, modPath+"/") {
			if len(modPath) > len(bestMod) {
				bestMod = modPath
			}
		}
	}
	if bestMod != "" {
		sub := strings.TrimPrefix(rest, bestMod)
		sub = strings.TrimPrefix(sub, "/")
		return filepath.Clean(filepath.Join(modDirs[bestMod], filepath.FromSlash(sub))), nil
	}

	// Fallback: try progressively shorter prefixes via EnsureModuleDir.
	parts := strings.Split(rest, "/")
	for n := len(parts); n >= 3; n-- {
		candidate := strings.Join(parts[:n], "/")
		dir, err := EnsureModuleDir(ctx, modRoot, candidate, modDirs)
		if err == nil && dir != "" {
			sub := strings.Join(parts[n:], "/")
			return filepath.Clean(filepath.Join(dir, filepath.FromSlash(sub))), nil
		}
	}
	return "", fmt.Errorf("unable to resolve proto module path spec %q", spec)
}

// ResolveGoTool resolves a Go tool binary path in priority order:
//  1. `go tool [-modfile=...] -n <toolName>` (Go 1.24+ native cached tool binary)
//  2. Local build of pkgPath into fallbackBinDir (when pkgPath is provided)
//  3. System PATH lookup
func ResolveGoTool(ctx context.Context, workDir, toolName, modFile, pkgPath, fallbackBinDir string) (string, error) {
	args := []string{"tool"}
	if modFile != "" {
		mf := modFile
		if !filepath.IsAbs(mf) {
			mf = filepath.Join(workDir, mf)
		}
		if _, err := os.Stat(mf); err == nil {
			args = append(args, "-modfile="+mf)
		}
	}
	args = append(args, "-n", toolName)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = workDir
	if out, err := cmd.Output(); err == nil {
		bin := strings.TrimSpace(string(out))
		if bin != "" {
			if _, statErr := os.Stat(bin); statErr == nil {
				return bin, nil
			}
		}
	}

	if pkgPath != "" && fallbackBinDir != "" {
		if err := os.MkdirAll(fallbackBinDir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir fallback bin dir: %w", err)
		}
		outBin := filepath.Join(fallbackBinDir, BinName(toolName))
		buildCmd := exec.CommandContext(ctx, "go", "build", "-o", outBin, pkgPath)
		buildCmd.Dir = workDir
		var stderr bytes.Buffer
		buildCmd.Stderr = &stderr
		if err := buildCmd.Run(); err == nil {
			return outBin, nil
		}
	}

	if p, err := exec.LookPath(toolName); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("go tool %q not found (tried go tool -n, go build %q, and PATH)", toolName, pkgPath)
}

type goPackageInfo struct {
	ImportPath string   `json:"ImportPath"`
	Dir        string   `json:"Dir"`
	Deps       []string `json:"Deps"`
}

// AffectedPackages computes the transitive closure of Go packages in scopePatterns
// impacted by changes relative to sinceRef (plus uncommitted working tree changes).
// If global module files (go.mod, go.work, go.sum) changed, returns scopePatterns directly.
func AffectedPackages(ctx context.Context, workDir, sinceRef string, scopePatterns []string) ([]string, error) {
	if len(scopePatterns) == 0 {
		scopePatterns = []string{"./..."}
	}
	changedFiles, err := gitChangedFiles(ctx, workDir, sinceRef)
	if err != nil {
		return nil, err
	}
	if len(changedFiles) == 0 {
		return nil, nil
	}

	changedDirs := make(map[string]struct{})
	for _, rel := range changedFiles {
		base := filepath.Base(rel)
		if base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum" {
			return scopePatterns, nil
		}
		abs := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(rel)))
		changedDirs[filepath.Dir(abs)] = struct{}{}
	}

	args := append([]string{"list", "-json"}, scopePatterns...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list -json: %w\n%s", err, stderr.String())
	}

	var pkgs []goPackageInfo
	dec := json.NewDecoder(&stdout)
	for {
		var p goPackageInfo
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go list package: %w", err)
		}
		pkgs = append(pkgs, p)
	}

	direct := make(map[string]struct{})
	for _, p := range pkgs {
		if _, ok := changedDirs[filepath.Clean(p.Dir)]; ok {
			direct[p.ImportPath] = struct{}{}
		}
	}
	if len(direct) == 0 {
		return nil, nil
	}

	affected := make([]string, 0, len(direct))
	for _, p := range pkgs {
		if _, ok := direct[p.ImportPath]; ok {
			affected = append(affected, p.ImportPath)
			continue
		}
		for _, dep := range p.Deps {
			if _, ok := direct[dep]; ok {
				affected = append(affected, p.ImportPath)
				break
			}
		}
	}
	slices.Sort(affected)
	return slices.Compact(affected), nil
}

func gitChangedFiles(ctx context.Context, workDir, sinceRef string) ([]string, error) {
	seen := make(map[string]struct{})

	runGitLines := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				seen[line] = struct{}{}
			}
		}
		return nil
	}

	if sinceRef != "" {
		if err := runGitLines("diff", "--name-only", sinceRef+"...HEAD"); err != nil {
			// Fallback for shallow clones or direct ref diff.
			if err2 := runGitLines("diff", "--name-only", sinceRef); err2 != nil {
				return nil, fmt.Errorf("git diff against %s: %w", sinceRef, err2)
			}
		}
	}
	// Also include uncommitted working tree modifications.
	_ = runGitLines("diff", "--name-only")
	_ = runGitLines("diff", "--cached", "--name-only")
	_ = runGitLines("ls-files", "--others", "--exclude-standard")

	result := make([]string, 0, len(seen))
	for f := range seen {
		result = append(result, f)
	}
	slices.Sort(result)
	return result, nil
}

// BinName appends ".exe" on Windows when not already present.
func BinName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}
