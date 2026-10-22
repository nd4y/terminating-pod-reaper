import { createHash } from "node:crypto";

export const VERSION = "0.4.0";
export const PROFILES = [
  "web",
  "mobile",
  "cli",
  "library",
  "service",
  "network",
  "integration",
  "infra",
  "monitoring",
  "data",
  "ml",
  "firmware",
  "docs",
];
export const KIT_FILES = [
  "package.json",
  "package-lock.json",
  ".gitignore",
  ".gitattributes",
  "gitleaks.toml",
  "format.json",
  "policy.mjs",
  "check.mjs",
  "install-gitleaks.mjs",
  "install-hooks.mjs",
  "hook.mjs",
  "hooks/pre-commit",
  "rules.en.md",
  "rules.ru.md",
  "LICENSE",
];

export function safePath(value) {
  if (
    typeof value !== "string" ||
    !value ||
    value.startsWith("/") ||
    value.includes("\\") ||
    value.includes(":") ||
    value.split("/").some((p) => p === ".." || p === "." || p === "" || p === ".git") ||
    /[\0\r\n]/.test(value)
  ) {
    throw new Error("Unsafe repository-relative path");
  }
  return value;
}

export function validatePolicy(p) {
  const errors = [];
  if (!p || typeof p !== "object" || Array.isArray(p)) return ["Policy must be an object"];
  if (p.schemaVersion !== 1) errors.push("schemaVersion must be 1");
  if (p.standardVersion !== VERSION) errors.push(`standardVersion must be ${VERSION}`);
  if (!["product", "fork", "template", "docs"].includes(p.kind))
    errors.push("Unsupported repository kind");
  if (!["experiment", "active", "maintenance"].includes(p.lifecycle))
    errors.push("Invalid lifecycle");
  if (!["adopting", "verified"].includes(p.adoption)) errors.push("Invalid adoption state");
  if (
    !Array.isArray(p.profiles) ||
    !p.profiles.length ||
    p.profiles.some((x) => !PROFILES.includes(x))
  )
    errors.push("Invalid profiles");
  if (p.docs?.mode !== "strict") errors.push("This release requires strict documentation policy");
  if (
    !Array.isArray(p.docs?.readmes) ||
    p.docs.readmes.length !== 2 ||
    new Set(p.docs.readmes).size !== 2
  )
    errors.push("Two distinct README paths are required");
  if (
    !Array.isArray(p.docs?.roots) ||
    p.docs.roots.length !== 2 ||
    new Set(p.docs.roots).size !== 2
  )
    errors.push("Two distinct documentation roots are required");
  else if (
    p.docs.roots.some((a, i) => p.docs.roots.some((b, j) => i !== j && b.startsWith(`${a}/`)))
  )
    errors.push("Documentation language roots must not overlap");
  for (const path of [
    ...(Array.isArray(p.docs?.readmes) ? p.docs.readmes : []),
    ...(Array.isArray(p.docs?.roots) ? p.docs.roots : []),
  ]) {
    try {
      safePath(path);
    } catch {
      errors.push("Invalid documentation path");
    }
  }
  if (!p.evidence || typeof p.evidence !== "object" || Array.isArray(p.evidence))
    errors.push("Evidence map is required");
  else
    for (const [name, value] of Object.entries(p.evidence)) {
      if (!["PASS", "FAIL", "NOT_VERIFIED", "NOT_APPLICABLE"].includes(value?.status))
        errors.push(`Invalid evidence status: ${name}`);
      if (
        value?.status !== "NOT_VERIFIED" &&
        (typeof value?.reference !== "string" || !value.reference.trim())
      )
        errors.push(`Evidence or reason is required: ${name}`);
    }
  for (const name of ["ci", "bootstrap", "release", "runtime"])
    if (!p.evidence?.[name]) errors.push(`Missing evidence: ${name}`);
  if (
    p.adoption === "verified" &&
    Object.values(p.evidence || {}).some(
      (x) => !x || x.status === "NOT_VERIFIED" || x.status === "FAIL",
    )
  )
    errors.push("Unverified evidence cannot be called verified");
  return errors;
}

export function meaningfulChange(before, after) {
  if (after == null || after.trim().length < 40) return false;
  const normalize = (s) => (s || "").replace(/<!--[^]*?-->/g, "").replace(/\s+/g, "");
  return normalize(before) !== normalize(after);
}

export function checkDocs(policy, changes, readBefore, readAfter) {
  const errors = [];
  for (const path of policy.docs.readmes) {
    const content = readAfter(path);
    if (content == null || content.trim().length < 40)
      errors.push(`Missing or empty README: ${path}`);
    if (changes.length && (!changes.includes(path) || !meaningfulChange(readBefore(path), content)))
      errors.push(`Meaningful README update required: ${path}`);
  }
  if (changes.length)
    for (const root of policy.docs.roots) {
      if (
        !changes.some(
          (path) =>
            path.startsWith(`${root}/`) &&
            /\.md$/i.test(path) &&
            meaningfulChange(readBefore(path), readAfter(path)),
        )
      )
        errors.push(`Meaningful documentation update required under ${root}/`);
    }
  return errors;
}

export function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

export function verifyLock(lock, readFile) {
  const errors = [];
  if (
    lock?.version !== VERSION ||
    !lock.files ||
    typeof lock.files !== "object" ||
    Array.isArray(lock.files)
  )
    return ["Invalid standard lock"];
  if (!/^[a-f0-9]{64}$/.test(lock.bundleSha256 || "")) errors.push("Invalid bundle checksum");
  const required = KIT_FILES.map((name) => `.standards/${name}`);
  if (
    Object.keys(lock.files).length !== required.length ||
    required.some((name) => !Object.hasOwn(lock.files, name))
  )
    errors.push("Incomplete or unexpected bundle manifest");
  for (const [path, expected] of Object.entries(lock.files)) {
    try {
      safePath(path);
      if (!/^[a-f0-9]{64}$/.test(expected) || sha256(readFile(path)) !== expected)
        errors.push(`Standard bundle changed: ${path}`);
    } catch {
      errors.push(`Standard bundle file missing or unsafe: ${path}`);
    }
  }
  const manifest = Object.entries(lock.files)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([p, h]) => `${h}  ${p}\n`)
    .join("");
  if (sha256(manifest) !== lock.bundleSha256) errors.push("Bundle manifest checksum mismatch");
  return errors;
}
