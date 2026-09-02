# syntax=docker/dockerfile:1.7
FROM golang:1.27.0-bookworm AS build
WORKDIR /src
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -buildvcs=false -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/vigil ./cmd/vigil

FROM debian:13-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 vigil \
    && useradd --system --uid 10001 --gid vigil --home-dir /nonexistent --shell /usr/sbin/nologin vigil
COPY --from=build --chown=10001:10001 /out/vigil /usr/local/bin/vigil
USER 10001:10001
WORKDIR /app
ENTRYPOINT ["/usr/local/bin/vigil"]
CMD ["api"]
