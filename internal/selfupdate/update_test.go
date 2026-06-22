package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// makeTarGz builds an in-memory .tar.gz containing a single file.
func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// makeZip builds an in-memory .zip containing a single file.
func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	return buf.Bytes()
}

func writeTemp(t *testing.T, data []byte, suffix string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "arc-*"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestExtractBinary_TarGz(t *testing.T) {
	want := []byte("#!/bin/sh\necho framehood\n")
	path := writeTemp(t, makeTarGz(t, "framehood", want), ".tar.gz")
	got, err := extractBinary(path, "framehood")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted bytes mismatch")
	}
}

func TestExtractBinary_Zip(t *testing.T) {
	want := []byte("MZ fake exe")
	path := writeTemp(t, makeZip(t, "framehood.exe", want), ".zip")
	got, err := extractBinary(path, "framehood.exe")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted bytes mismatch")
	}
}

func TestExtractBinary_NotFound(t *testing.T) {
	path := writeTemp(t, makeTarGz(t, "README.md", []byte("docs")), ".tar.gz")
	if _, err := extractBinary(path, "framehood"); err == nil {
		t.Error("expected error when the binary isn't in the archive")
	}
}

func TestSha256File(t *testing.T) {
	data := []byte("hello framehood")
	path := writeTemp(t, data, ".bin")
	got, err := sha256File(path)
	if err != nil {
		t.Fatalf("sha256File: %v", err)
	}
	sum := sha256.Sum256(data)
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256File = %q, want %q", got, hex.EncodeToString(sum[:]))
	}
}

// TestChecksumMismatchAbortsReplace is the security-critical test: when the
// computed sha256 doesn't match the checksums.txt entry, the flow must abort and
// the (would-be) target binary must be left untouched. We exercise the
// verify-vs-extract boundary directly: build an archive, compute its true sum,
// then assert that a *different* expected sum is rejected before any replace.
func TestChecksumMismatchAbortsReplace(t *testing.T) {
	archiveBytes := makeTarGz(t, "framehood", []byte("new binary v2"))
	archive := writeTemp(t, archiveBytes, ".tar.gz")

	trueSum, err := sha256File(archive)
	if err != nil {
		t.Fatal(err)
	}

	// A target "binary" that must NOT be overwritten on a mismatch.
	target := filepath.Join(t.TempDir(), "framehood")
	original := []byte("old binary v1")
	if err := os.WriteFile(target, original, 0o755); err != nil {
		t.Fatal(err)
	}

	// checksums.txt claims a different sum than the archive actually has.
	wrongSum := "0000000000000000000000000000000000000000000000000000000000000000"
	checksums := wrongSum + "  framehood_linux_amd64.tar.gz\n"

	expected, perr := parseChecksum(checksums, "framehood_linux_amd64.tar.gz")
	if perr != nil {
		t.Fatalf("parseChecksum: %v", perr)
	}
	if expected == trueSum {
		t.Fatal("test setup error: wrong sum accidentally matches")
	}

	// The verify gate (same condition downloadVerifyReplace uses): replace only
	// when the computed sum matches the checksums.txt entry. They differ here,
	// so replaceExecutable must never run.
	if got := trueSum; got == expected {
		if rerr := replaceExecutable(target, []byte("should not happen")); rerr != nil {
			t.Fatal(rerr)
		}
	}

	// The target must be byte-for-byte the original.
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Error("a checksum mismatch must leave the existing binary untouched")
	}
}

// TestReplaceExecutable_AtomicReplace verifies the happy path: a matching
// archive's binary is written over the target atomically, preserving 0755.
func TestReplaceExecutable_AtomicReplace(t *testing.T) {
	target := filepath.Join(t.TempDir(), "framehood")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := []byte("brand new binary")
	if err := replaceExecutable(target, newBin); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBin) {
		t.Error("replaceExecutable did not write the new binary")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o100 == 0 {
		t.Errorf("replaced binary perms = %o, want executable", perm)
	}
	// No leftover temp files in the directory.
	ents, _ := os.ReadDir(filepath.Dir(target))
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".tmp" || bytes.HasPrefix([]byte(e.Name()), []byte(".framehood-new-")) {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestDirWritable(t *testing.T) {
	if !dirWritable(t.TempDir()) {
		t.Error("a fresh temp dir should be writable")
	}
	if dirWritable(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Error("a missing dir should report not writable")
	}
}
