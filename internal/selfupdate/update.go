package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// maxDownloadBytes caps a single archive download so a hostile/oversized
// response can't fill the disk. Generous for a stripped Go binary archive.
const maxDownloadBytes = 256 << 20 // 256 MiB

// userAgent identifies the client to GitHub (the API rejects empty UAs).
const userAgent = "framehood-cli-selfupdate"

// httpClient is used for all release traffic, with a sane overall timeout.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// Outcome describes what Upgrade did.
type Outcome int

const (
	OutcomeUpToDate Outcome = iota // already on the latest version
	OutcomeUpgraded                // binary was self-replaced
	OutcomeManaged                 // managed install — advised, not replaced
)

// Result is the outcome of an Upgrade call.
type Result struct {
	Outcome Outcome
	From    string // current version
	To      string // latest version (or current, when up-to-date)
	Advice  string // for OutcomeManaged: the command/message to show the user
}

// LatestVersion resolves the latest release tag (e.g. "v1.2.3") via the GitHub
// releases API. HTTPS, no token.
func LatestVersion(ctx context.Context) (string, error) {
	url := "https://api.github.com/repos/" + Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases API returned HTTP %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return "", fmt.Errorf("decode release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return "", fmt.Errorf("github release has no tag_name")
	}
	return rel.TagName, nil
}

// Upgrade resolves the latest release and, if newer than current, downloads +
// verifies + self-replaces the running binary. Managed installs (Homebrew, npm)
// are detected first and steered to the proper command instead of replacing.
func Upgrade(ctx context.Context, current string) (Result, error) {
	latest, err := LatestVersion(ctx)
	if err != nil {
		return Result{}, err
	}
	if !isNewer(current, latest) {
		return Result{Outcome: OutcomeUpToDate, From: current, To: latest}, nil
	}

	// Resolve the real binary path (follow symlinks) and check for a managed
	// install before touching anything.
	exe, err := os.Executable()
	if err != nil {
		return Result{}, fmt.Errorf("locate running binary: %w", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	if kind, advice := detectManaged(exe, dirWritable(filepath.Dir(exe))); kind != managedNone {
		return Result{Outcome: OutcomeManaged, From: current, To: latest, Advice: advice}, nil
	}

	asset, err := AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	if err := downloadVerifyReplace(ctx, latest, asset, exe); err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeUpgraded, From: current, To: latest}, nil
}

// downloadVerifyReplace downloads the asset archive + checksums.txt for the
// given tag, verifies the sha256, extracts the framehood binary, and atomically
// replaces the running binary at exePath.
func downloadVerifyReplace(ctx context.Context, tag, asset, exePath string) error {
	base := "https://github.com/" + Repo + "/releases/download/" + tag
	archive, err := downloadToTemp(ctx, base+"/"+asset, asset)
	if err != nil {
		return err
	}
	defer os.Remove(archive)

	checksums, err := downloadBody(ctx, base+"/checksums.txt")
	if err != nil {
		return err
	}
	expected, err := parseChecksum(string(checksums), asset)
	if err != nil {
		return err
	}
	got, err := sha256File(archive)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", asset, expected, got)
	}

	binData, err := extractBinary(archive, binaryName(runtime.GOOS))
	if err != nil {
		return err
	}
	return replaceExecutable(exePath, binData)
}

// downloadBody GETs url (following redirects, which http.Client does by
// default) and returns the body, capped at maxDownloadBytes.
func downloadBody(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDownloadBytes {
		return nil, fmt.Errorf("download exceeds %d bytes", maxDownloadBytes)
	}
	return b, nil
}

// downloadToTemp streams url into a temp file (named after asset) and returns
// its path. The caller removes it.
func downloadToTemp(ctx context.Context, url, asset string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d for %s", resp.StatusCode, url)
	}
	f, err := os.CreateTemp("", "framehood-update-*-"+asset)
	if err != nil {
		return "", err
	}
	name := f.Name()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if n > maxDownloadBytes {
		f.Close()
		os.Remove(name)
		return "", fmt.Errorf("download exceeds %d bytes", maxDownloadBytes)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// sha256File returns the lowercase hex sha256 of the file at path, streaming so
// the whole archive never sits in memory.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxDownloadBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary pulls the named binary out of a .tar.gz or .zip archive and
// returns its bytes. The archive type is inferred from the path extension.
func extractBinary(archivePath, binName string) ([]byte, error) {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, binName)
	}
	return extractFromTarGz(archivePath, binName)
}

func extractFromTarGz(archivePath, binName string) ([]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == binName && hdr.Typeflag == tar.TypeReg {
			return readCapped(tr, binName)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

func extractFromZip(archivePath, binName string) ([]byte, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) == binName {
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return readCapped(rc, binName)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

// readCapped reads up to maxDownloadBytes and fails if the entry is larger, so a
// decompression bomb in the archive can't yield a truncated binary we'd install.
func readCapped(r io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxDownloadBytes {
		return nil, fmt.Errorf("extracted %s exceeds %d bytes", name, maxDownloadBytes)
	}
	return data, nil
}

// replaceExecutable atomically replaces the binary at exePath with newBin. It
// writes a temp file in the SAME directory (so the rename is atomic on the same
// filesystem), chmods 0755, then renames over the target. On Windows the
// running .exe can't be overwritten, so the current binary is moved aside to
// <exe>.old first.
func replaceExecutable(exePath string, newBin []byte) error {
	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".framehood-new-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	if runtime.GOOS == "windows" {
		// Can't overwrite a running .exe: move it aside, then move the new one in.
		old := exePath + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exePath, old); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("move current binary aside: %w", err)
		}
		if err := os.Rename(tmpName, exePath); err != nil {
			// Best-effort rollback.
			_ = os.Rename(old, exePath)
			os.Remove(tmpName)
			return fmt.Errorf("install new binary: %w", err)
		}
		return nil
	}

	if err := os.Rename(tmpName, exePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

// dirWritable reports whether dir is writable by creating and removing a temp
// file in it. A failure (permission, read-only fs) means "not writable".
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".framehood-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
