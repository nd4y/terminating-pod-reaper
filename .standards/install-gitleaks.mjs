import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import { sha256 } from "./policy.mjs";

const assets = {
  "linux-x64": [
    "gitleaks_8.30.1_linux_x64.tar.gz",
    "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb",
  ],
  "linux-arm64": [
    "gitleaks_8.30.1_linux_arm64.tar.gz",
    "e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080",
  ],
  "win32-x64": [
    "gitleaks_8.30.1_windows_x64.zip",
    "d29144deff3a68aa93ced33dddf84b7fdc26070add4aa0f4513094c8332afc4e",
  ],
  "win32-arm64": [
    "gitleaks_8.30.1_windows_arm64.zip",
    "b95f5e4f5c425cedca7ee203d9afd29597e692c4924a12ed42f970537c72cc0f",
  ],
  "darwin-arm64": [
    "gitleaks_8.30.1_darwin_arm64.tar.gz",
    "b40ab0ae55c505963e365f271a8d3846efbc170aa17f2607f13df610a9aeb6a5",
  ],
  "darwin-x64": [
    "gitleaks_8.30.1_darwin_x64.tar.gz",
    "dfe101a4db2255fc85120ac7f3d25e4342c3c20cf749f2c20a18081af1952709",
  ],
};
const asset = assets[`${process.platform}-${process.arch}`];
if (!asset) throw new Error("Unsupported platform; install pinned Gitleaks and set GITLEAKS_BIN");
const directory = path.dirname(fileURLToPath(import.meta.url));
const bin = path.join(directory, "bin");
fs.mkdirSync(bin, { recursive: true });
const response = await fetch(
  `https://github.com/gitleaks/gitleaks/releases/download/v8.30.1/${asset[0]}`,
  { signal: AbortSignal.timeout(60000) },
);
if (!response.ok) throw new Error(`Gitleaks download failed: ${response.status}`);
const buffer = Buffer.from(await response.arrayBuffer());
if (sha256(buffer) !== asset[1]) throw new Error("Gitleaks checksum mismatch");
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "gitleaks-download-"));
try {
  const archive = path.join(temporary, asset[0]);
  fs.writeFileSync(archive, buffer);
  const executable = process.platform === "win32" ? "gitleaks.exe" : "gitleaks";
  execFileSync("tar", ["-xf", archive, "-C", bin, executable], { stdio: "inherit" });
  if (process.platform !== "win32") fs.chmodSync(path.join(bin, executable), 0o755);
  console.log(execFileSync(path.join(bin, executable), ["version"], { encoding: "utf8" }).trim());
} finally {
  const resolved = path.resolve(temporary);
  if (
    path.dirname(resolved) !== path.resolve(os.tmpdir()) ||
    !path.basename(resolved).startsWith("gitleaks-download-")
  )
    throw new Error("Unsafe temporary cleanup target");
  fs.rmSync(resolved, { recursive: true, force: true });
}
