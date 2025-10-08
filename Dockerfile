# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS builder
WORKDIR /app

# зависимости
RUN apk add --no-cache git build-base

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Собираем статически
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/bot ./cmd/bot

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app

# Папка для БД и коллекций
USER nonroot:nonroot
COPY --from=builder /bin/bot /app/bot
COPY collections /app/collections

# Порт для healthcheck (опционально)
EXPOSE 8080

ENV LEADS_SQLITE_DSN=/app/leads.db

ENTRYPOINT ["/app/bot"]

