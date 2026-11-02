package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveFormer(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "sandesh")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "cache.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "narada")
	moveFormer(dir, "sandesh")
	if _, err := os.Stat(filepath.Join(dir, "cache.db")); err != nil {
		t.Fatalf("not moved: %v", err)
	}
	// once there, the new folder is never replaced
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	moveFormer(dir, "sandesh")
	if _, err := os.Stat(old); err != nil {
		t.Fatal("touched the old folder a second time")
	}
}
