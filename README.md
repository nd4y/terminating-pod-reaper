# terminating-pod-reaper

🇬🇧 **English** | [🇷🇺 Русский](README.ru.md)

A `controller-runtime` operator that **watches pods** and force-deletes the ones stuck in
`Terminating` — as fast as possible, but safely.

A pod is deleted once its own `terminationGracePeriodSeconds` has expired (graceful shutdown
should already be over) **plus** an `extraGraceSeconds` buffer. The API server encodes
`terminationGracePeriodSeconds` into `metadata.deletionTimestamp` (= requestTime + grace) — that
is the deadline for graceful termination, not a guarantee that kubelet has already released the
node's resources by then: the container runtime, a sidecar (the classic one being an Istio proxy,
not always instant to react to SIGTERM) or a loaded node may finish seconds after the nominal
deadline — which is normal, not "stuck". Without the buffer, a force-delete at T+0 systematically
races legitimate (slightly slower) kubelet termination and wins: the pod object disappears from
the API before kubelet has actually killed the containers. The buffer gives kubelet a fair chance
to finish on its own; the operator steps in only if the pod outlived that allowance too — that is,
if it is genuinely stuck (dead node, hung finalizer) rather than merely a bit slower than usual.

## How it works

1. Watch pods (optionally restricted to given namespaces).
2. A predicate filters out everything except pods with a `deletionTimestamp` set (ordinary update
   traffic never disturbs reconcile).
3. `Reconcile`:
   - if `deletionTimestamp + extraGraceSeconds` is still ahead → `RequeueAfter` exactly up to it;
   - otherwise the pod outlived the grace period (+ buffer):
     - if **finalizers** are holding it → force-delete is powerless: only the
       `terminating_pod_reaper_pods_finalizer_blocked` metric is incremented and a log line is
       written (manual intervention required);
     - otherwise → force-delete (`grace-period=0`) with `Preconditions.UID` (race protection —
       so a new pod with the same name is never deleted).
4. If the pod disappears on its own, reconcile finishes without acting.

## Configuration

| Setting | Flag | Env | Default |
|---|---|---|---|
| Log only (safe mode) | `--dry-run` | `DRY_RUN` | `true` |
| Buffer on top of the grace period, sec | `--extra-grace-seconds` | `EXTRA_GRACE_SECONDS` | `60` |
| Hard watch restriction (ns list) | `--namespaces` | `NAMESPACES` | `""` (whole cluster) |
| Max deletions per window (0 = unlimited) | `--max-deletions-per-interval` | `MAX_DELETIONS_PER_INTERVAL` | `200` |
| Deletion rate-limit window, sec | `--rate-limit-window-seconds` | `RATE_LIMIT_WINDOW_SECONDS` | `30` |
| Concurrent reconcile workers | `--max-concurrent-reconciles` | `MAX_CONCURRENT_RECONCILES` | `4` |
| Cluster poll/resync period, sec | `--sync-period-seconds` | `SYNC_PERIOD_SECONDS` | `600` |
| Leader election (HA) | `--leader-elect` | — | automatic when `replicaCount > 1` |

The deletion limit is counted over a window of `rate-limit-window-seconds` (200 pods / 30s by
default) — a parameter separate from the cache resync: when a whole zone fails, the operator
clears a large backlog quickly in waves of 200 every 30s without avalanching the API server;
"excess" pods automatically roll over into the next window. `sync-period-seconds` is the full
resync of the watch cache, an insurance against missed events; it does not affect the deletion
limit and is usually much larger.

Env takes precedence over flag defaults.

> **`dry-run` is on by default** — the operator only logs and deletes nothing (an explicit
> warning is printed to the log at startup). For real deletion: `--set config.dryRun=false`.
>
> **Leader election turns on automatically** when there is more than one replica — then only the
> leader does the reaping (the chart also grants RBAC on `leases`). With a single replica it can
> be forced on via `leaderElection.enabled=true`.

### Namespace and pod filtering

On top of the hard `--namespaces` (which narrows the watch cache) there are "soft" filters
applied during reconcile. The logic: **exclude beats include**; if several include conditions are
set, they are ANDed (the namespace must pass all of them).

| Setting | Flag | Env | Meaning |
|---|---|---|---|
| Include ns by name regex | `--namespace-include-regex` | `NAMESPACE_INCLUDE_REGEX` | process only namespaces whose name matches the regex |
| Exclude ns by name regex | `--namespace-exclude-regex` | `NAMESPACE_EXCLUDE_REGEX` | skip namespaces whose name matches the regex (default `^kube-system$`) |
| Include ns by label | `--namespace-include-selector` | `NAMESPACE_INCLUDE_SELECTOR` | only namespaces matching the selector (e.g. `terminating-pod-reaper=enabled`) |
| Exclude ns by label | `--namespace-exclude-selector` | `NAMESPACE_EXCLUDE_SELECTOR` | skip namespaces matching the selector |
| Exclude pods by label | `--pod-exclude-selector` | `POD_EXCLUDE_SELECTOR` | never touch pods matching the selector (e.g. `terminating-pod-reaper.io/ignore=true`) |
| Allowed owners | `--reap-owner-kinds` | `REAP_OWNER_KINDS` | delete only pods managed by a controller of this Kind (default `ReplicaSet,Job`) |

A selector uses the standard Kubernetes label selector syntax (`key=value`, `key!=value`,
`key in (a,b)`, `key`, `!key`). Filtering **by namespace labels** requires reading `Namespace`
objects (cluster-scoped) — the chart grants read-only access to them automatically.

### Pod owner filter (zone / node failure)

By default (`ReplicaSet,Job`) the operator only touches pods whose **controller owner** is a
`ReplicaSet` (i.e. a Deployment) or a `Job` (i.e. a CronJob): such controllers will recreate the
pod in a healthy zone themselves. `StatefulSet`, `DaemonSet` and bare (ownerless) pods are
**skipped** — for a StatefulSet a force-delete is dangerous (split-brain). The owner is taken from
`pod.ownerReferences`, with no extra API calls.

This is exactly the zone-failure scenario in Yandex Cloud: when nodes become unreachable, the Node
Controller evicts the pods (sets `deletionTimestamp`), but they hang in `Terminating` while
kubelet is dead — terminating-pod-reaper finishes them off after the grace period, and the
Deployment/Job bring the replicas up in the remaining zones.
To lift the restriction: `--set '{config.ownerKinds}={}'` (an empty list = any owner).

Example — clean only namespaces labelled `terminating-pod-reaper=enabled`, except `kube-*`, and
leave labelled pods alone:

```bash
--set config.filters.namespaceIncludeSelector="terminating-pod-reaper=enabled" \
--set config.filters.namespaceExcludeRegex="^kube-" \
--set config.filters.podExcludeSelector="terminating-pod-reaper.io/ignore=true"
```

## Installing with Helm

```bash
# From the OCI registry (without --version the latest published version is installed):
helm install terminating-pod-reaper oci://ghcr.io/nd4y/charts/terminating-pod-reaper \
  --namespace terminating-pod-reaper --create-namespace \
  --set image.repository=ghcr.io/nd4y/terminating-pod-reaper

# Or from the local chart directory (dry-run by default, nothing is deleted):
helm install terminating-pod-reaper charts/terminating-pod-reaper \
  --namespace terminating-pod-reaper --create-namespace

kubectl -n terminating-pod-reaper logs deploy/terminating-pod-reaper -f
```

Main values (full list in [charts/terminating-pod-reaper/values.yaml](charts/terminating-pod-reaper/values.yaml)):

| Value | Default | Purpose |
|---|---|---|
| `config.dryRun` | `true` | log only (safe mode) |
| `config.extraGraceSeconds` | `60` | buffer on top of the pod's grace period before force-delete |
| `config.maxDeletionsPerInterval` | `200` | max deletions per `rateLimitWindowSeconds` window (0 = unlimited) |
| `config.rateLimitWindowSeconds` | `30` | deletion rate-limit window, sec |
| `config.maxConcurrentReconciles` | `4` | concurrent reconcile workers |
| `config.syncPeriodSeconds` | `600` | cluster poll/resync period, sec (does not affect the deletion limit) |
| `config.filters.namespaceExcludeRegex` | `^kube-system$` | protects kube-system |
| `podDisruptionBudget.enabled` | `true` | PDB when `replicaCount > 1` |
| `config.watchNamespaces` | `[]` | namespace list (empty = whole cluster) |
| `rbac.scope` | `cluster` | `cluster` or `namespaced` |
| `replicaCount` + `leaderElection.enabled` | `1` / `false` | HA |
| `metrics.serviceMonitor.enabled` | `false` | ServiceMonitor for Prometheus Operator |

Restricting to namespaces (least privilege — a Role in each namespace):

```bash
helm install terminating-pod-reaper charts/terminating-pod-reaper -n terminating-pod-reaper --create-namespace \
  --set rbac.scope=namespaced \
  --set '{config.watchNamespaces}={app-prod,app-staging}'
```

### Example values for a cluster-wide (HA) deployment

For a multi-zone cluster (e.g. 3 node groups across zones in Yandex Cloud): several replicas,
automatic leader election (only the leader reaps), replicas spread across zones, and the operator
itself leaving a failed node quickly.

```yaml
# values-ha.yaml
replicaCount: 3            # >1 → leader election is enabled automatically

config:
  dryRun: false            # real deletion (run it with dryRun: true first)
  # kube-system is excluded by default; ownerKinds defaults to ReplicaSet,Job

# Spread the operator's replicas across availability zones
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          topologyKey: topology.kubernetes.io/zone
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: terminating-pod-reaper

# So the operator's own pod leaves an unreachable node quickly (zone failure)
tolerations:
  - key: node.kubernetes.io/unreachable
    operator: Exists
    effect: NoExecute
    tolerationSeconds: 30
  - key: node.kubernetes.io/not-ready
    operator: Exists
    effect: NoExecute
    tolerationSeconds: 30

resources:
  requests: { cpu: 50m, memory: 64Mi }
  limits:   { cpu: 200m, memory: 128Mi }

metrics:
  serviceMonitor:
    enabled: true          # if Prometheus Operator is installed
```

```bash
helm install terminating-pod-reaper oci://ghcr.io/nd4y/charts/terminating-pod-reaper \
  -n terminating-pod-reaper --create-namespace \
  --set image.repository=ghcr.io/nd4y/terminating-pod-reaper \
  -f values-ha.yaml
```

> In HA only the current leader reaps; the other replicas are a hot standby. If the leader's zone
> fails, the `lease` is taken over by a live replica within a few seconds.

## Metrics (Prometheus, on `:8080/metrics`)

- `terminating_pod_reaper_pods_force_deleted_total{namespace}` — how many pods were deleted.
- `terminating_pod_reaper_delete_errors_total{namespace}` — force-delete errors.
- `terminating_pod_reaper_pods_skipped_total{namespace,reason}` — how many stuck pods were skipped
  by the filters (`owner_kind`, `pod_label`, `namespace`) or deferred by the rate limit
  (`rate_limited`).
- `terminating_pod_reaper_pods_finalizer_blocked{namespace}` — a **gauge**: how many pods right now
  have outlived the grace period but are held by finalizers (force-delete is powerless, manual
  intervention required) — a convenient `> 0` alert.
- plus the standard controller-runtime metrics (queue depth, reconcile duration and so on).

## Restricting to namespaces

`rbac.scope=cluster` (the default) grants a cluster-wide `ClusterRole`. For least privilege use
`rbac.scope=namespaced` + `config.watchNamespaces` — the chart will create a `Role`/`RoleBinding`
in each listed namespace and narrow the watch cache (see the `--set rbac.scope=namespaced` example
above). If namespace **label** filters are in play as well, the chart additionally grants a
read-only `ClusterRole` on `namespaces` only.

## Testing

Three levels, from fast to realistic:

| Level | What it checks | Where | How to run |
|---|---|---|---|
| Unit | filter logic (namespace/label/owner) | job `ci → go` | `go test ./...` |
| Integration (envtest) | reconcile against a real kube-apiserver: timing, owner filter, finalizer branch | job `ci → envtest` | `KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test -tags=integration ./...` |
| E2E (kind) | the full path: node "death" → a Deployment pod is reaped, a StatefulSet is left alone | workflow `e2e` (kind) | `bash test/e2e/run.sh` |

A real zone failure in Yandex Cloud (a SecurityGroup blocking traffic to a node group) is not
reproducible in CI — that is a chaos test for staging (possible as a separate `workflow_dispatch`
pipeline against a live cluster). CI simulates the *symptom* (pods in `Terminating` with the right
owners), not the *cause* (a network partition).

## ⚠️ Important

A force-delete removes the pod record from etcd but does **not** guarantee that the container on
the node stops (for example, if kubelet is unreachable). For a **StatefulSet** this risks a double
run (split-brain) — apply it deliberately. Mass hangs in `Terminating` are a symptom of a problem
(hung finalizers, failed nodes, volumes that will not unmount); the operator treats the effect,
not the cause.

## License

[MIT](LICENSE) — open source.
