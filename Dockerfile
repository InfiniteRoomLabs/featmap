# Multi-stage build for the Featmap fork (MCP server + API-key auth).
#
# Mirrors the host build scripts (build/build_webapp.sh, build/generate.sh):
#   webui  -> pnpm + CRA4 webapp build (legacy OpenSSL provider for webpack4 md4)
#   build  -> go-bindata v4 embed + static go build
#   final  -> distroless static, nonroot
#
# Supply-chain stance: every base image is digest-pinned (no tag drift), pnpm is
# version-pinned and installs with --frozen-lockfile (integrity-verified, no version
# resolution -- so the global minimum-release-age gate is moot by construction), and
# postinstall scripts stay gated by package.json -> pnpm.onlyBuiltDependencies (no
# dangerously-allow-all-builds). The Go stage builds -mod=readonly with a local
# toolchain (no unpinned toolchain download) against the checked-in go.sum.

# ---- Stage 1: webapp (CRA 4 / React 17) ----
FROM node@sha256:3d0f05455dea2c82e2f76e7e2543964c30f6b7d673fc1a83286736d44fe4c41c AS webui
# webpack 4 (CRA 4) uses md4 hashes OpenSSL 3 dropped. Required for Node 17+.
ENV NODE_OPTIONS=--openssl-legacy-provider
RUN corepack enable && corepack prepare pnpm@10.32.1 --activate
WORKDIR /app/webapp
# Manifest + lockfile + per-project .npmrc first, for a cacheable install layer.
COPY webapp/package.json webapp/pnpm-lock.yaml webapp/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY webapp/ ./
RUN pnpm run build

# ---- Stage 2: go binary (embeds migrations, templates, webapp build) ----
FROM golang@sha256:c99705d76da262268a7d29ff9638b2ad51d141512fea8489f5bad3e4a6e95d07 AS build
ENV GOFLAGS=-mod=readonly \
    GOTOOLCHAIN=local \
    CGO_ENABLED=0
# Fork uses the maintained go-bindata (kevinburke/v4), not the archived jteeuwen one.
RUN go install github.com/kevinburke/go-bindata/v4/...@v4.0.2
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=webui /app/webapp/build ./webapp/build
# Same three bundles as build/generate.sh.
RUN cd migrations && go-bindata -pkg migrations . && cd .. \
 && go-bindata -pkg tmpl -o ./tmpl/bindata.go ./tmpl/ \
 && go-bindata -pkg webapp -o ./webapp/bindata.go ./webapp/build/...
RUN go build -trimpath -ldflags "-s -w" -o /out/featmap .

# ---- Stage 3: runtime (distroless static, nonroot uid 65532) ----
FROM gcr.io/distroless/static-debian12@sha256:b669b9df05a88a085fefed6520c6d2268aabacf3008b149ddf877e752ae89400
COPY --from=build /out/featmap /opt/featmap/featmap
WORKDIR /opt/featmap
EXPOSE 5001
# conf.json is bind-mounted at runtime (see docker-compose.yml). featmap reads it from cwd.
ENTRYPOINT ["/opt/featmap/featmap"]
