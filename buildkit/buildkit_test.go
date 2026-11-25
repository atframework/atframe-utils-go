// Copyright 2026 atframework
package buildkit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheStoreAndHashInputs(t *testing.T) {
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "sample.proto")
	outFile := filepath.Join(tmp, "sample.pb.go")
	if err := os.WriteFile(srcFile, []byte("syntax = \"proto3\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outFile, []byte("package sample\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := OpenCacheStore(filepath.Join(tmp, "cache", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	digest1, err := HashInputs([]string{srcFile}, []string{".proto"}, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if store.IsUpToDate("step1", digest1, []string{outFile}) {
		t.Fatal("expected cache miss before recording")
	}

	store.Record("step1", digest1, []string{outFile})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenCacheStore(filepath.Join(tmp, "cache", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsUpToDate("step1", digest1, []string{outFile}) {
		t.Fatal("expected cache hit after reload")
	}

	// Deleting generated output must invalidate the cache hit.
	if err := os.Remove(outFile); err != nil {
		t.Fatal(err)
	}
	if reloaded.IsUpToDate("step1", digest1, []string{outFile}) {
		t.Fatal("expected cache miss when output file is missing")
	}

	// Modifying input must change digest.
	if err := os.WriteFile(srcFile, []byte("syntax = \"proto3\";\nmessage A {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest2, err := HashInputs([]string{srcFile}, []string{".proto"}, []string{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if digest1 == digest2 {
		t.Fatal("expected digest to change after input modification")
	}
}

func TestExpandProtoPath(t *testing.T) {
	ctx := context.Background()
	modDirs := map[string]string{
		"github.com/atframework/libatbus-go": filepath.Join("C:", "modcache", "libatbus-go@v1.0.0"),
	}

	got, err := ExpandProtoPath(ctx, "mod:github.com/atframework/libatbus-go/protocol", "/work", "/root", modDirs)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(filepath.Join("C:", "modcache", "libatbus-go@v1.0.0", "protocol"))
	if got != want {
		t.Fatalf("ExpandProtoPath got %q, want %q", got, want)
	}

	relGot, err := ExpandProtoPath(ctx, "protocol/public", filepath.Join("C:", "repo"), filepath.Join("C:", "repo"), modDirs)
	if err != nil {
		t.Fatal(err)
	}
	relWant := filepath.Clean(filepath.Join("C:", "repo", "protocol", "public"))
	if relGot != relWant {
		t.Fatalf("relative ExpandProtoPath got %q, want %q", relGot, relWant)
	}
}

func TestLoadConfigDefaultAndFile(t *testing.T) {
	tmp := t.TempDir()
	cfg, err := LoadConfig(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProtocVersion != DefaultProtocVersion {
		t.Fatalf("default protoc version got %q, want %q", cfg.ProtocVersion, DefaultProtocVersion)
	}

	yamlContent := "protoc_version: \"32.1\"\nproto_targets:\n  - name: bus\n    work_dir: protocol\n    files:\n      - \"*.proto\"\n"
	if err := os.WriteFile(filepath.Join(tmp, "atbuild.yaml"), []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := LoadConfig(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg2.ProtoTargets) != 1 || cfg2.ProtoTargets[0].Name != "bus" {
		t.Fatalf("unexpected parsed targets: %+v", cfg2.ProtoTargets)
	}
}
