# Репозиторий и тег задаются раздельно, чтобы не ломаться на реестрах
# с портом (localhost:5000/...). IMG собирается из них.
IMG_REPO ?= ghcr.io/nd4y/terminating-pod-reaper
IMG_TAG  ?= dev
IMG      ?= $(IMG_REPO):$(IMG_TAG)

.PHONY: tidy build docker-build docker-push deploy undeploy run

tidy:            ## Сгенерировать go.sum и подтянуть зависимости
	go mod tidy

build: tidy      ## Локальная сборка бинаря
	CGO_ENABLED=0 go build -o bin/terminating-pod-reaper .

run: tidy        ## Запуск локально против текущего kube-context
	go run . --dry-run=true

docker-build:    ## Собрать образ
	docker build -t $(IMG) .

docker-push:     ## Запушить образ
	docker push $(IMG)

deploy:          ## Установить через Helm (IMG_REPO/IMG_TAG задают образ)
	helm upgrade --install terminating-pod-reaper charts/terminating-pod-reaper \
		--namespace terminating-pod-reaper --create-namespace \
		--set image.repository=$(IMG_REPO) \
		--set image.tag=$(IMG_TAG)

undeploy:        ## Удалить релиз Helm
	helm uninstall terminating-pod-reaper --namespace terminating-pod-reaper
