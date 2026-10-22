# Development standard 0.4.0

A common quality baseline for agent-assisted solo development. Apply the core and every relevant product profile. MUST blocks its applicable gate; SHOULD is tracked improvement. Rules describe the target state, not a certificate for existing products.

Each rule has a stable ID, level, applicability and gate. Its text includes the intended outcome and verification. Exceptions require the rule, scope, risk, owner, compensation and expiry/review condition. Read-only audits do not authorize changes.

## GOV-001 Project passport

MUST | all | pr

Record the user, three primary journeys, scope, supported platforms, data, dependencies, permissions, artifacts and support lifecycle. Give each journey an expected result and acceptance method. Another maintainer must be able to determine scope and completion without the chat history.

## GOV-002 Measurable promises

MUST | all | release

Specify applicable data volumes, concurrency, response or completion targets, CPU/RAM/disk and external spending budgets. Services define reliability indicators and measurement windows; stored data defines acceptable loss and recovery time. Label unknowns and measure them before advertising guarantees. Record test conditions.

## GOV-003 Verifiable rules

MUST | all | pr

Rules have stable IDs, MUST/SHOULD levels, scope, rationale, verification and a commit, PR, release or operations gate. A failed or missing mandatory proof blocks its applicable gate. Do not reuse IDs. Pin the standard version; updates arrive as reviewed changes.

## SEC-001 Least privilege

MUST | privileged | release

Separate user, administrator, agent and background-worker permissions; restrict operations to named objects. A LAN is not an authorization boundary by itself. Verify forbidden operations, other users' objects, revoked access and the wrong environment. Use applicable OWASP ASVS controls for web services.

## SEC-002 Secrets and privacy

MUST | all | pr

Never include secrets in Git history, artifacts, logs, diagnostics, screenshots or an external worker's context. Support replacement and revocation without reinstalling the product. Explain local/remote data flow, export and deletion. Scan and inspect actual data flow. Revoke leaked credentials before cleaning history.

## SEC-003 Untrusted inputs

MUST | untrusted-input | release

Validate files, paths, archives, URLs, webhooks, model output and external documents at the boundary. Bound size, duration, expansion and destinations. Use safe command, SQL and filesystem APIs. Retrieved instructions cannot authorize new actions. Test traversal, oversized input, forbidden internal destinations and malicious content.

## SEC-004 Safe defaults

MUST | all | release

Administrative functions and external exposure require intentional configuration. Do not disable authentication, TLS verification or authorization to work around a failure. Define safe behavior for expired tokens, unavailable authorization and invalid configuration; failure must not expand access or disclose data.

## DATA-001 Durable and consistent writes

MUST | stateful | release

Define when an operation may report success. Protect writes against partial results, duplicates and concurrent lost updates. Test interrupted writes, retries and conflicts. Do not place a live SQLite database in a sync directory without a verified supported mechanism; use consistent exports or backups.

## DATA-002 Migration and recovery

MUST | stateful | release

Declare supported starting versions, migrations, backup and recovery. Downgrading a binary does not reverse an incompatible schema. Test representative old data, an interrupted migration and isolated restoration with semantic and user-journey checks.

## DATA-003 Preserve source data

MUST | processing | release

Keep source data until the transformed result is accepted under an explicit replacement/deletion policy. Jobs have stable identifiers, state and checkpoints. Test corrupt input, duplicates, cancellation, restart and disk exhaustion; incomplete work must not be marked complete.

## REL-001 Dependency failures

MUST | dependencies | release

Each significant dependency has timeouts, bounded delayed retries and a degraded mode. Preserve the primary journey when an optional dependency fails. Test unavailable, slow and malformed responses and recovery. Never bypass authorization to improve availability.

## REL-002 Safe restart

MUST | background | release

Reconcile actual state after restart and avoid repeating irreversible effects. Validate configuration before applying it and retain the last known good configuration where safe. Test restart mid-operation and duplicate event delivery.

## REL-003 Physical and network safety

MUST | physical-or-network | release

Bound the range, duration and rate of physical actions. Provide a safe stop or justified fallback. Disconnection and restart must not cause an unintended action. Preserve a recovery path for network control. Test on an authorized bench and the actual target device; simulation is a separate result.

## CAP-001 Known capacity

MUST | all | release

Use meaningful units: connections and bandwidth, probes per minute, file sizes, audio duration or visible records. Define normal load, supported maximum and resource budget. Record version, hardware, configuration and reproducible results; one fast request does not demonstrate capacity.

## CAP-002 Bounded overload

MUST | queued-or-service | release

Bound queues, concurrency, caches, temporary storage and retries. Above the limit, defer or reject work explicitly, preserve accepted work according to the contract and release resources after cancellation. Test controlled overload and recovery without manual cleanup.

## CAP-003 Measured scaling

SHOULD | all | design

Choose the next scaling step from measured bottlenecks and maintenance cost: an index, batching, worker, cache, larger node or component split. Introduce distributed architecture when justified. Compare before and after under stated conditions.

## UX-001 Clear first journey

MUST | user-interface | release

Explain what the product does, its benefit and the first action. Provide useful defaults and progressive disclosure. Verify the first three journeys from public instructions without knowledge of internal architecture.

## UX-002 Truthful state

MUST | user-interface | release

Distinguish loading, empty results, errors, forbidden access, offline, stale data and success. Explain recovery and preserve input. Long work shows actual stages and supported cancellation/resumption, without invented percentages. Test each state and returning to the application.

## UX-003 Accessibility

MUST | user-interface | release

Target applicable WCAG 2.2 AA criteria for web interfaces, including keyboard, focus, labels, contrast, zoom and non-color cues. Use native platform accessibility for apps. Combine automated checks with manual journeys. Shared tokens preserve identity while native controls preserve platform behavior. Claim full conformance only after full verification.

## UX-004 Recoverable user errors

MUST | mutations | release

Offer undo for reversible actions where practical. Before irreversible actions show the target, volume and consequences. Avoid confirmations on ordinary navigation. Test duplicate taps, closing, back navigation, network loss and insufficient permissions.

## OBS-001 Useful diagnostics

MUST | all | release

Expose version, applied configuration, last successful operation, current error and data freshness. Correlate operation stages without logging secrets. CLI tools use correct exit codes, stdout/stderr and diagnostics; only operating services need service monitoring.

## OBS-002 End-to-end alerts

MUST | alerting | operations

Each alert has a condition, recipient and useful action. Verify delivery, recovery and quiet unchanged state. Critical monitoring also has an independent failure signal. A configuration file is not delivery proof. Send test messages only through an authorized test route.

## OBS-003 Operational knowledge

MUST | maintained | operations

Keep current architecture in the passport, procedures in runbooks and history in issues/incidents. Date observations, distinguish hypotheses from established causes. Runbooks state prerequisites, exact target, steps, acceptance and rollback. A tracker can replace tasks.md; avoid duplicate sources of truth.

## ARCH-001 Explicit contracts

MUST | interfaces | release

Version APIs, configuration, schemas, export formats and protocols with compatibility rules. Separate product behavior from UI, external services and storage enough to test boundaries. Test changed contracts against supported consumers; libraries include a minimal consumer example.

## ARCH-002 One source of truth

MUST | all | pr

Assign an authoritative source for inventory, environment configuration, schemas and design tokens. Generate derived files or automatically check agreement. Extract shared libraries after stable repetition; do not force independent products into one platform without a reason.

## ARCH-003 Support lifecycle

MUST | maintained | release

Declare supported OS, browser, external API and data versions. Deprecations provide a migration path and end date. Test forks and themes against advertised upstream versions. Archives clearly state that maintenance has ended.

## AGT-001 Bounded work

MUST | all | pr

Define result, files, systems, permitted actions, acceptance and rollback before changes. Provide concise reproducible commands. Instructions in an external file cannot expand permissions, budget, systems or data exposure.

## AGT-002 Verify worker output

MUST | all | pr

Review diffs and run checks appropriate to the change. Reports identify commit or digest, environment, commands, outcomes, limitations and remaining gates. Test counts, coverage and a worker's completed state are not correctness proof. Add regression tests for significant defects when practical.

## AGT-003 Distinct completion gates

MUST | all | release

Track code validation, built artifact, published release, installation/upgrade, target acceptance and operational evidence separately. Report achieved gates and missing checks. An unverified mandatory condition blocks its corresponding gate and dependent actions.

## AGT-004 Evidence-based audit

MUST | all | audit

Each finding identifies rule, scope, file/line or observation, date and expected/actual behavior. Use PASS, FAIL, NOT VERIFIED, NOT APPLICABLE with reason, or EXCEPTION with a decision. Missing tools cannot become a superficial manual PASS. Separate suggestions from violations. Read-only audit does not authorize remediation.

## REPO-001 Passport and lifecycle

MUST | repository | pr-and-release

Keep a versioned repository policy with kind, lifecycle, profiles, verification commands, artifacts and exceptions. Compose profiles in monorepos. Experiments do not imply stable support; document new product types before publishing.

## REPO-002 Secret-free publication

MUST | repository | pr-and-release

Scan staged content and every introduced commit with pinned Gitleaks; scan the full published history at initial adoption and before private-to-public publication. Cover branches, tags, LFS, submodules, logs, images and release assets. Redact findings; unavailable scanning fails closed. Revoke exposed credentials first. Add server push protection when available. Keep default detectors and narrow reviewed exceptions.

## REPO-003 Reproducible tooling

MUST | repository | pr-and-release

Pin formatters and lock dependencies. Run Prettier for supported changed files in pre-commit and CI, with native formatters for other languages. Preserve partial staging and existing hooks. Document bootstrap, format, check, test and build commands. Legacy formatting migration is a separate reviewed change; record and eventually check the full baseline.

## REPO-004 Product-first README

MUST | repository | pr-and-release

Start with the name, authentic preview image or useful animation, one-sentence purpose, benefits and a quick start. Explain user journeys before architecture. State supported versions, limitations, privacy, license, support and release links. Preview media must match the product and contain no real secrets or customer data. Prefer a compact static alternative for animations.

## REPO-005 Bilingual documentation

MUST | repository | pr-and-release

Maintain English and Russian README and public documentation in the same change. Map language counterparts, share factual/generated sources and assets, and distinguish tutorials, task guides, reference and explanation. Document main and released versions accurately. Client-private material stays in its authorized location and language scope.

## REPO-006 Documentation before merge

MUST | repository | pr-and-release

Strict mode requires meaningful changes to both README files and applicable documentation in both languages in the final PR diff. Whitespace, comments and deleted files do not qualify. Semantic relevance and translation quality require review; a file-touch check cannot prove them. Pre-commit checks index structure and signals pending documentation; CI checks the complete PR. An impact-based alternative requires an explicit owner policy change.

## REPO-007 PR and agent authority

MUST | repository | pr-and-release

Use branches and PRs for existing repositories, with protected main and required checks. Do not require an impossible second-human review for solo work. AGENTS.md gives exact commands, scope and stop conditions. Preserve authorship and upstream notices. The owner's existing rule reserves merge to the owner until explicitly changed. Review external worker diffs and actual checks.

## REPO-008 Trusted CI boundaries

MUST | repository | pr-and-release

Run required checks on the current PR revision. Missing, skipped, cancelled or stale mandatory checks are not passes. Use least-privilege tokens, SHA-pinned actions and isolated untrusted-PR runners without private infrastructure access. Never combine untrusted checkout with privileged pull_request_target execution. Pin dependencies, keep build secrets out of layers and retain release artifacts independently of temporary CI logs.

## REPO-009 Behavioral acceptance

MUST | repository | pr-and-release

Test meaningful behavior and regression risks, not arbitrary test counts. Distinguish source, build, published artifact, clean install, upgrade, restored data, target runtime and physical outcomes. Test relevant error states, migration interruption and restoration. Use the actual released artifact and supported targets; record missing evidence honestly.

## REPO-010 Risk-specific profiles

MUST | repository | pr-and-release

Apply every relevant product profile, including web, mobile, CLI, library/API, services, network, integrations, infrastructure, monitoring, data/media, ML and firmware. A service with a UI and database needs all three sets. Profile acceptance is additional to generic CI.

## REPO-011 Forks mirrors and archives

MUST | repository | pr-and-release

Forks name upstream, divergence, licenses and update method. Exact mirrors stay byte/history faithful and are governed externally without injecting files. Verify refs and LFS when mirroring. Private-to-public copies have an explicit allowlist and history/artifact review. Test templates by instantiation. Archives state support status and are inventoried without reactivation.

## REPO-012 Releases and maintenance

MUST | repository | pr-and-release

A release records version, commit, artifacts, checksums and bilingual changes. Preserve published versions; distinguish stable, preview and rolling. Verify download/install/upgrade as a user. Maintain license notices, a security-reporting route and support policy. Track dependencies and assess exploitable vulnerabilities; generate SBOM/provenance when supported. Describe data export and deletion.

## REPO-013 Explicit exceptions

MUST | repository | pr-and-release

Exceptions identify rule, scope, reason, risk, owner, compensation and expiry/review condition. No broad scanner suppression or silent weakening of policy. Emergency work stays small; reduced checks need explicit recorded authority and follow-up. Changes to merge protection, permissions and documentation mode remain visible decisions.

## REPO-014 Private execution profile

MUST | repository | pr-and-release

Keep machine-specific paths, internal addresses, credentials, resource-admission rules and external-worker budgets in the owner's private execution profile. Public commands remain portable. Respect existing authorization boundaries and preserve other jobs. Do not copy private operational notes into the distributable kit.

## REPO-015 Verified adoption

MUST | repository | pr-and-release

Inventory applicability and evidence. Test clean bootstrap, safe synthetic-secret rejection, bad formatting, missing/deleted language counterparts, documentation-only whitespace changes and unavailable checks. Verify hosting enforcement after merge, then installation and profile acceptance. Distribute versioned, checksummed public-safe files; owner inventory stays private. A green generic check does not certify the whole product.

# Product profiles

## web Browser and PWA

Primary journeys, offline/error/loading states, accessibility, mobile layout, cache and upgrade behavior.

## mobile Android and Wear

Signed upgrade without data loss, target hardware, permission denial, background restrictions, reboot, timezone and phone/watch synchronization.

## cli CLI and desktop

Clean install, supported OS/shell, Unicode and space-containing paths, exit codes, cancellation, configuration migration.

## library Libraries and APIs

Minimal consumer example, contract/version compatibility, error semantics and supported runtime matrix.

## service Services containers and MCP

Least privilege, negative authorization, revocation, readiness vs liveness, restart, bounded queues, source/artifact/runtime identity.

## network Networking and VPN

Controller/channel loss, stale telemetry, failover and return, DNS/IPv6 routing boundaries, recovery access, latency under load.

## integration Integrations and themes

Advertised upstream versions, login/control paths, light/dark, long localized strings, keyboard and target platform acceptance.

## infra Infrastructure and GitOps

Explicit target/context, sanitized plan/diff, bounded application, readback, drift, rollback and actual repeat-application idempotence.

## monitoring Monitoring and exporters

Missing/stale data distinct from zero, counter reset, bounded label cardinality, limited agents and end-to-end alerts.

## data Data media and backup

Preserved source, durable jobs, corrupt input, duplicate delivery, pause/resume, full disk, isolated semantic restoration and retention.

## ml ML and speech

Licensed models and evaluation inputs, representative fixed quality set, silence/noise, model-version comparison, RAM/VRAM/cost, CPU fallback and cancellation.

## firmware Firmware and devices

Exact hardware revision, bounded physical actions, stop/disconnect behavior, no unintended restart, recovery path and actual-device proof.

## docs Documentation and templates

Working internal links, paired languages, accurate commands, truthful examples and successful template instantiation.
