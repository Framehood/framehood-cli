#!/usr/bin/env node
// Postinstall: download the prebuilt `framehood` binary for this platform from
// the matching GitHub Release and unpack it into ./bin. run.js then execs it.
const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const crypto = require("crypto");
const { execFileSync } = require("child_process");

const { version } = require("./package.json");

if (process.env.FRAMEHOOD_SKIP_DOWNLOAD) {
  console.log("framehood: FRAMEHOOD_SKIP_DOWNLOAD set — skipping binary download.");
  process.exit(0);
}

const osMap = { darwin: "darwin", linux: "linux", win32: "windows" };
const archMap = { arm64: "arm64", x64: "amd64" };

const goos = osMap[process.platform];
const goarch = archMap[process.arch];

if (!goos || !goarch) {
  console.error(`framehood: unsupported platform ${process.platform}/${process.arch}.`);
  console.error("Install from https://github.com/Framehood/framehood-cli/releases instead.");
  process.exit(1);
}
if (goos === "windows" && goarch === "arm64") {
  console.error("framehood: windows/arm64 is not built. Use go install or build from source.");
  process.exit(1);
}

const ext = goos === "windows" ? "zip" : "tar.gz";
const asset = `framehood_${goos}_${goarch}.${ext}`;
const url = `https://github.com/Framehood/framehood-cli/releases/download/v${version}/${asset}`;

const binDir = path.join(__dirname, "bin");
fs.mkdirSync(binDir, { recursive: true });
const archivePath = path.join(os.tmpdir(), asset);

function download(u, dest, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 10) return reject(new Error("too many redirects"));
    https
      .get(u, { headers: { "User-Agent": "framehood-npm" } }, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          return resolve(download(res.headers.location, dest, redirects + 1));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`download failed: HTTP ${res.statusCode} for ${u}`));
        }
        const file = fs.createWriteStream(dest);
        res.pipe(file);
        file.on("finish", () => file.close(() => resolve()));
        file.on("error", reject);
      })
      .on("error", reject);
  });
}

(async () => {
  try {
    console.log(`framehood: downloading ${asset} …`);
    await download(url, archivePath);

    // Verify the download against the release's checksums.txt before we unpack
    // and run it — a tampered/MITM'd binary fails the sha256 match.
    const checksumUrl = `https://github.com/Framehood/framehood-cli/releases/download/v${version}/checksums.txt`;
    const checksumPath = path.join(os.tmpdir(), `framehood_checksums_${version}.txt`);
    await download(checksumUrl, checksumPath);
    const line = fs.readFileSync(checksumPath, "utf8").split("\n").find((l) => l.trim().endsWith(asset));
    const expected = line ? line.trim().split(/\s+/)[0].toLowerCase() : null;
    fs.unlinkSync(checksumPath);
    if (!expected) throw new Error(`no checksum for ${asset} in checksums.txt`);
    const actual = crypto.createHash("sha256").update(fs.readFileSync(archivePath)).digest("hex");
    if (actual !== expected) throw new Error(`checksum mismatch for ${asset} (expected ${expected}, got ${actual})`);
    console.log("framehood: checksum verified ✓");

    // Both tar.gz and zip are handled by the bsdtar shipped on macOS, Linux,
    // and Windows 10+.
    execFileSync("tar", ["-xf", archivePath, "-C", binDir], { stdio: "inherit" });
    const binName = goos === "windows" ? "framehood.exe" : "framehood";
    const binPath = path.join(binDir, binName);
    if (!fs.existsSync(binPath)) throw new Error(`binary ${binName} not found after extract`);
    if (goos !== "windows") fs.chmodSync(binPath, 0o755);
    fs.unlinkSync(archivePath);
    console.log("framehood: installed ✓");
  } catch (err) {
    console.error(`framehood: install failed — ${err.message}`);
    console.error("Download manually from https://github.com/Framehood/framehood-cli/releases/latest");
    process.exit(1);
  }
})();
