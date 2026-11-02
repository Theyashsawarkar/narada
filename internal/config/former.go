package config

import (
	"os"
	"path/filepath"
)

// MoveFormer: narada was called sandesh up to v0.4, so its folders (mail
// cache, the WhatsApp link, settings) move to the new name the first time the
// command runs. Only main calls it, never a test.
func MoveFormer() {
	if os.Getenv("NARADA_HOME") != "" {
		return
	}
	p := DefaultPaths()
	for _, dir := range []string{p.Config, p.Data, p.State, p.Cache} {
		moveFormer(dir, "sandesh")
	}
}

func moveFormer(dir, old string) {
	from := filepath.Join(filepath.Dir(dir), old)
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if fi, err := os.Lstat(from); err != nil || !fi.IsDir() {
		return
	}
	_ = os.Rename(from, dir)
}
