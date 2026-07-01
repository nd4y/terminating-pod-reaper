# ---- build ----
# BUILDPLATFORM = архитектура раннера (amd64). Сборка идёт нативно на ней,
# а Go кросс-компилирует под TARGETARCH — без медленной QEMU-эмуляции.
FROM --platform=$BUILDPLATFORM golang:1.22 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY . .
# Кэш-маунты BuildKit ускоряют повторные сборки (модули и build-кэш).
# go.sum может отсутствовать в репозитории — приводим модуль в порядок здесь.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod tidy && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -a -ldflags="-s -w" -o /out/reaper .

# ---- runtime ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/reaper /reaper
USER 65532:65532
ENTRYPOINT ["/reaper"]
