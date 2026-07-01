# ---- build ----
FROM golang:1.22 AS build
WORKDIR /src

COPY . .
# go.sum может отсутствовать в репозитории — приводим модуль в порядок здесь.
RUN go mod tidy
# Статическая сборка под distroless
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o /out/reaper .

# ---- runtime ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/reaper /reaper
USER 65532:65532
ENTRYPOINT ["/reaper"]
