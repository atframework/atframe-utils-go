package main

import (
	"testing"
)

// TestGetGoBinPath 测试获取 Go bin 路径
func TestGetGoBinPath(t *testing.T) {
	binPath, err := GetGoBinPath()
	if err != nil {
		t.Fatalf("GetGoBinPath failed: %v", err)
	}

	if binPath == "" {
		t.Fatal("GetGoBinPath returned empty path")
	}

	t.Logf("Go bin path: %s", binPath)
}

func TestGetGoProtocPlugins(t *testing.T) {
	GetGoProtocPlugins()
}
