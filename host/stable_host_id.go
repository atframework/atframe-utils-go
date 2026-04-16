package libatframe_utils_redis_util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
)

// GetStableHostID 返回一个稳定的 8 位字符串。
// 同一台机器固定，不同机器大概率不同。
func GetStableHostID(version int32) string {
	var parts []string

	// 加入操作系统类型（防止不同平台同名主机冲突）
	parts = append(parts, runtime.GOOS)

	// 加入主机名
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		parts = append(parts, hostname)
	}

	// 加入第一个可用的 MAC 地址
	mac := getFirstMAC()
	if mac != "" {
		parts = append(parts, mac)
	}

	parts = append(parts, fmt.Sprintf("%d", version))

	// 拼接后哈希
	base := strings.Join(parts, "_")
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:])[:8]
}

// getFirstMAC 获取第一个有效的 MAC 地址（跨平台）
func getFirstMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		return iface.HardwareAddr.String()
	}
	return ""
}
