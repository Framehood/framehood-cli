#!/usr/bin/env node
// Thin launcher: exec the platform binary fetched by install.js, forwarding all
// arguments and stdio (so the interactive TUI works).
const path = require("path");
const fs = require("fs");
const { spawnSync } = require("child_process");

const binName = process.platform === "win32" ? "framehood.exe" : "framehood";
const binPath = path.join(__dirname, "bin", binName);

if (!fs.existsSync(binPath)) {
  console.error("framehood: binary not found — reinstall the package, or download from");
  console.error("https://github.com/Framehood/framehood-cli/releases/latest");
  process.exit(1);
}

const result = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  console.error(`framehood: ${result.error.message}`);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
