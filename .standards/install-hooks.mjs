import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
const git = (...args) => execFileSync("git", args, { encoding: "utf8" }).trim();
const root = git("rev-parse", "--show-toplevel");
process.chdir(root);
let hooksPath;
try {
  hooksPath = git("config", "--local", "--get", "core.hooksPath");
} catch {
  hooksPath = "";
}
if (hooksPath === ".standards/hooks") {
  console.log("Standard hook already installed");
  process.exit(0);
}
const worktrees = git("worktree", "list", "--porcelain")
  .split("\n")
  .filter((x) => x.startsWith("worktree "));
if (worktrees.length > 1)
  throw new Error(
    "Shared worktrees: add node .standards/check.mjs staged to the existing hook manually; automatic hook replacement is disabled.",
  );
const original = hooksPath
  ? path.resolve(root, hooksPath, "pre-commit")
  : path.resolve(git("rev-parse", "--git-path", "hooks"), "pre-commit");
const state = path.join(git("rev-parse", "--absolute-git-dir"), "repository-standard-hook.json");
if (fs.existsSync(state)) throw new Error("Existing hook state requires review before reinstall");
fs.writeFileSync(
  state,
  JSON.stringify({ hooksPath, original: fs.existsSync(original) ? original : null }, null, 2) +
    "\n",
  { flag: "wx" },
);
git("config", "--local", "core.hooksPath", ".standards/hooks");
console.log("Installed standard hook; existing hook will run first and is preserved.");
