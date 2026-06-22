package selfupdate

import (
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		current, latest string
		want            int // -1 older, 0 equal, +1 newer
	}{
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.4", -1},   // v-prefix on current
		{"1.2.3", "v1.2.4", -1},   // v-prefix on latest
		{"v1.2.3", "v1.2.3", 0},   // both prefixed, equal
		{"1.9.0", "1.10.0", -1},   // numeric, not lexicographic
		{"2.0.0", "1.9.9", 1},     // major dominates
		{"1.2", "1.2.0", 0},       // missing patch == .0
		{"1.2.3-rc1", "1.2.3", 0}, // pre-release suffix ignored → equal core
	}
	for _, c := range cases {
		if got := compareVersions(c.current, c.latest); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.current, c.latest, got, c.want)
		}
	}
}

func TestCompareVersions_DevAlwaysUpgrades(t *testing.T) {
	for _, latest := range []string{"v0.0.1", "1.0.0", "v9.9.9"} {
		if got := compareVersions("dev", latest); got != -1 {
			t.Errorf("compareVersions(dev, %q) = %d, want -1 (dev always behind)", latest, got)
		}
		if !isNewer("dev", latest) {
			t.Errorf("isNewer(dev, %q) = false, want true", latest)
		}
	}
}

func TestIsNewer(t *testing.T) {
	if !isNewer("1.0.0", "1.0.1") {
		t.Error("1.0.1 should be newer than 1.0.0")
	}
	if isNewer("1.0.1", "1.0.0") {
		t.Error("1.0.0 is not newer than 1.0.1")
	}
	if isNewer("1.0.0", "1.0.0") {
		t.Error("equal versions: not newer")
	}
}

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "framehood_darwin_arm64.tar.gz"},
		{"darwin", "amd64", "framehood_darwin_amd64.tar.gz"},
		{"linux", "amd64", "framehood_linux_amd64.tar.gz"},
		{"linux", "arm64", "framehood_linux_arm64.tar.gz"},
		{"windows", "amd64", "framehood_windows_amd64.zip"},
	}
	for _, c := range cases {
		got, err := AssetName(c.goos, c.goarch)
		if err != nil {
			t.Errorf("AssetName(%s,%s): %v", c.goos, c.goarch, err)
			continue
		}
		if got != c.want {
			t.Errorf("AssetName(%s,%s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestAssetName_Unsupported(t *testing.T) {
	if _, err := AssetName("plan9", "amd64"); err == nil {
		t.Error("expected error for unsupported OS")
	}
	if _, err := AssetName("linux", "riscv64"); err == nil {
		t.Error("expected error for unsupported arch")
	}
	if _, err := AssetName("windows", "arm64"); err == nil {
		t.Error("windows/arm64 is not built and should error")
	}
}

func TestBinaryName(t *testing.T) {
	if got := binaryName("windows"); got != "framehood.exe" {
		t.Errorf("windows binary = %q, want framehood.exe", got)
	}
	if got := binaryName("linux"); got != "framehood" {
		t.Errorf("unix binary = %q, want framehood", got)
	}
}

func TestParseChecksum(t *testing.T) {
	// goreleaser checksums.txt: "<sha256>  <filename>"
	sum := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	body := other + "  framehood_linux_amd64.tar.gz\n" +
		sum + "  framehood_darwin_arm64.tar.gz\n" +
		strings.Repeat("c", 64) + "  framehood_windows_amd64.zip\n"

	got, err := parseChecksum(body, "framehood_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("parseChecksum: %v", err)
	}
	if got != sum {
		t.Errorf("checksum = %q, want %q", got, sum)
	}
}

func TestParseChecksum_MissingAndMalformed(t *testing.T) {
	// Asset not present.
	if _, err := parseChecksum("deadbeef  other.tar.gz\n", "framehood_darwin_arm64.tar.gz"); err == nil {
		t.Error("missing asset should error")
	}
	// Malformed (too short) checksum.
	if _, err := parseChecksum("abc  framehood_darwin_arm64.tar.gz\n", "framehood_darwin_arm64.tar.gz"); err == nil {
		t.Error("short checksum should error")
	}
	// Non-hex checksum of correct length.
	bad := strings.Repeat("z", 64)
	if _, err := parseChecksum(bad+"  framehood_darwin_arm64.tar.gz\n", "framehood_darwin_arm64.tar.gz"); err == nil {
		t.Error("non-hex checksum should error")
	}
	// A line that merely contains the asset name as a substring must NOT match
	// (exact basename only).
	good := strings.Repeat("a", 64)
	body := good + "  framehood_darwin_arm64.tar.gz.sig\n"
	if _, err := parseChecksum(body, "framehood_darwin_arm64.tar.gz"); err == nil {
		t.Error("substring (the .sig line) must not satisfy an exact asset match")
	}
}

func TestDetectManaged_Brew(t *testing.T) {
	// Genuine brew installs: the bin/framehood symlink resolves (after
	// EvalSymlinks) into a Cellar dir, which is the ownership signal → owned.
	ownedPaths := []string{
		"/opt/homebrew/Cellar/framehood/1.2.3/bin/framehood",
		"/usr/local/Cellar/framehood/1.2.3/bin/framehood",
	}
	for _, p := range ownedPaths {
		kind, conf, advice := detectManaged(p, true)
		if kind != managedBrew {
			t.Errorf("detectManaged(%q) kind = %d, want managedBrew", p, kind)
		}
		if conf != confidenceOwned {
			t.Errorf("detectManaged(%q) confidence = %d, want confidenceOwned (Cellar owns it)", p, conf)
		}
		if !strings.Contains(advice, "brew upgrade framehood") {
			t.Errorf("brew advice for %q = %q, want it to mention `brew upgrade framehood`", p, advice)
		}
	}
}

func TestDetectManaged_BrewBinDirNotOwned(t *testing.T) {
	// A self-built binary merely COPIED into a Homebrew/linuxbrew bin dir, with
	// no Cellar in the resolved path: brew doesn't own it. Detection must be weak
	// so the caller falls back to self-update/advice instead of `brew upgrade`.
	weakPaths := []string{
		"/opt/homebrew/bin/framehood",
		"/home/linuxbrew/.linuxbrew/bin/framehood",
	}
	for _, p := range weakPaths {
		kind, conf, _ := detectManaged(p, true)
		if kind != managedBrew {
			t.Errorf("detectManaged(%q) kind = %d, want managedBrew", p, kind)
		}
		if conf != confidenceWeak {
			t.Errorf("detectManaged(%q) confidence = %d, want confidenceWeak (bin dir, no Cellar)", p, conf)
		}
	}
}

func TestDetectManaged_Npm(t *testing.T) {
	p := "/usr/local/lib/node_modules/framehood/bin/framehood"
	kind, conf, advice := detectManaged(p, true)
	if kind != managedNpm {
		t.Errorf("npm path kind = %d, want managedNpm", kind)
	}
	if conf != confidenceOwned {
		t.Errorf("npm confidence = %d, want confidenceOwned (node_modules owns it)", conf)
	}
	if !strings.Contains(advice, "npm i -g framehood@latest") {
		t.Errorf("npm advice = %q, want it to mention the npm command", advice)
	}
}

func TestDetectManaged_NonWritableDir(t *testing.T) {
	// A plain path, but the dir isn't writable → advise, don't self-replace.
	kind, conf, advice := detectManaged("/usr/bin/framehood", false)
	if kind != managedOther {
		t.Errorf("non-writable dir kind = %d, want managedOther", kind)
	}
	if conf != confidenceOwned {
		t.Errorf("non-writable confidence = %d, want confidenceOwned (advise, no PM auto-exec)", conf)
	}
	if !strings.Contains(advice, "releases/latest") {
		t.Errorf("non-writable advice = %q, want a releases link", advice)
	}
}

func TestDetectManaged_SelfManaged(t *testing.T) {
	// A normal, writable install dir → safe to self-replace.
	kind, conf, advice := detectManaged("/home/user/.local/bin/framehood", true)
	if kind != managedNone {
		t.Errorf("self-managed kind = %d, want managedNone", kind)
	}
	if conf != confidenceNone {
		t.Errorf("self-managed confidence = %d, want confidenceNone", conf)
	}
	if advice != "" {
		t.Errorf("self-managed advice = %q, want empty", advice)
	}
}
