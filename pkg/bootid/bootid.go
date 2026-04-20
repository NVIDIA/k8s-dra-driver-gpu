// Package bootid reads the Linux kernel boot_id used to detect node reboots
// across kubelet plugin restarts.
package bootid

import (
	"os"
	"path/filepath"
	"strings"
)

const defaultBootIDPath = "/proc/sys/kernel/random/boot_id"

// bootIDPath is mutable for tests.
var bootIDPath = defaultBootIDPath

// CurrentBootID returns the current boot id.
func CurrentBootID() (string, error) {
	b, err := os.ReadFile(bootIDPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ReadStoredBootID returns the stored boot id.
// If the file does not exist, it returns ("", nil).
func ReadStoredBootID(dir, fileName string) (string, error) {
	p := filepath.Join(dir, fileName)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// WriteStoredBootID writes the stored boot id to the sidecar file, replacing
// any previous contents.
func WriteStoredBootID(dir, fileName, bootID string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, fileName)
	return os.WriteFile(p, []byte(strings.TrimSpace(bootID)+"\n"), 0o644)
}
