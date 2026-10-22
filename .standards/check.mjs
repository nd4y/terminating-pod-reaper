import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync, spawnSync } from "node:child_process";
import { checkDocs, safePath, validatePolicy, verifyLock } from "./policy.mjs";

const kit = path.dirname(fileURLToPath(import.meta.url));
function git(args, options = {}) {
  return execFileSync("git", args, {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    stdio: ["ignore", "pipe", "pipe"],
    ...options,
  });
}
const root = git(["rev-parse", "--show-toplevel"]).trim();
process.chdir(root);
const args = process.argv.slice(2);
const mode = args[0];
if (!["staged", "ci", "structure"].includes(mode))
  throw new Error("Usage: check.mjs staged | ci <base-sha> <head-sha> | structure");
const commit = (value) => {
  if (!/^[a-f0-9]{40}$/.test(value || "")) throw new Error("A full commit SHA is required");
  return git(["rev-parse", "--verify", `${value}^{commit}`]).trim();
};
let before, after, changes;
if (mode === "ci") {
  const base = commit(args[1]);
  after = commit(args[2]);
  before = git(["merge-base", base, after]).trim();
  changes = git(["diff", "--name-only", "--no-renames", "-z", before, after, "--"])
    .split("\0")
    .filter(Boolean);
} else if (mode === "staged") {
  try {
    before = git(["rev-parse", "--verify", "HEAD"]).trim();
  } catch {
    before = null;
  }
  after = ":";
  changes = git(["diff", "--cached", "--name-only", "--no-renames", "-z", "--"])
    .split("\0")
    .filter(Boolean);
} else {
  before = null;
  after = null;
  changes = [];
}
function readAt(ref, name) {
  safePath(name);
  if (ref === null) return null;
  const object = ref === ":" ? `:${name}` : `${ref}:${name}`;
  try {
    return git(["show", object]);
  } catch (e) {
    // A missing path is expected for additions/deletions. Other Git failures remain errors.
    const message = String(e.stderr || "");
    if (
      /does not exist|exists on disk, but not in|not in the index|not a valid object name|path .* not in/i.test(
        message,
      )
    )
      return null;
    throw e;
  }
}
const content = (name) =>
  mode === "structure"
    ? fs.readFileSync(path.join(root, safePath(name)), "utf8")
    : readAt(after, name);
const policy = JSON.parse(content(".repo-policy.json") || "null");
const failures = validatePolicy(policy);
if (failures.length) throw new Error(failures.join("\n"));
for (const directory of policy.docs.roots) {
  const page = `${directory}/development-standard.md`;
  if (!content(page)?.trim()) failures.push(`Missing adoption documentation: ${page}`);
}
const lock = JSON.parse(content(".standards/lock.json") || "null");
failures.push(...verifyLock(lock, (name) => content(name)));
if (mode !== "staged")
  failures.push(...checkDocs(policy, changes, (name) => readAt(before, name), content));
else {
  // Commit-time structure is authoritative; the final accumulated PR diff is enforced in CI.
  failures.push(...checkDocs(policy, [], () => null, content));
  const pending = checkDocs(policy, changes, (name) => readAt(before, name), content);
  if (pending.length)
    console.log("DOCS: final PR must update both README files and both documentation languages.");
}
if (failures.length) throw new Error(failures.join("\n"));
if (mode === "structure") {
  console.log("PASS: policy, bundle and README structure");
  process.exit(0);
}

const prettier = await import("prettier");
const formatOptions = JSON.parse(fs.readFileSync(path.join(kit, "format.json"), "utf8"));
for (const name of changes) {
  safePath(name);
  if (
    name.startsWith(".standards/") ||
    /(^|\/)(package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$/.test(name)
  )
    continue;
  const data = content(name);
  if (data == null || data.includes("\0")) continue;
  const info = await prettier.getFileInfo(name, { resolveConfig: false });
  if (!info.inferredParser) continue;
  if (!(await prettier.check(data, { ...formatOptions, filepath: name })))
    failures.push(`Prettier: ${name}`);
}
if (failures.length) throw new Error(failures.join("\n"));

const binary =
  process.env.GITLEAKS_BIN ||
  path.join(kit, "bin", process.platform === "win32" ? "gitleaks.exe" : "gitleaks");
const common = ["--redact", "--no-banner", "--config", path.join(kit, "gitleaks.toml")];
function scan(scanArgs) {
  const result = spawnSync(binary, scanArgs, { cwd: root, stdio: "inherit", timeout: 300000 });
  if (result.error || result.status !== 0)
    throw new Error("Gitleaks failed or is unavailable; commit/CI blocked.");
}
if (mode === "ci") scan(["git", root, `--log-opts=${before}..${after}`, ...common]);
else {
  const temp = fs.mkdtempSync(path.join(os.tmpdir(), "repository-standard-"));
  try {
    for (const name of changes) {
      const text = content(name);
      if (text == null) continue;
      const data = git(["show", `:${name}`], { encoding: null });
      const destination = path.join(temp, safePath(name));
      fs.mkdirSync(path.dirname(destination), { recursive: true });
      fs.writeFileSync(destination, data);
    }
    scan(["dir", temp, ...common]);
  } finally {
    const resolved = path.resolve(temp);
    if (
      path.dirname(resolved) !== path.resolve(os.tmpdir()) ||
      !path.basename(resolved).startsWith("repository-standard-")
    )
      throw new Error("Unsafe temporary cleanup target");
    fs.rmSync(resolved, { recursive: true, force: true });
  }
}
console.log(`PASS: ${mode} policy, documentation, changed-file Prettier and Gitleaks`);
