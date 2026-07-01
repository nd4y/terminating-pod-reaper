# terminating-pod-reaper

Оператор на `controller-runtime`, который **подписывается (watch) на поды** и принудительно
удаляет те, что зависли в состоянии `Terminating` дольше настраиваемого порога.

В отличие от CronJob, реакция почти мгновенная: как только под переходит в `Terminating`,
оператор ставит отложенную проверку ровно на момент `deletionTimestamp + threshold`.
Если под к этому времени всё ещё существует — делается force-delete (`grace-period=0`).

## Как это работает

1. Watch на поды (при желании — только в заданных namespace).
2. Predicate отфильтровывает всё, кроме подов с проставленным `deletionTimestamp`
   (обычный трафик апдейтов не тревожит reconcile).
3. `Reconcile`:
   - если `age < threshold` → `RequeueAfter: threshold - age` (ждём точно нужное время);
   - если `age >= threshold` → force-delete с `Preconditions.UID` (защита от гонки —
     не удалим новый под с тем же именем).
4. Если под исчезает сам — reconcile завершается без действий.

## Конфигурация

| Параметр | Флаг | Env | По умолчанию |
|---|---|---|---|
| Порог удаления, сек | `--threshold-seconds` | `THRESHOLD_SECONDS` | `120` |
| Только логировать | `--dry-run` | `DRY_RUN` | `false` |
| Жёсткое ограничение watch (список ns) | `--namespaces` | `NAMESPACES` | `""` (весь кластер) |
| Leader election (HA) | `--leader-elect` | — | `false` |

Env имеет приоритет над дефолтами флагов.

### Фильтрация namespace и подов

Поверх жёсткого `--namespaces` (который сужает watch-кэш) есть «мягкие» фильтры,
применяемые в reconcile. Логика: **exclude приоритетнее include**; если задано несколько
include-условий — они работают по И (namespace должен пройти все).

| Параметр | Флаг | Env | Смысл |
|---|---|---|---|
| Включить ns по regex имени | `--namespace-include-regex` | `NAMESPACE_INCLUDE_REGEX` | обрабатывать только ns, чьё имя матчит regex |
| Исключить ns по regex имени | `--namespace-exclude-regex` | `NAMESPACE_EXCLUDE_REGEX` | пропускать ns, чьё имя матчит regex |
| Включить ns по label | `--namespace-include-selector` | `NAMESPACE_INCLUDE_SELECTOR` | только ns с метками по selector (напр. `reaper=enabled`) |
| Исключить ns по label | `--namespace-exclude-selector` | `NAMESPACE_EXCLUDE_SELECTOR` | пропускать ns с метками по selector |
| Исключить поды по label | `--pod-exclude-selector` | `POD_EXCLUDE_SELECTOR` | не трогать поды с метками по selector (напр. `reaper.io/ignore=true`) |

Selector — стандартный синтаксис Kubernetes label selector (`key=value`, `key!=value`,
`key in (a,b)`, `key`, `!key`). Фильтрация **по меткам namespace** требует чтения объектов
`Namespace` (cluster-scoped) — чарт автоматически выдаёт read-only доступ к ним.

Пример: чистить только ns с меткой `reaper=enabled`, кроме `kube-*`, не трогая помеченные поды:

```bash
--set config.filters.namespaceIncludeSelector="reaper=enabled" \
--set config.filters.namespaceExcludeRegex="^kube-" \
--set config.filters.podExcludeSelector="reaper.io/ignore=true"
```

## Установка через Helm

```bash
# Из OCI-реестра (после релиза тега):
helm install reaper oci://ghcr.io/nd4y/charts/terminating-pod-reaper \
  --version 0.1.0 \
  --namespace pod-reaper --create-namespace \
  --set image.repository=ghcr.io/nd4y/terminating-pod-reaper \
  --set config.thresholdSeconds=120

# Или из локальной папки чарта:
helm install reaper charts/terminating-pod-reaper \
  --namespace pod-reaper --create-namespace \
  --set config.dryRun=true

kubectl -n pod-reaper logs deploy/reaper-terminating-pod-reaper -f
```

Основные values (полный список — в [charts/terminating-pod-reaper/values.yaml](charts/terminating-pod-reaper/values.yaml)):

| Value | По умолчанию | Назначение |
|---|---|---|
| `config.thresholdSeconds` | `120` | порог удаления, сек |
| `config.dryRun` | `false` | только логировать |
| `config.watchNamespaces` | `[]` | список namespace (пусто = весь кластер) |
| `rbac.scope` | `cluster` | `cluster` или `namespaced` |
| `replicaCount` + `leaderElection.enabled` | `1` / `false` | HA |
| `metrics.serviceMonitor.enabled` | `false` | ServiceMonitor для Prometheus Operator |

Ограничение namespace (наименьшие привилегии — Role в каждом ns):

```bash
helm install reaper charts/terminating-pod-reaper -n pod-reaper --create-namespace \
  --set rbac.scope=namespaced \
  --set '{config.watchNamespaces}={app-prod,app-staging}'
```

## Метрики (Prometheus, на `:8080/metrics`)

- `reaper_pods_force_deleted_total{namespace}` — сколько подов удалено.
- `reaper_delete_errors_total{namespace}` — ошибки force-delete.
- `reaper_pods_skipped_total{namespace,reason}` — сколько зависших подов пропущено фильтрами.
- плюс стандартные метрики controller-runtime (глубина очереди, длительность reconcile и т.д.).

## Ограничение по namespace

`rbac.scope=cluster` (по умолчанию) даёт `ClusterRole` на весь кластер. Для наименьших
привилегий используйте `rbac.scope=namespaced` + `config.watchNamespaces` — чарт создаст
`Role`/`RoleBinding` в каждом из указанных namespace и сузит watch-кэш (см. пример с
`--set rbac.scope=namespaced` выше). Если при этом задействованы фильтры по **меткам**
namespace, чарт дополнительно выдаёт read-only `ClusterRole` только на `namespaces`.

## ⚠️ Важно

Force-delete убирает запись пода из etcd, но **не гарантирует** остановку контейнера на ноде
(например, если kubelet недоступен). Для **StatefulSet** это риск двойного запуска (split-brain) —
применяйте осознанно. Массовые зависания в `Terminating` — симптом проблемы (зависшие finalizers,
упавшие ноды, не отмонтируемые volume); оператор лечит следствие, а не причину.

## Лицензия

[MIT](LICENSE) — open source.
