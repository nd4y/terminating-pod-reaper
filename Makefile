IMG ?= registry.example.com/terminating-pod-reaper:0.1.0

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

deploy:          ## Установить через Helm (IMG задаёт образ)
	helm upgrade --install terminating-pod-reaper charts/terminating-pod-reaper \
		--namespace terminating-pod-reaper --create-namespace \
		--set image.repository=$(firstword $(subst :, ,$(IMG))) \
		--set image.tag=$(lastword $(subst :, ,$(IMG)))

undeploy:        ## Удалить релиз Helm
	helm uninstall terminating-pod-reaper --namespace terminating-pod-reaper
