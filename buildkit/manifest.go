// Copyright 2026 atframework
package buildkit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Default tool versions aligned across atframework repositories.
const (
	DefaultProtocVersion       = "32.1"
	DefaultGoPluginVersion     = "v1.36.9"
	DefaultXresloaderVersion   = "2.23.4"
	DefaultAtdtoolVersion      = "1.0.6"
	DefaultJavaMinMajorVersion = 17
)

// Config represents the declarative atbuild.yaml configuration for a repository.
type Config struct {
	ProtocVersion     string        `yaml:"protoc_version"`
	CacheDir          string        `yaml:"cache_dir"`
	ToolsDir          string        `yaml:"tools_dir"`
	ToolsModFile      string        `yaml:"tools_modfile"`
	LintConfig        string        `yaml:"lint_config"`
	ProtoTargets      []ProtoTarget `yaml:"proto_targets"`
	StandaloneModules []string      `yaml:"standalone_modules"`
}

// LoadConfig loads atbuild.yaml from rootDir if it exists; otherwise returns defaults.
func LoadConfig(rootDir string) (*Config, error) {
	cfg := &Config{
		ProtocVersion: DefaultProtocVersion,
		CacheDir:      filepath.Join(".cache", "atbuild"),
		ToolsDir:      filepath.Join(".cache", "atbuild", "tools"),
	}

	cfgPath := filepath.Join(rootDir, "atbuild.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", cfgPath, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", cfgPath, err)
	}
	if cfg.ProtocVersion == "" {
		cfg.ProtocVersion = DefaultProtocVersion
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = filepath.Join(".cache", "atbuild")
	}
	if cfg.ToolsDir == "" {
		cfg.ToolsDir = filepath.Join(cfg.CacheDir, "tools")
	}
	return cfg, nil
}

// CacheEntry records the input digest and produced outputs of a cached build step.
type CacheEntry struct {
	Digest  string   `json:"digest"`
	Outputs []string `json:"outputs,omitempty"`
}

// CacheStore persists step digests in a JSON manifest for fast incremental builds.
type CacheStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]CacheEntry
}

// OpenCacheStore loads or initializes a CacheStore at path.
func OpenCacheStore(path string) (*CacheStore, error) {
	store := &CacheStore{
		path:    path,
		entries: make(map[string]CacheEntry),
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read cache manifest %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store.entries); err != nil {
		// Corrupted manifest should not block builds; start fresh.
		store.entries = make(map[string]CacheEntry)
	}
	return store, nil
}

// IsUpToDate returns true when the cached digest matches and every declared output still exists.
func (c *CacheStore) IsUpToDate(key, digest string, outputs []string) bool {
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if !ok || entry.Digest != digest {
		return false
	}

	checkList := outputs
	if len(checkList) == 0 {
		checkList = entry.Outputs
	}
	for _, out := range checkList {
		if out == "" {
			continue
		}
		if _, err := os.Stat(out); err != nil {
			return false
		}
	}
	return true
}

// Record updates a step entry in memory.
func (c *CacheStore) Record(key, digest string, outputs []string) {
	cleaned := make([]string, 0, len(outputs))
	for _, o := range outputs {
		if o != "" {
			cleaned = append(cleaned, filepath.Clean(o))
		}
	}
	slices.Sort(cleaned)
	cleaned = slices.Compact(cleaned)

	c.mu.Lock()
	c.entries[key] = CacheEntry{
		Digest:  digest,
		Outputs: cleaned,
	}
	c.mu.Unlock()
}

// Save atomically persists the manifest to disk.
func (c *CacheStore) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return fmt.Errorf("mkdir cache dir: %w", err)
	}
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache manifest: %w", err)
	}
	data = append(data, '\n')

	tmpPath := c.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp cache manifest: %w", err)
	}
	if err := os.Rename(tmpPath, c.path); err != nil {
		// Fallback for Windows transient rename locks.
		_ = os.Remove(c.path)
		if err2 := os.WriteFile(c.path, data, 0o644); err2 != nil {
			return fmt.Errorf("write cache manifest: %w", err2)
		}
		_ = os.Remove(tmpPath)
	}
	return nil
}

// HashInputs computes a deterministic SHA-256 digest over files/directories and extra metadata strings.
// For directories, only files matching allowedExts (or all files when allowedExts is empty) are hashed.
func HashInputs(paths []string, allowedExts []string, extra []string) (string, error) {
	extSet := make(map[string]struct{}, len(allowedExts))
	for _, ext := range allowedExts {
		extSet[strings.ToLower(ext)] = struct{}{}
	}

	fileSet := make(map[string]struct{})
	for _, raw := range paths {
		if raw == "" {
			continue
		}
		matches, err := filepath.Glob(raw)
		if err != nil || len(matches) == 0 {
			matches = []string{raw}
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return "", fmt.Errorf("stat %s: %w", m, err)
			}
			if !info.IsDir() {
				abs, err := filepath.Abs(m)
				if err != nil {
					abs = filepath.Clean(m)
				}
				fileSet[abs] = struct{}{}
				continue
			}
			err = filepath.WalkDir(m, func(p string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() {
					name := d.Name()
					if name == ".git" || name == ".cache" || name == ".venv" || name == "node_modules" {
						return filepath.SkipDir
					}
					return nil
				}
				if len(extSet) > 0 {
					if _, ok := extSet[strings.ToLower(filepath.Ext(p))]; !ok {
						return nil
					}
				}
				abs, err := filepath.Abs(p)
				if err != nil {
					abs = filepath.Clean(p)
				}
				fileSet[abs] = struct{}{}
				return nil
			})
			if err != nil {
				return "", fmt.Errorf("walk %s: %w", m, err)
			}
		}
	}

	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	slices.Sort(files)

	h := sha256.New()
	for _, s := range extra {
		_, _ = io.WriteString(h, "meta:")
		_, _ = io.WriteString(h, s)
		_, _ = io.WriteString(h, "\n")
	}

	for _, f := range files {
		_, _ = io.WriteString(h, "file:")
		_, _ = io.WriteString(h, filepath.ToSlash(f))
		_, _ = io.WriteString(h, "\n")
		fh, err := os.Open(f)
		if err != nil {
			return "", fmt.Errorf("open %s for hashing: %w", f, err)
		}
		_, copyErr := io.Copy(h, fh)
		closeErr := fh.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash %s: %w", f, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close %s: %w", f, closeErr)
		}
		_, _ = io.WriteString(h, "\n")
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
