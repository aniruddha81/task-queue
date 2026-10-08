# docker build --target app --build-arg CMD=jobs .   (any Go service)
# docker build --target gateway --build-arg CMD=gateway .   (adds the dashboard)

# Multi-arch (AWS Graviton is arm64, Azure B-series v2 amd64): Go cross-compiles on the
# build host instead of running under emulation, and the dashboard is arch-independent.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD
# TAGS selects a mutant build for the chaos checker's self-test, e.g. mutant_nofence.
ARG TAGS=""
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags "${TAGS}" -o /out/app ./cmd/${CMD} && mkdir /out/acme

FROM gcr.io/distroless/static:nonroot AS app
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]

FROM app AS gateway
COPY --from=web /web/out /static
# Let's Encrypt certificate cache; a volume mounted here inherits the nonroot owner.
COPY --from=build --chown=65532:65532 /out/acme /acme
