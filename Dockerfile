# ---- build ----
# BUILDPLATFORM = архитектура раннера (amd64). Сборка идёт нативно на ней,
# а Go кросс-компилирует под TARGETARCH — без медленной QEMU-эмуляции.
FROM --platform=$BUILDPLATFORM golang:1.22 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Сначала только манифесты зависимостей — слой download кэшируется,
# пока go.mod/go.sum не меняются. go.sum обязателен: версии зафиксированы.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -a -ldflags="-s -w" -o /out/terminating-pod-reaper .

# ---- runtime ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /out/terminating-pod-reaper /terminating-pod-reaper
USER 65532:65532
ENTRYPOINT ["/terminating-pod-reaper"]
