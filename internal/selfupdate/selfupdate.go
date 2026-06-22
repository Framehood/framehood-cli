// Package selfupdate implements `framehood upgrade`: it resolves the latest
// GitHub release, downloads the platform archive, verifies it against the
// release's sha256 checksums.txt, and atomically self-replaces the running
// binary. Package-manager-managed installs (Homebrew, the npm wrapper) are
// detected and steered to the right command instead of being overwritten.
//
// All network access is HTTPS to github.com; no token is needed (public
// releases). Nothing is ever written outside the running binary's directory
// (plus the OS temp dir for the download).
package selfupdate

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Repo is the GitHub "owner/name" the releases are published under.
const Repo = "Framehood/framehood-cli"

// devVersion is the build-time default for an un-released (locally built)
// binary. It is always treated as "an upgrade is available".
const devVersion = "dev"

// AssetName returns the release asset filename for the given platform, matching
// the goreleaser archive name_template ("framehood_<os>_<arch>.<ext>") and the
// npm install.js convention. tar.gz on unix, zip on windows.
func AssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return "", fmt.Errorf("unsupported OS %q", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported arch %q", goarch)
	}
	if goos == "windows" && goarch == "arm64" {
		return "", fmt.Errorf("windows/arm64 is not built; install from source")
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("framehood_%s_%s.%s", goos, goarch, ext), nil
}

// binaryName is the executable name inside the archive for this platform.
func binaryName(goos string) string {
	if goos == "windows" {
		return "framehood.exe"
	}
	return "framehood"
}

// normalizeVersion strips a leading "v" and surrounding space, returning the
// bare "X.Y.Z" (or whatever followed the v).
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	return strings.TrimPrefix(v, "v")
}

// parseSemver parses "X.Y.Z" (ignoring any pre-release/build suffix) into three
// ints. Missing components default to 0; a non-numeric leading component makes
// the parse "unknown" (ok=false).
func parseSemver(v string) (nums [3]int, ok bool) {
	v = normalizeVersion(v)
	// Drop a pre-release/build suffix ("1.2.3-rc1+meta" → "1.2.3").
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nums, false
	}
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return nums, false
		}
		nums[i] = n
	}
	return nums, true
}

// compareVersions returns -1 if current < latest, 0 if equal, +1 if current >
// latest. A "dev" (or otherwise unparseable) current version is always treated
// as older than any real latest (returns -1), so `upgrade` offers the update.
func compareVersions(current, latest string) int {
	if normalizeVersion(current) == devVersion {
		return -1
	}
	cur, curOK := parseSemver(current)
	lat, latOK := parseSemver(latest)
	switch {
	case !latOK:
		// We can't understand the latest tag — treat as no upgrade rather than
		// blindly replacing.
		return 0
	case !curOK:
		// Unknown current, known latest → assume an upgrade is available.
		return -1
	}
	for i := 0; i < 3; i++ {
		if cur[i] < lat[i] {
			return -1
		}
		if cur[i] > lat[i] {
			return 1
		}
	}
	return 0
}

// isNewer reports whether latest is strictly newer than current.
func isNewer(current, latest string) bool {
	return compareVersions(current, latest) < 0
}

// parseChecksum extracts the lowercase sha256 for asset from a checksums.txt
// body. The file format is "<sha256>  <filename>" per line (goreleaser /
// sha256sum). Mirrors the npm install.js matching: find the line ending with
// the asset name, take the first whitespace-separated field.
func parseChecksum(checksums, asset string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Match the exact asset basename at the end of the line.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[len(fields)-1]
		if name == asset {
			sum := strings.ToLower(fields[0])
			if len(sum) != 64 {
				return "", fmt.Errorf("malformed checksum for %s: %q", asset, fields[0])
			}
			for _, r := range sum {
				if !isHex(r) {
					return "", fmt.Errorf("non-hex checksum for %s", asset)
				}
			}
			return sum, nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in checksums.txt", asset)
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

// managedKind classifies how the running binary was installed, to decide
// whether self-replace is appropriate.
type managedKind int

const (
	managedNone  managedKind = iota // self-managed → safe to self-replace
	managedBrew                     // Homebrew → `brew upgrade framehood`
	managedNpm                      // npm wrapper → `npm i -g framehood@latest`
	managedOther                    // some other managed/non-writable install
)

// detectManaged inspects an already-resolved (symlinks evaluated) executable
// path and reports whether the install is package-manager-managed and the
// advice to print. dirWritable reports whether the binary's directory is
// writable (callers pass the real check; tests pass a stub).
func detectManaged(resolvedExe string, dirWritable bool) (managedKind, string) {
	lower := strings.ToLower(filepath.ToSlash(resolvedExe))
	switch {
	case strings.Contains(lower, "/cellar/") || strings.Contains(lower, "/homebrew/") || strings.Contains(lower, "/.linuxbrew/"):
		return managedBrew, "this looks like a Homebrew install — run:\n  brew upgrade framehood"
	case strings.Contains(lower, "/node_modules/framehood/"):
		return managedNpm, "this looks like the npm wrapper — run:\n  npm i -g framehood@latest"
	case !dirWritable:
		return managedOther, "the binary's directory isn't writable — re-download from\n  https://github.com/" + Repo + "/releases/latest"
	}
	return managedNone, ""
}

// CurrentPlatform returns the asset name for the running platform.
func CurrentPlatform() (string, error) {
	return AssetName(runtime.GOOS, runtime.GOARCH)
}
