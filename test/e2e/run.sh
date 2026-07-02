#!/usr/bin/env bash
# E2E: имитируем отказ ноды (как отказ зоны в Yandex Cloud) и проверяем, что оператор
# добивает Terminating-под Deployment'а, но НЕ трогает под StatefulSet.
#
# Требует: kind, kubectl, helm, docker.
set -euo pipefail

CLUSTER="${CLUSTER:-terminating-pod-reaper-e2e}"
IMG="${IMG:-terminating-pod-reaper:e2e}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${HERE}/../.." && pwd)"

DEAD_NODE="${CLUSTER}-worker"    # ноду убиваем
LIVE_NODE="${CLUSTER}-worker2"   # здесь живёт оператор

cleanup() { kind delete cluster --name "${CLUSTER}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> Создаём kind-кластер (1 cp + 2 worker)"
kind create cluster --name "${CLUSTER}" --config "${HERE}/kind-config.yaml" --wait 120s

echo "==> Собираем и загружаем образ оператора"
docker build -t "${IMG}" "${ROOT}"
kind load docker-image "${IMG}" --name "${CLUSTER}"

echo "==> Ставим оператор (пин на ${LIVE_NODE}, dry-run=false)"
helm install terminating-pod-reaper "${ROOT}/charts/terminating-pod-reaper" \
  --namespace terminating-pod-reaper --create-namespace \
  --set image.repository="${IMG%:*}" \
  --set image.tag="${IMG##*:}" \
  --set image.pullPolicy=IfNotPresent \
  --set config.dryRun=false \
  --set nodeSelector."kubernetes\.io/hostname"="${LIVE_NODE}" \
  --wait --timeout 120s

echo "==> Разворачиваем тестовую нагрузку на ${DEAD_NODE}"
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata: { name: web, namespace: default }
spec:
  replicas: 1
  selector: { matchLabels: { app: web } }
  template:
    metadata: { labels: { app: web } }
    spec:
      nodeSelector: { kubernetes.io/hostname: ${DEAD_NODE} }
      terminationGracePeriodSeconds: 20
      containers: [{ name: c, image: registry.k8s.io/pause:3.9 }]
---
apiVersion: apps/v1
kind: StatefulSet
metadata: { name: db, namespace: default }
spec:
  replicas: 1
  serviceName: db
  selector: { matchLabels: { app: db } }
  template:
    metadata: { labels: { app: db } }
    spec:
      nodeSelector: { kubernetes.io/hostname: ${DEAD_NODE} }
      terminationGracePeriodSeconds: 20
      containers: [{ name: c, image: registry.k8s.io/pause:3.9 }]
EOF

kubectl rollout status deploy/web --timeout=120s
kubectl rollout status statefulset/db --timeout=120s

DEP_POD="$(kubectl get pod -l app=web -o jsonpath='{.items[0].metadata.name}')"
SS_POD="$(kubectl get pod -l app=db -o jsonpath='{.items[0].metadata.name}')"
echo "    deployment pod = ${DEP_POD}"
echo "    statefulset pod = ${SS_POD}"

echo "==> Убиваем ноду ${DEAD_NODE} (docker stop) — эмуляция отказа зоны"
docker stop "${DEAD_NODE}"

echo "==> Инициируем graceful-удаление застрявших подов (kubelet мёртв → Terminating)"
kubectl delete pod "${DEP_POD}" --grace-period=20 --wait=false
kubectl delete pod "${SS_POD}" --grace-period=20 --wait=false

echo "==> Ждём, что оператор добьёт под Deployment (до 120с)"
deadline=$((SECONDS + 120))
while kubectl get pod "${DEP_POD}" >/dev/null 2>&1; do
  if (( SECONDS > deadline )); then
    echo "FAIL: под Deployment ${DEP_POD} не был удалён оператором"
    kubectl -n terminating-pod-reaper logs deploy/terminating-pod-reaper --tail=50 || true
    exit 1
  fi
  sleep 3
done
echo "OK: под Deployment добит"

echo "==> Проверяем, что под StatefulSet НЕ тронут (должен всё ещё быть Terminating)"
if ! kubectl get pod "${SS_POD}" >/dev/null 2>&1; then
  echo "FAIL: под StatefulSet ${SS_POD} был удалён — так быть не должно"
  exit 1
fi
echo "OK: под StatefulSet не тронут"

echo "E2E PASSED"
