// Copyright 2026 atframework
package buildkit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
)

// ProtoTarget defines a single protoc compilation step.
type ProtoTarget struct {
	Name          string   `yaml:"name"`
	Stage         int      `yaml:"stage"` // Lower stage runs first; same stage runs concurrently.
	WorkDir       string   `yaml:"work_dir"`
	ProtoPaths    []string `yaml:"proto_paths"`
	Files         []string `yaml:"files"`
	GoOut         string   `yaml:"go_out"`
	MutableOut    string   `yaml:"mutable_out"`
	GoOpt         string   `yaml:"go_opt"`
	MutableOpt    string   `yaml:"mutable_opt"`
	DescriptorOut string   `yaml:"descriptor_out"`
	ExtraArgs     []string `yaml:"extra_args"`
}

// ProtoRunner holds resolved toolchain binaries and module directories for running ProtoTargets.
type ProtoRunner struct {
	RootDir           string
	ProtocBin         string
	ProtocVersion     string
	ProtocGenGoBin    string
	ProtocGenMutable  string
	ModuleDirs        map[string]string
	Cache             *CacheStore
	Force             bool
	Concurrency       int
}

// RunTargets executes all proto targets grouped by Stage (ascending), running
// targets in the same stage concurrently via errgroup with SHA-256 incremental caching.
func (r *ProtoRunner) RunTargets(ctx context.Context, targets []ProtoTarget) error {
	if len(targets) == 0 {
		return nil
	}

	stageMap := make(map[int][]ProtoTarget)
	var stages []int
	for _, t := range targets {
		if _, ok := stageMap[t.Stage]; !ok {
			stages = append(stages, t.Stage)
		}
		stageMap[t.Stage] = append(stageMap[t.Stage], t)
	}
	slices.Sort(stages)

	limit := r.Concurrency
	if limit <= 0 {
		limit = 4
	}

	for _, st := range stages {
		group := stageMap[st]
		eg, egCtx := errgroup.WithContext(ctx)
		eg.SetLimit(limit)
		for _, target := range group {
			t := target
			eg.Go(func() error {
				return r.runSingleTarget(egCtx, t)
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
	}

	if r.Cache != nil {
		return r.Cache.Save()
	}
	return nil
}

func (r *ProtoRunner) runSingleTarget(ctx context.Context, t ProtoTarget) error {
	workDir := r.RootDir
	if t.WorkDir != "" {
		if filepath.IsAbs(t.WorkDir) {
			workDir = filepath.Clean(t.WorkDir)
		} else {
			workDir = filepath.Clean(filepath.Join(r.RootDir, filepath.FromSlash(t.WorkDir)))
		}
	}

	resolvedPaths := make([]string, 0, len(t.ProtoPaths))
	for _, p := range t.ProtoPaths {
		rp, err := ExpandProtoPath(ctx, p, workDir, r.RootDir, r.ModuleDirs)
		if err != nil {
			return fmt.Errorf("proto target %s: %w", t.Name, err)
		}
		resolvedPaths = append(resolvedPaths, rp)
	}

	protoFiles, err := expandProtoFiles(workDir, t.Files)
	if err != nil {
		return fmt.Errorf("proto target %s: %w", t.Name, err)
	}
	if len(protoFiles) == 0 {
		return fmt.Errorf("proto target %s: no .proto files matched %v in %s", t.Name, t.Files, workDir)
	}

	var expectedOutputs []string
	var descOutAbs string
	if t.DescriptorOut != "" {
		if filepath.IsAbs(t.DescriptorOut) {
			descOutAbs = filepath.Clean(t.DescriptorOut)
		} else {
			descOutAbs = filepath.Clean(filepath.Join(r.RootDir, filepath.FromSlash(t.DescriptorOut)))
		}
		expectedOutputs = append(expectedOutputs, descOutAbs)
	}

	// Predict expected Go outputs so deleting a generated file triggers regeneration.
	for _, f := range protoFiles {
		relProto := protoFileRelativeToPaths(f, resolvedPaths)
		if relProto == "" {
			continue
		}
		stem := strings.TrimSuffix(relProto, ".proto")
		if t.GoOut != "" {
			goOutDir := resolveDir(workDir, t.GoOut)
			expectedOutputs = append(expectedOutputs, filepath.Join(goOutDir, filepath.FromSlash(stem)+".pb.go"))
		}
		if t.MutableOut != "" {
			mutOutDir := resolveDir(workDir, t.MutableOut)
			expectedOutputs = append(expectedOutputs, filepath.Join(mutOutDir, filepath.FromSlash(stem)+"_mutable.pb.go"))
		}
	}

	cacheKey := "proto:" + t.Name
	hashPaths := append([]string{}, protoFiles...)
	hashPaths = append(hashPaths, resolvedPaths...)
	if r.ProtocGenMutable != "" {
		hashPaths = append(hashPaths, r.ProtocGenMutable)
	}
	meta := []string{
		"protoc:" + r.ProtocVersion,
		"go_out:" + t.GoOut,
		"mutable_out:" + t.MutableOut,
		"go_opt:" + t.GoOpt,
		"mutable_opt:" + t.MutableOpt,
		"desc_out:" + descOutAbs,
		"extra:" + strings.Join(t.ExtraArgs, " "),
	}

	digest, err := HashInputs(hashPaths, []string{".proto", ".exe", ""}, meta)
	if err != nil {
		return fmt.Errorf("hash proto target %s: %w", t.Name, err)
	}
	if !r.Force && r.Cache != nil && r.Cache.IsUpToDate(cacheKey, digest, expectedOutputs) {
		fmt.Printf("[proto] skip (up-to-date): %s\n", t.Name)
		return nil
	}

	args := make([]string, 0, len(resolvedPaths)+len(protoFiles)+10)
	if t.GoOut != "" {
		if r.ProtocGenGoBin != "" {
			args = append(args, "--plugin=protoc-gen-go="+r.ProtocGenGoBin)
		}
		goOutDir := resolveDir(workDir, t.GoOut)
		if err := os.MkdirAll(goOutDir, 0o755); err != nil {
			return fmt.Errorf("mkdir go_out %s: %w", goOutDir, err)
		}
		args = append(args, "--go_out="+goOutDir)
		goOpt := t.GoOpt
		if goOpt == "" {
			goOpt = "paths=source_relative"
		}
		args = append(args, "--go_opt="+goOpt)
	}
	if t.MutableOut != "" {
		if r.ProtocGenMutable != "" {
			args = append(args, "--plugin=protoc-gen-mutable="+r.ProtocGenMutable)
		}
		mutOutDir := resolveDir(workDir, t.MutableOut)
		if err := os.MkdirAll(mutOutDir, 0o755); err != nil {
			return fmt.Errorf("mkdir mutable_out %s: %w", mutOutDir, err)
		}
		args = append(args, "--mutable_out="+mutOutDir)
		mutOpt := t.MutableOpt
		if mutOpt == "" {
			mutOpt = "paths=source_relative"
		}
		args = append(args, "--mutable_opt="+mutOpt)
	}

	for _, rp := range resolvedPaths {
		args = append(args, "--proto_path="+rp)
	}
	if descOutAbs != "" {
		if err := os.MkdirAll(filepath.Dir(descOutAbs), 0o755); err != nil {
			return fmt.Errorf("mkdir descriptor dir %s: %w", filepath.Dir(descOutAbs), err)
		}
		args = append(args, "-o", descOutAbs)
	}
	args = append(args, t.ExtraArgs...)
	args = append(args, protoFiles...)

	fmt.Printf("[proto] generate: %s (%d files)\n", t.Name, len(protoFiles))
	cmd := exec.CommandContext(ctx, r.ProtocBin, args...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("protoc failed for %s: %w\n%s", t.Name, err, stderr.String())
	}

	if r.Cache != nil {
		r.Cache.Record(cacheKey, digest, expectedOutputs)
	}
	return nil
}

func resolveDir(workDir, dir string) string {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Clean(filepath.Join(workDir, filepath.FromSlash(dir)))
}

func expandProtoFiles(workDir string, patterns []string) ([]string, error) {
	seen := make(map[string]struct{})
	var files []string
	for _, pat := range patterns {
		fullPat := resolveDir(workDir, pat)
		matches, err := filepath.Glob(fullPat)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pat, err)
		}
		if len(matches) == 0 && fileExists(fullPat) {
			matches = []string{fullPat}
		}
		for _, m := range matches {
			clean := filepath.Clean(m)
			if _, ok := seen[clean]; ok {
				continue
			}
			if fileExists(clean) && strings.HasSuffix(strings.ToLower(clean), ".proto") {
				seen[clean] = struct{}{}
				files = append(files, clean)
			}
		}
	}
	slices.Sort(files)
	return files, nil
}

func protoFileRelativeToPaths(absFile string, protoPaths []string) string {
	cleanFile := filepath.Clean(absFile)
	best := ""
	for _, pp := range protoPaths {
		rel, err := filepath.Rel(filepath.Clean(pp), cleanFile)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		if best == "" || len(rel) < len(best) {
			best = rel
		}
	}
	return filepath.ToSlash(best)
}
