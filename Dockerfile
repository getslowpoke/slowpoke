# syntax=docker/dockerfile:1
# Slowpoke images, built from source:
#   docker build --target server -t slowpoke/server .
#   docker build --target agent  -t slowpoke/agent .

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# Writes the UI to /src/internal/server/web/dist, embedded in the server binary.
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/server/web/dist/ internal/server/web/dist/
ARG VERSION=0.1.0-dev
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/slowpoke-server ./cmd/slowpoke-server \
 && go build -trimpath -ldflags "-s -w -X github.com/getslowpoke/slowpoke/internal/agent.Version=${VERSION}" -o /out/slowpoke-agent ./cmd/slowpoke-agent \
 && mkdir -p /out/data
# The server hands these out at /agent/<platform>: a machine installs with one command and always
# gets the agent of the version it talks to.
RUN GOARCH=amd64 go build -trimpath -ldflags "-s -w -X github.com/getslowpoke/slowpoke/internal/agent.Version=${VERSION}" -o /out/agents/slowpoke-agent-linux-amd64 ./cmd/slowpoke-agent \
 && GOARCH=arm64 go build -trimpath -ldflags "-s -w -X github.com/getslowpoke/slowpoke/internal/agent.Version=${VERSION}" -o /out/agents/slowpoke-agent-linux-arm64 ./cmd/slowpoke-agent

# Server: API, UI and workers in one static binary, no shell, unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=build /out/slowpoke-server /usr/local/bin/slowpoke-server
COPY --from=build /out/agents/ /usr/local/share/slowpoke/
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV SLOWPOKE_DATABASE_URL=sqlite:///data/slowpoke.db \
    SLOWPOKE_LISTEN=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/usr/local/bin/slowpoke-server", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/slowpoke-server"]
CMD ["serve"]

# Agent: runs next to the application. It reads logs owned by root or adm, so it keeps
# root inside the container; mount everything read-only (see docs/install/agent.md).
FROM gcr.io/distroless/static-debian12 AS agent
COPY --from=build /out/slowpoke-agent /usr/local/bin/slowpoke-agent
ENTRYPOINT ["/usr/local/bin/slowpoke-agent"]
CMD ["-config", "/etc/slowpoke/agent.yaml"]
