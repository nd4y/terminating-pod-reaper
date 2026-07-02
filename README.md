# terminating-pod-reaper

Оператор на `controller-runtime`, который **подписывается (watch) на поды** и принудительно
удаляет те, что застряли в состоянии `Terminating` — как можно быстрее, но безопасно.

Отдельного настраиваемого порога нет: под удаляется ровно тогда, когда истёк его собственный
`terminationGracePeriodSeconds` (штатная остановка уже должна была завершиться). Это значение
API-сервер кодирует в `metadata.deletionTimestamp` (= времяЗапроса + grace). Пока дедлайн не
наступил — под в своём законном grace-периоде, и его не трогают.

## Как это работает

1. Watch на поды (при желании — только в заданных namespace).
2. Predicate отфильтровывает всё, кроме подов с проставленным `deletionTimestamp`
   (обычный трафик апдейтов не тревожит reconcile).
3. `Reconcile`:
   - если дедлайн (`deletionTimestamp`) ещё впереди → `RequeueAfter` ровно до него;
   - иначе под пережил grace-период:
     - если его держат **finalizers** → force-delete бессилен: только инкремент метрики
       `reaper_pods_finalizer_blocked_total` и лог (нужно ручное вмешательство);
     - иначе → force-delete (`grace-period=0`) с `Preconditions.UID` (защита от гонки —
       не удалим новый под с тем же именем).
4. Если под исчезает сам — reconcile завершается без действий.

## Конфигурация

| Параметр | Флаг | Env | По умолчанию |
|---|---|---|---|
| Только логировать (безопасный режим) | `--dry-run` | `DRY_RUN` | `true` |
| Жёсткое ограничение watch (список ns) | `--namespaces` | `NAMESPACES` | `""` (весь кластер) |
| Leader election (HA) | `--leader-elect` | — | авто при `replicaCount > 1` |

Env имеет приоритет над дефолтами флагов.

> **По умолчанию включён `dry-run`** — оператор только логирует, ничего не удаляет
> (в лог при старте выводится явное предупреждение). Для реального удаления:
> `--set config.dryRun=false`.
>
> **Leader election включается автоматически**, если реплик больше одной — тогда только
> лидер выполняет reaping (чарт заодно выдаёт RBAC на `leases`). При одной реплике можно
> включить принудительно через `leaderElection.enabled=true`.

### Фильтрация namespace и подов

Поверх жёсткого `--namespaces` (который сужает watch-кэш) есть «мягкие» фильтры,
применяемые в reconcile. Логика: **exclude приоритетнее include**; если задано несколько
include-условий — они работают по И (namespace должен пройти все).

| Параметр | Флаг | Env | Смысл |
|---|---|---|---|
| Включить ns по regex имени | `--namespace-include-regex` | `NAMESPACE_INCLUDE_REGEX` | обрабатывать только ns, чьё имя матчит regex |
| Исключить ns по regex имени | `--namespace-exclude-regex` | `NAMESPACE_EXCLUDE_REGEX` | пропускать ns, чьё имя матчит regex (по умолчанию `^kube-system$`) |
| Включить ns по label | `--namespace-include-selector` | `NAMESPACE_INCLUDE_SELECTOR` | только ns с метками по selector (напр. `reaper=enabled`) |
| Исключить ns по label | `--namespace-exclude-selector` | `NAMESPACE_EXCLUDE_SELECTOR` | пропускать ns с метками по selector |
| Исключить поды по label | `--pod-exclude-selector` | `POD_EXCLUDE_SELECTOR` | не трогать поды с метками по selector (напр. `reaper.io/ignore=true`) |
| Разрешённые владельцы | `--reap-owner-kinds` | `REAP_OWNER_KINDS` | удалять только поды под управлением контроллера с таким Kind (по умолчанию `ReplicaSet,Job`) |

Selector — стандартный синтаксис Kubernetes label selector (`key=value`, `key!=value`,
`key in (a,b)`, `key`, `!key`). Фильтрация **по меткам namespace** требует чтения объектов
`Namespace` (cluster-scoped) — чарт автоматически выдаёт read-only доступ к ним.

### Фильтр по владельцу пода (отказ зоны/ноды)

По умолчанию (`ReplicaSet,Job`) оператор трогает только поды, чей **controller-owner** —
`ReplicaSet` (т.е. Deployment) или `Job` (т.е. CronJob): такие контроллеры сами пересоздадут
под в живой зоне. Поды `StatefulSet`, `DaemonSet` и «голые» (без владельца) **пропускаются** —
для StatefulSet force-delete опасен (split-brain). Owner берётся из `pod.ownerReferences`,
доп. запросов к API нет.

Это ровно сценарий отказа зоны в Yandex Cloud: при недоступности нод Node Controller выселяет
поды (ставит `deletionTimestamp`), но они виснут в `Terminating`, пока kubelet мёртв —
terminating-pod-reaper добивает их после grace-периода, и Deployment/Job поднимают реплики
в оставшихся зонах.
Снять ограничение: `--set '{config.ownerKinds}={}'` (пустой список = любой владелец).

Пример: чистить только ns с меткой `reaper=enabled`, кроме `kube-*`, не трогая помеченные поды:

```bash
--set config.filters.namespaceIncludeSelector="reaper=enabled" \
--set config.filters.namespaceExcludeRegex="^kube-" \
--set config.filters.podExcludeSelector="reaper.io/ignore=true"
```

## Установка через Helm

```bash
# Из OCI-реестра (без --version ставится последняя опубликованная версия):
helm install reaper oci://ghcr.io/nd4y/charts/terminating-pod-reaper \
  --namespace pod-reaper --create-namespace \
  --set image.repository=ghcr.io/nd4y/terminating-pod-reaper

# Или из локальной папки чарта (по умолчанию dry-run, ничего не удаляется):
helm install reaper charts/terminating-pod-reaper \
  --namespace pod-reaper --create-namespace

kubectl -n pod-reaper logs deploy/reaper-terminating-pod-reaper -f
```

Основные values (полный список — в [charts/terminating-pod-reaper/values.yaml](charts/terminating-pod-reaper/values.yaml)):

| Value | По умолчанию | Назначение |
|---|---|---|
| `config.dryRun` | `true` | только логировать (безопасный режим) |
| `config.filters.namespaceExcludeRegex` | `^kube-system$` | защита kube-system |
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

### Пример values для кластерного (HA) развёртывания

Для кластера с несколькими зонами (напр. 3 группы нод по зонам в Yandex Cloud): несколько
реплик, автоматический leader election (reaping делает только лидер), разнос реплик по зонам
и быстрый уход самого оператора с упавшей ноды.

```yaml
# values-ha.yaml
replicaCount: 3            # >1 → leader election включается автоматически

config:
  dryRun: false            # реальное удаление (сначала обкатайте с dryRun: true)
  # kube-system исключён по умолчанию; ownerKinds по умолчанию ReplicaSet,Job

# Разносим реплики оператора по зонам доступности
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          topologyKey: topology.kubernetes.io/zone
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: terminating-pod-reaper

# Чтобы под оператора сам быстро уезжал с недоступной ноды (отказ зоны)
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
    enabled: true          # если стоит Prometheus Operator
```

```bash
helm install reaper oci://ghcr.io/nd4y/charts/terminating-pod-reaper \
  -n pod-reaper --create-namespace \
  --set image.repository=ghcr.io/nd4y/terminating-pod-reaper \
  -f values-ha.yaml
```

> Reaping в HA выполняет только текущий лидер, остальные реплики — горячий резерв.
> При отказе зоны с лидером аренда (`lease`) перехватывается живой репликой за несколько секунд.

## Метрики (Prometheus, на `:8080/metrics`)

- `reaper_pods_force_deleted_total{namespace}` — сколько подов удалено.
- `reaper_delete_errors_total{namespace}` — ошибки force-delete.
- `reaper_pods_skipped_total{namespace,reason}` — сколько зависших подов пропущено фильтрами.
- `reaper_pods_finalizer_blocked_total{namespace}` — поды, пережившие grace-период, но
  удерживаемые finalizers (force-delete бессилен, требуется ручное вмешательство) —
  удобно вешать алерт.
- плюс стандартные метрики controller-runtime (глубина очереди, длительность reconcile и т.д.).

## Ограничение по namespace

`rbac.scope=cluster` (по умолчанию) даёт `ClusterRole` на весь кластер. Для наименьших
привилегий используйте `rbac.scope=namespaced` + `config.watchNamespaces` — чарт создаст
`Role`/`RoleBinding` в каждом из указанных namespace и сузит watch-кэш (см. пример с
`--set rbac.scope=namespaced` выше). Если при этом задействованы фильтры по **меткам**
namespace, чарт дополнительно выдаёт read-only `ClusterRole` только на `namespaces`.

## Тестирование

Три уровня, от быстрого к реалистичному:

| Уровень | Что проверяет | Где | Как запустить |
|---|---|---|---|
| Unit | логика фильтров (namespace/label/owner) | job `ci → go` | `go test ./...` |
| Интеграционные (envtest) | reconcile против настоящего kube-apiserver: тайминг, owner-фильтр, ветка finalizer | job `ci → envtest` | `KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test -tags=integration ./...` |
| E2E (kind) | полный путь: «смерть» ноды → добивание пода Deployment, StatefulSet не тронут | workflow `e2e` (kind) | `bash test/e2e/run.sh` |

Реальный отказ зоны в Yandex Cloud (SecurityGroup, блокирующая трафик группе нод) в CI не
воспроизводится — это chaos-тест для staging (можно отдельным `workflow_dispatch`-пайплайном
против живого кластера). В CI имитируется *симптом* (поды в `Terminating` с нужными owner’ами),
а не *причина* (сетевой разрыв).

## ⚠️ Важно

Force-delete убирает запись пода из etcd, но **не гарантирует** остановку контейнера на ноде
(например, если kubelet недоступен). Для **StatefulSet** это риск двойного запуска (split-brain) —
применяйте осознанно. Массовые зависания в `Terminating` — симптом проблемы (зависшие finalizers,
упавшие ноды, не отмонтируемые volume); оператор лечит следствие, а не причину.

## Лицензия

[MIT](LICENSE) — open source.
