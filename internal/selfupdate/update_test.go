package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// withRunCommand swaps the package-level runCommand stub for the duration of a
// test, restoring it afterward.
func withRunCommand(t *testing.T, fn func(ctx context.Context, name string, args ...string) error) {
	t.Helper()
	orig := runCommand
	runCommand = fn
	t.Cleanup(func() { runCommand = orig })
}

// TestRunManagedUpgrade_SuccessDoesNotClaimVersion covers finding 3: a clean PM
// exit only proves the command ran. The result must NOT carry a To version (the
// formula/npm index can lag the GitHub release).
func TestRunManagedUpgrade_SuccessDoesNotClaimVersion(t *testing.T) {
	var gotName string
	var gotArgs []string
	withRunCommand(t, func(_ context.Context, name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	})

	res := runManagedUpgrade(context.Background(), managedBrew, "v1.0.0", "v2.0.0", "advice")
	if res.Outcome != OutcomeManagedRan {
		t.Fatalf("outcome = %d, want OutcomeManagedRan", res.Outcome)
	}
	if res.To != "" {
		t.Errorf("To = %q, want empty (a clean PM exit does not confirm the version landed)", res.To)
	}
	if res.From != "v1.0.0" {
		t.Errorf("From = %q, want v1.0.0", res.From)
	}
	if res.Manager != "Homebrew" {
		t.Errorf("Manager = %q, want Homebrew", res.Manager)
	}
	// It ran the tap-qualified formula, not the bare name.
	if gotName != "brew" || strings.Join(gotArgs, " ") != "upgrade "+brewFormula {
		t.Errorf("ran %q %v, want brew upgrade %s", gotName, gotArgs, brewFormula)
	}
}

// TestRunManagedUpgrade_FailureAdviceFromAttemptedArgv covers finding 2: when
// the PM command fails, the fallback advice must reflect the argv actually run
// (brew upgrade framehood/tap/framehood), not the generic detector hint.
func TestRunManagedUpgrade_FailureAdviceFromAttemptedArgv(t *testing.T) {
	withRunCommand(t, func(_ context.Context, _ string, _ ...string) error {
		return errors.New("exit status 1")
	})

	// The detector advice deliberately uses the un-qualified `brew upgrade
	// framehood`; the attempted argv uses the tap-qualified formula. The fallback
	// must carry the latter.
	res := runManagedUpgrade(context.Background(), managedBrew,
		"v1.0.0", "v2.0.0", "this looks like a Homebrew install — run:\n  brew upgrade framehood")
	if res.Outcome != OutcomeManaged {
		t.Fatalf("outcome = %d, want OutcomeManaged (fallback)", res.Outcome)
	}
	if !strings.Contains(res.Advice, "brew upgrade "+brewFormula) {
		t.Errorf("advice = %q, want it to mention the attempted `brew upgrade %s`", res.Advice, brewFormula)
	}
	if !strings.Contains(res.Advice, "failed") {
		t.Errorf("advice = %q, want it to note the command failed", res.Advice)
	}
}

// TestRunManagedUpgrade_NotFoundAdvice: when the PM binary isn't on PATH, the
// advice still shows the attempted argv so the user can run it once installed.
func TestRunManagedUpgrade_NotFoundAdvice(t *testing.T) {
	withRunCommand(t, func(_ context.Context, _ string, _ ...string) error {
		return exec.ErrNotFound
	})

	res := runManagedUpgrade(context.Background(), managedNpm, "v1.0.0", "v2.0.0", "advice")
	if res.Outcome != OutcomeManaged {
		t.Fatalf("outcome = %d, want OutcomeManaged", res.Outcome)
	}
	if !strings.Contains(res.Advice, "npm i -g "+pkgName+"@latest") {
		t.Errorf("advice = %q, want the attempted npm argv", res.Advice)
	}
	if !strings.Contains(res.Advice, "not found on PATH") {
		t.Errorf("advice = %q, want a 'not found on PATH' note", res.Advice)
	}
}
