FROM golang:1.22-bookworm AS base

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

FROM base AS dev

RUN go install github.com/air-verse/air@v1.52.3

COPY . .

EXPOSE 8080

CMD ["air", "-c", ".air.toml"]

FROM base AS build

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/api /api

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/api"]

FROM base AS migration-build
RUN go install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1

FROM debian:bookworm-slim AS migrator
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=migration-build /go/bin/migrate /usr/local/bin/migrate
COPY migrations /migrations
USER 65532:65532
ENTRYPOINT ["migrate"]
