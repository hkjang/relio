# syntax=docker/dockerfile:1.7

FROM node:24-bookworm-slim AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24-bookworm AS go-build
ARG VERSION=0.1.0-dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/internal/webui/dist/ ./internal/webui/dist/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test ./... && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
      -ldflags="-s -w -X github.com/hkjang/relio/internal/platform/version.Version=${VERSION} -X github.com/hkjang/relio/internal/platform/version.GitCommit=${GIT_COMMIT} -X github.com/hkjang/relio/internal/platform/version.BuildDate=${BUILD_DATE} -X github.com/hkjang/relio/internal/platform/version.Edition=Community" \
      -o /out/relio ./cmd/relio

FROM debian:bookworm-slim
# 떠 있는 이미지에서 어느 소스로 만든 것인지 알 수 있게 한다. ARG 는
# 스테이지마다 다시 선언해야 값이 넘어온다.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="Relio" \
      org.opencontainers.image.description="Relio release and delivery server" \
      org.opencontainers.image.source="https://github.com/hkjang/relio" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --gid 10001 relio && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin relio && \
    mkdir -p /var/lib/relio/secrets /var/lib/relio/uploads /var/lib/relio/exports && \
    chown -R 10001:10001 /var/lib/relio
COPY --from=go-build /out/relio /usr/local/bin/relio
USER 10001:10001
VOLUME ["/var/lib/relio"]
EXPOSE 8080
HEALTHCHECK --interval=20s --timeout=5s --start-period=20s --retries=5 CMD ["/usr/local/bin/relio", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/relio"]
