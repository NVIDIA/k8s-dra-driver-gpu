package bootid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointBootIDMatchesCurrent(t *testing.T) {
	mockBootID := "beef-beef-beef-beef-beefbeef0001"

	dir := t.TempDir()
	path := filepath.Join(dir, "boot_id")
	if err := os.WriteFile(path, []byte(mockBootID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bootIDPath = path

	bootID, err := GetCurrentBootID()
	if err != nil {
		t.Fatal(err)
	}
	if bootID != mockBootID {
		t.Fatalf("expected boot ID to be '%s', got '%s'", mockBootID, bootID)
	}
}
