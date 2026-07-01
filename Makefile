IMG ?= registry.example.com/terminating-pod-reaper:0.1.0

.PHONY: tidy build docker-build docker-push deploy undeploy run

tidy:            ## Сгенерировать go.sum и подтянуть зависимости
	go mod tidy

build: tidy      ## Локальная сборка бинаря
	CGO_ENABLED=0 go build -o bin/reaper .

run: tidy        ## Запуск локально против текущего kube-context
	go run . --threshold-seconds=120 --dry-run=true

docker-build:    ## Собрать образ
	docker build -t $(IMG) .

docker-push:     ## Запушить образ
	docker push $(IMG)

deploy:          ## Применить манифесты (подставьте IMG в deploy/operator.yaml)
	kubectl apply -f deploy/operator.yaml

undeploy:        ## Удалить оператор
	kubectl delete -f deploy/operator.yaml
