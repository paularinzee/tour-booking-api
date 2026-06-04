# Stage 1: Build
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o main ./cmd/main.go

# Stage 2: Production
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

RUN mkdir -p /app/public/img/tours

WORKDIR /app

COPY --from=builder /app/main .
COPY --from=builder /app/public ./public

EXPOSE 8080

USER nobody

CMD ["./main"]