// Copyright 2026 atframework
package buildkit

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// EnsureProtoc locates or downloads protoc of the requested version.
// Resolution order:
//  1. ATBUILD_PROTOC environment variable
//  2. Cached binary under toolsDir/protoc/<version>/<os>-<arch>/[bin/]protoc(.exe)
//  3. System PATH (when version matches or version is empty)
//  4. Download official release zip (or ATBUILD_PROTOC_URL override)
func EnsureProtoc(ctx context.Context, version, toolsDir string) (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("ATBUILD_PROTOC")); envPath != "" {
		if fileExists(envPath) {
			return filepath.Clean(envPath), nil
		}
		return "", fmt.Errorf("ATBUILD_PROTOC points to non-existent file: %s", envPath)
	}

	if version == "" {
		version = DefaultProtocVersion
	}

	targetDir := filepath.Join(toolsDir, "protoc", version, runtime.GOOS+"-"+runtime.GOARCH)
	for _, candidate := range []string{
		filepath.Join(targetDir, "bin", BinName("protoc")),
		filepath.Join(targetDir, BinName("protoc")),
	} {
		if fileExists(candidate) {
			return filepath.Clean(candidate), nil
		}
	}

	if pathBin, err := exec.LookPath("protoc"); err == nil {
		if version == "" || protocMatchesVersion(ctx, pathBin, version) {
			return filepath.Clean(pathBin), nil
		}
	}

	assetURL := strings.TrimSpace(os.Getenv("ATBUILD_PROTOC_URL"))
	if assetURL == "" {
		var err error
		assetURL, err = protocAssetURL(version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return "", err
		}
	}

	fmt.Printf("[atbuild] downloading protoc %s from %s\n", version, assetURL)
	data, err := downloadWithRetry(ctx, assetURL, 3)
	if err != nil {
		return "", fmt.Errorf("download protoc %s: %w", version, err)
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", targetDir, err)
	}
	if err := extractZipBytes(data, targetDir); err != nil {
		return "", fmt.Errorf("extract protoc %s: %w", version, err)
	}

	for _, candidate := range []string{
		filepath.Join(targetDir, "bin", BinName("protoc")),
		filepath.Join(targetDir, BinName("protoc")),
	} {
		if fileExists(candidate) {
			if runtime.GOOS != "windows" {
				_ = os.Chmod(candidate, 0o755)
			}
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("protoc binary not found after extracting to %s", targetDir)
}

func protocMatchesVersion(ctx context.Context, binPath, want string) bool {
	out, err := exec.CommandContext(ctx, binPath, "--version").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), want)
}

func protocAssetURL(version, osName, arch string) (string, error) {
	base := "https://github.com/protocolbuffers/protobuf/releases/download/v" + version + "/"
	switch osName {
	case "linux":
		switch arch {
		case "amd64":
			return base + "protoc-" + version + "-linux-x86_64.zip", nil
		case "arm64":
			return base + "protoc-" + version + "-linux-aarch_64.zip", nil
		}
	case "darwin":
		return base + "protoc-" + version + "-osx-universal_binary.zip", nil
	case "windows":
		if arch == "amd64" || arch == "arm64" {
			return base + "protoc-" + version + "-win64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform %s/%s for protoc %s", osName, arch, version)
}

// AssetSpec describes an external release artifact to download and cache.
type AssetSpec struct {
	Name        string
	Version     string
	URL         string
	ToolsDir    string
	InstallPath string // Optional destination path to copy the resolved executable/jar
}

// EnsureDownloadAsset downloads, optionally extracts (.zip/.tar.gz), caches, and
// returns the path to the primary artifact.
func EnsureDownloadAsset(ctx context.Context, spec AssetSpec) (string, error) {
	cacheDir := filepath.Join(spec.ToolsDir, spec.Name, spec.Version)
	marker := filepath.Join(cacheDir, ".ready")
	if fileExists(marker) {
		if cachedBytes, err := os.ReadFile(marker); err == nil {
			cachedPath := strings.TrimSpace(string(cachedBytes))
			if fileExists(cachedPath) {
				if err := syncInstalledAsset(cachedPath, spec.InstallPath); err != nil {
					return "", err
				}
				if spec.InstallPath != "" {
					return filepath.Clean(spec.InstallPath), nil
				}
				return filepath.Clean(cachedPath), nil
			}
		}
	}

	fmt.Printf("[atbuild] downloading %s %s from %s\n", spec.Name, spec.Version, spec.URL)
	data, err := downloadWithRetry(ctx, spec.URL, 3)
	if err != nil {
		return "", fmt.Errorf("download %s %s: %w", spec.Name, spec.Version, err)
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", cacheDir, err)
	}

	lowerURL := strings.ToLower(spec.URL)
	var primaryPath string
	switch {
	case strings.HasSuffix(lowerURL, ".zip"):
		if err := extractZipBytes(data, cacheDir); err != nil {
			return "", fmt.Errorf("extract zip %s: %w", spec.Name, err)
		}
		primaryPath, err = findPrimaryBinary(cacheDir, spec.Name)
		if err != nil {
			return "", err
		}
	case strings.HasSuffix(lowerURL, ".tar.gz") || strings.HasSuffix(lowerURL, ".tgz"):
		if err := extractTarGzBytes(data, cacheDir); err != nil {
			return "", fmt.Errorf("extract tar.gz %s: %w", spec.Name, err)
		}
		primaryPath, err = findPrimaryBinary(cacheDir, spec.Name)
		if err != nil {
			return "", err
		}
	default:
		baseName := filepath.Base(spec.URL)
		primaryPath = filepath.Join(cacheDir, baseName)
		if err := os.WriteFile(primaryPath, data, 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", primaryPath, err)
		}
	}

	if runtime.GOOS != "windows" && !strings.HasSuffix(strings.ToLower(primaryPath), ".jar") {
		_ = os.Chmod(primaryPath, 0o755)
	}
	_ = os.WriteFile(marker, []byte(primaryPath+"\n"), 0o644)

	if err := syncInstalledAsset(primaryPath, spec.InstallPath); err != nil {
		return "", err
	}
	if spec.InstallPath != "" {
		return filepath.Clean(spec.InstallPath), nil
	}
	return filepath.Clean(primaryPath), nil
}

func syncInstalledAsset(src, dst string) error {
	if dst == "" {
		return nil
	}
	return CopyFile(src, dst)
}

func findPrimaryBinary(dir, appName string) (string, error) {
	lowerApp := strings.ToLower(appName)
	var fallback string
	var matched string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		base := strings.ToLower(d.Name())
		if base == ".ready" {
			return nil
		}
		if base == strings.ToLower(BinName(appName)) {
			matched = p
			return nil
		}
		if strings.Contains(base, lowerApp) && (strings.HasSuffix(base, ".exe") || strings.HasSuffix(base, ".jar") || !strings.Contains(base, ".")) {
			if fallback == "" {
				fallback = p
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if matched != "" {
		return matched, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("no binary matching %q found in %s", appName, dir)
}

var javaVersionRe = regexp.MustCompile(`version "(\d+)(?:\.(\d+))?`)

// ResolveJava locates java on PATH (or JAVA_HOME) and validates its major version >= minMajor.
func ResolveJava(ctx context.Context, minMajor int) (string, int, error) {
	javaBin := ""
	if jh := strings.TrimSpace(os.Getenv("JAVA_HOME")); jh != "" {
		candidate := filepath.Join(jh, "bin", BinName("java"))
		if fileExists(candidate) {
			javaBin = candidate
		}
	}
	if javaBin == "" {
		p, err := exec.LookPath("java")
		if err != nil {
			return "", 0, fmt.Errorf("java not found in JAVA_HOME or PATH (require JDK >= %d)", minMajor)
		}
		javaBin = p
	}

	out, err := exec.CommandContext(ctx, javaBin, "-version").CombinedOutput()
	if err != nil {
		return "", 0, fmt.Errorf("run %s -version: %w\n%s", javaBin, err, string(out))
	}
	m := javaVersionRe.FindStringSubmatch(string(out))
	if len(m) < 2 {
		return "", 0, fmt.Errorf("unable to parse java version from output:\n%s", string(out))
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return "", 0, fmt.Errorf("parse java major version %q: %w", m[1], err)
	}
	if major == 1 && len(m) >= 3 && m[2] != "" {
		if legacy, convErr := strconv.Atoi(m[2]); convErr == nil {
			major = legacy
		}
	}
	if major < minMajor {
		return "", major, fmt.Errorf("java version %d is less than required minimum %d (%s)", major, minMajor, javaBin)
	}
	return filepath.Clean(javaBin), major, nil
}

// PythonRunner abstracts running Python scripts through uv (preferred) or a local venv.
type PythonRunner struct {
	Command   string
	BaseArgs  []string
	UsingUV   bool
	VenvDir   string
	PythonBin string
}

// ResolvePythonRunner prefers `uv run` when pyproject.toml and uv are available;
// otherwise ensures a standard venv under <projectRoot>/.venv (or PYTHON_VENV_PATH).
func ResolvePythonRunner(ctx context.Context, projectRoot, requirementsFile, extraGroup string) (*PythonRunner, error) {
	pyprojectPath := filepath.Join(projectRoot, "pyproject.toml")
	if fileExists(pyprojectPath) {
		if uvBin, err := exec.LookPath("uv"); err == nil {
			args := []string{"run", "--project", projectRoot}
			if extraGroup != "" {
				args = append(args, "--group", extraGroup)
			}
			args = append(args, "python")
			return &PythonRunner{
				Command:   uvBin,
				BaseArgs:  args,
				UsingUV:   true,
				VenvDir:   filepath.Join(projectRoot, ".venv"),
				PythonBin: uvBin,
			}, nil
		}
	}

	venvDir := strings.TrimSpace(os.Getenv("PYTHON_VENV_PATH"))
	if venvDir == "" {
		venvDir = filepath.Join(projectRoot, ".venv")
	}

	pythonExe := filepath.Join(venvDir, "bin", "python")
	if runtime.GOOS == "windows" {
		pythonExe = filepath.Join(venvDir, "Scripts", "python.exe")
	}

	if !fileExists(pythonExe) {
		sysPython, err := findSystemPython()
		if err != nil {
			return nil, err
		}
		fmt.Printf("[atbuild] creating Python venv at %s\n", venvDir)
		cmd := exec.CommandContext(ctx, sysPython, "-m", "venv", venvDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("create venv %s: %w", venvDir, err)
		}
		if requirementsFile != "" && fileExists(requirementsFile) {
			pipCmd := exec.CommandContext(ctx, pythonExe, "-m", "pip", "install", "-r", requirementsFile)
			pipCmd.Stdout = os.Stdout
			pipCmd.Stderr = os.Stderr
			if err := pipCmd.Run(); err != nil {
				return nil, fmt.Errorf("pip install -r %s: %w", requirementsFile, err)
			}
		}
	}

	return &PythonRunner{
		Command:   pythonExe,
		UsingUV:   false,
		VenvDir:   venvDir,
		PythonBin: pythonExe,
	}, nil
}

// ExecCmd constructs an exec.Cmd to run a Python script or module (`-m ...`).
func (r *PythonRunner) ExecCmd(ctx context.Context, scriptArgs ...string) *exec.Cmd {
	allArgs := make([]string, 0, len(r.BaseArgs)+len(scriptArgs))
	allArgs = append(allArgs, r.BaseArgs...)
	allArgs = append(allArgs, scriptArgs...)
	return exec.CommandContext(ctx, r.Command, allArgs...)
}

func findSystemPython() (string, error) {
	candidates := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", errors.New("neither uv nor python3/python found on PATH")
}

func downloadWithRetry(ctx context.Context, url string, attempts int) ([]byte, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			backoff := time.Duration(1<<uint(i)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && readErr == nil {
			return body, nil
		}
		if readErr != nil {
			lastErr = readErr
		} else {
			lastErr = fmt.Errorf("HTTP %s", resp.Status)
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			break
		}
	}
	return nil, lastErr
}

func extractZipBytes(data []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		target := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), cleanDest) {
			return fmt.Errorf("illegal zip path: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func extractTarGzBytes(data []byte, dest string) error {
	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gzr.Close() }()
	tr := tar.NewReader(gzr)
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(target), cleanDest) {
			return fmt.Errorf("illegal tar path: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode)
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
