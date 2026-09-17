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
