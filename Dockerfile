# Dockerfile genérico para todos los binarios Go del monorepo.
# Uso: docker build --build-arg SERVICE_PATH=servicios/audios .
FROM golang:1.24-alpine AS build

ARG SERVICE_PATH
WORKDIR /src

# Dependencias primero para aprovechar la caché de capas.
COPY go.mod go.sum ./
RUN go mod download

COPY pkg ./pkg
COPY ${SERVICE_PATH} ./${SERVICE_PATH}
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./${SERVICE_PATH}

# --- Imagen final mínima --------------------------------------------------
FROM alpine:3.20

# wget (busybox) se usa en los healthchecks de docker-compose.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 app \
    && mkdir -p /data/audio \
    && chown -R 10001:10001 /data/audio
USER app

COPY --from=build /out/app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]

