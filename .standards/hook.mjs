import fs from "node:fs";
import path from "node:path";
import { execFileSync, spawnSync } from "node:child_process";
const gitDir = execFileSync("git", ["rev-parse", "--absolute-git-dir"], {
  encoding: "utf8",
}).trim();
const stateFile = path.join(gitDir, "repository-standard-hook.json");
if (fs.existsSync(stateFile)) {
  const state = JSON.parse(fs.readFileSync(stateFile, "utf8"));
  if (state.original) {
    const previous = spawnSync("sh", [state.original], { stdio: "inherit" });
    if (previous.error || previous.status !== 0) process.exit(previous.status || 1);
  }
}
const checker = spawnSync(
  process.execPath,
  [path.join(import.meta.dirname, "check.mjs"), "staged"],
  { stdio: "inherit" },
);
process.exit(checker.error || checker.status !== 0 ? checker.status || 1 : 0);
