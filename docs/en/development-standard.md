# Development and acceptance

This project uses standard 0.4.0 with profiles: infra, service. Read the [complete rules](../../.standards/rules.en.md). The toolkit is an adoption mechanism, not proof that all legacy code, translations or runtime scenarios already comply.

The control repository is private. Only the explicit allowlist in tools/export.mjs is distributed. Inventory, customer materials, local paths and operational notes never enter public repositories. The copied toolkit works in fork PRs without credentials for the private source.

## Scope and first rollout

Create a branch from the verified current default-branch commit. Read existing AGENTS.md instructions, hooks and CI first. Add the standalone .standards directory, .repo-policy.json, an isolated workflow, bilingual development-standard.md and relevant README updates. Preserve native formatting/build/release checks. Map README language paths instead of renaming existing files blindly. Set adoption to adopting until all applicable evidence is verified.

The first rollout enforces changed-file formatting, introduced-commit secret scanning, required language files and strict documentation changes. Full-history scanning, presentation quality, translated legacy documentation, all-file formatting and product-specific acceptance are recorded as separate evidence; do not claim they were verified by the generic job.

## Local setup

Run these commands from the repository root with Git, Node.js 22 or later, npm, tar and a POSIX shell available:

```sh
npm ci --prefix .standards --ignore-scripts --no-audit --no-fund
node .standards/install-gitleaks.mjs
node .standards/install-hooks.mjs
node .standards/check.mjs structure
```

The hook checks the Git index and does not rewrite or restage files. A previous hook runs first and remains intact. An existing hooksPath is recorded locally. Multiple worktrees require manual integration into their existing hook to avoid changing shared settings unexpectedly. For a pre-commit-managed repository, add a local hook that runs node .standards/check.mjs staged with pass_filenames: false; do not install two competing hook managers.

Restore the original hooksPath from the local repository-standard-hook.json if undoing installation. If it was empty, unset the local core.hooksPath. Keep the original hook file. Removing a rollout requires a reviewed PR; repository history and existing product checks stay intact.

## CI and merge protection

The repository-standard job checks a full PR diff against its merge base, including merge-queue events. Both README files and Markdown under both configured documentation roots need meaningful changes. Whitespace, comments and deletion do not count. A reviewer still verifies semantic relevance and translation quality. Pre-commit reports pending documentation but cannot prove a future merge.

Make repository-standard a required check only after the workflow exists on the default branch and its real run has passed. Preserve all existing required checks. Verify the actual hosting rule and bypass permissions; record plan limitations rather than claiming enforcement from a YAML file. Owner merge policy remains in force.

## Baseline, forks and updates

Full-history Gitleaks is a separate initial-adoption and private-to-public gate. Run it in an authorized isolated checkout and redact reports. Revoke any exposed credential before removing history. Exact mirrors and archives are governed in the private inventory without injecting files or reactivating them. Modified forks retain upstream conventions and authorship.

Update by generating a new versioned kit and a reviewed PR. Verify every lock hash, inspect policy and workflow changes, run negative tests, and keep product evidence distinct from tool adoption. Generated standards come from the bilingual catalog; edit the catalog instead of one generated language.

## Acceptance record

Record commit/digest, environment, command, date and result for CI, clean bootstrap, release/install/upgrade, runtime, full history, full formatting, presentation and relevant profiles. PASS needs evidence; NOT_APPLICABLE needs a reason. NOT_VERIFIED stays visible and cannot become a compliance claim.
