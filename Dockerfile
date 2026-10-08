# docker build --target app --build-arg CMD=jobs .   (any Go service)
# docker build --target gateway --build-arg CMD=gateway .   (adds the dashboard)

FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD
RUN CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static:nonroot AS app
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]

FROM app AS gateway
COPY --from=web /web/out /static
