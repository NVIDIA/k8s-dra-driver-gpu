package bootid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "boot_id")
	want := "deadbeef-dead-dead-dead-deadbeef0001"
	if err := os.WriteFile(path, []byte(want+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := bootIDPath
	bootIDPath = path
	t.Cleanup(func() { bootIDPath = old })

	got, err := CurrentBootID()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Read() = %q, want %q", got, want)
	}
}

func TestReadWriteSidecar(t *testing.T) {
	dir := t.TempDir()
	const name = "node_boot_id"
	want := "abc-def-ghi"

	if err := WriteStoredBootID(dir, name, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStoredBootID(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ReadSidecar = %q, want %q", got, want)
	}

	empty, err := ReadStoredBootID(dir, "missing")
	if err != nil || empty != "" {
		t.Fatalf("missing file: got %q err=%v", empty, err)
	}
}

func TestReadSidecarTrim(t *testing.T) {
	dir := t.TempDir()
	name := "boot"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("trimmed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStoredBootID(dir, name)
	if err != nil || got != "trimmed" {
		t.Fatalf("got %q err=%v", got, err)
	}
}
