package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	build_setting "github.com/atframework/atframe-utils-go/build_setting"
)

type Config struct {
	AppName      string
	AppVersion   string
	URL          string
	DownloadPath string
	InstallPath  string
	SettingsFile string
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.AppName, "app-name", "", "Application name")
	flag.StringVar(&cfg.AppVersion, "app-version", "", "Application version")
	flag.StringVar(&cfg.URL, "url", "", "Download URL")
	flag.StringVar(&cfg.DownloadPath, "download-path", "./", "Download path")
	flag.StringVar(&cfg.InstallPath, "install-path", "", "Install path")
	flag.StringVar(&cfg.SettingsFile, "settings-file", "", "Settings file path")
	flag.Parse()

	if cfg.AppName == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: app-name is required\n")
		os.Exit(1)
	}
	if cfg.AppVersion == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: app-version is required\n")
		os.Exit(1)
	}
	if cfg.URL == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: url is required\n")
		os.Exit(1)
	}

	return cfg
}

// downloadFile 从 URL 下载文件到指定路径
func downloadFile(url, filePath string) error {
	data := mustHTTPGet(url)

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func mustHTTPGet(targetURL string) []byte {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "download failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "bad status: %s\n", resp.Status)
		os.Exit(1)
	}
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "read body: %v\n", err)
		os.Exit(1)
	}
	return buf.Bytes()
}

// extractZip 解压 ZIP 文件
func extractZip(zipPath, destDir string) ([]string, error) {
	fmt.Printf("📦 Extracting ZIP: %s to %s\n", zipPath, destDir)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip: %w", err)
	}
	defer reader.Close()

	var extractedFiles []string
	for _, file := range reader.File {
		filePath := filepath.Join(destDir, file.Name)

		if file.FileInfo().IsDir() {
			os.MkdirAll(filePath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}

		src, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open file in zip: %w", err)
		}

		dst, err := os.Create(filePath)
		if err != nil {
			src.Close()
			return nil, fmt.Errorf("failed to create extracted file: %w", err)
		}

		if _, err := io.Copy(dst, src); err != nil {
			src.Close()
			dst.Close()
			return nil, fmt.Errorf("failed to extract file: %w", err)
		}

		src.Close()
		dst.Close()

		// 记录提取的文件
		if !file.FileInfo().IsDir() {
			extractedFiles = append(extractedFiles, filePath)
		}
	}

	return extractedFiles, nil
}

// extractTarGz 解压 TAR.GZ 文件
func extractTarGz(tarGzPath, destDir string) ([]string, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	file, err := os.Open(tarGzPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open tar.gz: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	var extractedFiles []string

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar: %w", err)
		}

		filePath := filepath.Join(destDir, header.Name)

		if header.Typeflag == tar.TypeDir {
			os.MkdirAll(filePath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}

		dst, err := os.Create(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to create extracted file: %w", err)
		}

		if _, err := io.Copy(dst, tarReader); err != nil {
			dst.Close()
			return nil, fmt.Errorf("failed to extract file: %w", err)
		}

		dst.Close()
		extractedFiles = append(extractedFiles, filePath)
	}

	return extractedFiles, nil
}

// getExtractedTools 获取解压后的可执行文件
func getExtractedTools(extractedFiles []string, appName string) []string {
	var tools []string

	// 查找与应用名相关的可执行文件
	for _, file := range extractedFiles {
		baseName := filepath.Base(file)

		// Windows 可执行文件
		if strings.HasSuffix(file, ".exe") {
			tools = append(tools, file)
			continue
		}

		// 检查文件名是否包含应用名
		if strings.Contains(strings.ToLower(baseName), strings.ToLower(appName)) {
			tools = append(tools, file)
		}
	}

	return tools
}

// copyToolToInstallPath 复制工具到安装路径并去掉版本信息
func copyToolToInstallPath(srcPath, installPath, appName, appVersion string) (string, error) {
	if installPath == "" {
		return srcPath, nil
	}

	if err := os.MkdirAll(installPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create install directory: %w", err)
	}

	srcBaseName := filepath.Base(srcPath)

	// 步骤1: 先复制文件到安装路径（使用原始文件名）
	tempDestPath := filepath.Join(installPath, srcBaseName)

	// 使用局部作用域确保文件在复制后立即关闭
	{
		src, err := os.Open(srcPath)
		if err != nil {
			return "", fmt.Errorf("failed to open source file: %w", err)
		}
		defer src.Close()

		dst, err := os.Create(tempDestPath)
		if err != nil {
			return "", fmt.Errorf("failed to create destination file: %w", err)
		}
		defer dst.Close()

		if _, err := io.Copy(dst, src); err != nil {
			return "", fmt.Errorf("failed to copy file: %w", err)
		}

		// 保持执行权限
		if info, err := os.Stat(srcPath); err == nil {
			os.Chmod(tempDestPath, info.Mode())
		}
	} // 确保文件在这里完全关闭

	// 步骤2: 在目标路径中去掉版本号，重命名文件
	destName := removeVersionFromFileName(srcBaseName, appName, appVersion)
	destPath := filepath.Join(installPath, destName)

	// 如果文件名需要修改（版本号被移除了）
	if tempDestPath != destPath {

		// 使用复制+删除替代 os.Rename，避免 Windows 文件锁定问题
		var lastErr error
		maxRetries := 5
		for i := 0; i < maxRetries; i++ {
			// 如果目标文件已存在，先删除
			if _, err := os.Stat(destPath); err == nil {
				os.Remove(destPath)
			}

			// 复制文件到新名字
			src, err := os.Open(tempDestPath)
			if err != nil {
				lastErr = err
				if i < maxRetries-1 {
					fmt.Printf(" Copy failed (open source), retrying... (attempt %d/%d): %v\n", i+1, maxRetries, err)
					time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
					continue
				}
				break
			}

			dst, err := os.Create(destPath)
			if err != nil {
				src.Close()
				lastErr = err
				if i < maxRetries-1 {
					fmt.Printf(" Copy failed (create dest), retrying... (attempt %d/%d): %v\n", i+1, maxRetries, err)
					time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
					continue
				}
				break
			}

			_, copyErr := io.Copy(dst, src)
			src.Close()
			dst.Close()

			if copyErr != nil {
				lastErr = copyErr
				if i < maxRetries-1 {
					fmt.Printf(" Copy failed (content), retrying... (attempt %d/%d): %v\n", i+1, maxRetries, copyErr)
					time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
					continue
				}
				break
			}

			// 保持执行权限
			if info, err := os.Stat(tempDestPath); err == nil {
				os.Chmod(destPath, info.Mode())
			}

			// 等待一小段时间确保文件句柄完全释放
			time.Sleep(50 * time.Millisecond)

			// 删除原文件
			if err := os.Remove(tempDestPath); err != nil {
				// 删除失败不算致命错误，只是警告
				fmt.Printf("⚠️  Warning: failed to remove temp file: %v\n", err)
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			return "", fmt.Errorf("failed to copy file to new name after %d retries: %w", maxRetries, lastErr)
		}
	}

	return destPath, nil
}

// removeVersionFromFileName 从文件名中去掉版本信息
func removeVersionFromFileName(fileName, appName, appVersion string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(appVersion))
	result := re.ReplaceAllString(fileName, "")

	result = strings.ReplaceAll(result, "-.", ".")
	result = strings.ReplaceAll(result, "_.", ".")
	result = strings.ReplaceAll(result, "--", "-")
	result = strings.ReplaceAll(result, "__", "_")

	// 清理末尾的分隔符
	result = strings.TrimSuffix(result, "-")
	result = strings.TrimSuffix(result, "_")

	return result
}

// InstallToolConfig 安装工具的配置信息
type InstallToolConfig struct {
	AppName      string
	AppVersion   string
	DownloadURL  string
	DownloadPath string
	InstallPath  string
	SettingsFile string
}

// checkToolInstalled 检查工具是否已安装且版本匹配
// 返回: (已安装的路径, 是否已安装, error)
func checkToolValid(cfg InstallToolConfig) (string, bool, error) {
	// 优先检查 build-settings 中的工具信息
	if cfg.SettingsFile != "" {
		manager, err := build_setting.BuildManagerLoad(cfg.SettingsFile)
		if err == nil {
			// 尝试从 settings 中获取已安装的工具信息
			if toolInfo, err := manager.GetTool(cfg.AppName); err == nil && toolInfo != nil {
				// 先检查工具文件是否存在
				if s, err := os.Stat(toolInfo.Path); err == nil && s.Size() > 0 {
					// 文件存在，再检查版本是否匹配
					if toolInfo.Version == cfg.AppVersion {
						fmt.Printf("Tool '%s' version %s already installed at: %s (from settings)\n",
							cfg.AppName, cfg.AppVersion, toolInfo.Path)
						return toolInfo.Path, true, nil
					} else {
						fmt.Printf("Tool '%s' version mismatch (installed: %s, required: %s), updating...\n",
							cfg.AppName, toolInfo.Version, cfg.AppVersion)
					}
				} else {
					fmt.Printf("!! Tool '%s' registered in settings but file not found at: %s, reinstalling...\n",
						cfg.AppName, toolInfo.Path)
				}
			}
		}
	}

	return "", false, nil
}

// installTool 主要业务逻辑：完整的工具安装流程
// 该函数可被 main 和测试用例调用
func installTool(cfg InstallToolConfig) ([]string, error) {
	// 步骤1: 下载文件
	downloadedFile := filepath.Join(cfg.DownloadPath, filepath.Base(cfg.DownloadURL))
	if err := downloadFile(cfg.DownloadURL, downloadedFile); err != nil {
		if _, err := os.Stat(downloadedFile); err == nil {
			os.Remove(downloadedFile)
		}
		return nil, fmt.Errorf("download failed: %w", err)
	}

	// 这里应该是如果有安装目录把下载的文件直接放进去

	// 步骤2: 检查是否是压缩包并解压
	toolPaths, err := extractAndGetTools(downloadedFile, cfg.DownloadPath, cfg.AppName)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	if len(toolPaths) == 0 {
		return nil, fmt.Errorf("no tools found after extraction")
	}

	// 步骤3: 复制到安装路径并更新 build-settings
	var installedPaths []string
	for _, toolPath := range toolPaths {
		installPath := filepath.Join(cfg.InstallPath, cfg.AppName)
		installedPath, err := copyToolToInstallPath(toolPath, installPath, cfg.AppName, cfg.AppVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to copy tool: %w", err)
		}

		fmt.Printf("✅ Tool '%s' installed successfully at: %s\n", cfg.AppName, installedPath)
		installedPaths = append(installedPaths, installedPath)

		// hard code 简单处理同名程序放进配置
		if !strings.Contains(filepath.Base(toolPath), cfg.AppName) {
			continue
		}

		// 步骤4: 更新 build-settings
		if err := updateBuildSettingsForTool(InstallToolConfig{
			AppName:      cfg.AppName,
			AppVersion:   cfg.AppVersion,
			DownloadPath: cfg.DownloadPath,
			SettingsFile: cfg.SettingsFile,
		}, installedPath); err != nil {
			return nil, fmt.Errorf("failed to update build settings: %w", err)
		}
	}

	return installedPaths, nil
}

// extractAndGetTools 处理文件解压并获取工具列表
func extractAndGetTools(downloadedFile, downloadPath, appName string) ([]string, error) {
	var toolPaths []string
	lowerFileName := strings.ToLower(downloadedFile)

	appDownloadPath := filepath.Join(downloadPath, appName)
	if strings.HasSuffix(lowerFileName, ".zip") {
		extracted, err := extractZip(downloadedFile, appDownloadPath)
		if err != nil {
			return nil, fmt.Errorf("ZIP extraction failed: %w", err)
		}
		toolPaths = getExtractedTools(extracted, appName)
	} else if strings.HasSuffix(lowerFileName, ".tar.gz") {
		extracted, err := extractTarGz(downloadedFile, appDownloadPath)
		if err != nil {
			return nil, fmt.Errorf("TAR.GZ extraction failed: %w", err)
		}
		toolPaths = getExtractedTools(extracted, appName)
	} else {
		// 非压缩包文件（如.jar）
		toolPaths = []string{downloadedFile}
	}

	return toolPaths, nil
}

// updateBuildSettingsForTool 更新 build-settings（分离出来便于测试）
func updateBuildSettingsForTool(cfg InstallToolConfig, toolPath string) error {
	var manager build_setting.BuildMananger
	var err error

	manager, err = build_setting.BuildManagerLoad(cfg.SettingsFile)
	if err != nil {
		return fmt.Errorf("failed to load build manager from settings file: %w", err)
	}

	// if err = manager.SetDocDir(cfg.DownloadPath); err != nil {
	// 	return fmt.Errorf("set doc dir failed: %w", err)
	// }

	if err := manager.SetTool(cfg.AppName, cfg.AppVersion, toolPath); err != nil {
		return fmt.Errorf("failed to set tool in build settings: %w", err)
	}

	return nil
}

func main() {
	cfg := parseFlags()

	// 调用主业务逻辑函数
	installCfg := InstallToolConfig{
		AppName:      cfg.AppName,
		AppVersion:   cfg.AppVersion,
		DownloadURL:  cfg.URL,
		DownloadPath: cfg.DownloadPath,
		InstallPath:  cfg.InstallPath,
		SettingsFile: cfg.SettingsFile,
	}

	// 步骤1: 检查工具是否已安装
	installedPath, alreadyInstalled, err := checkToolValid(installCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Check failed: %v\n", err)
		os.Exit(1)
	}

	if alreadyInstalled {
		fmt.Printf("✅ Installation check completed, tool already installed at: %s\n", installedPath)
		return
	}

	// 步骤2: 执行安装
	_, err = installTool(installCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Installation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Installation completed successfully!\n")
}
