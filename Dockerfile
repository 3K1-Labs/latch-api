# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o latch-backend ./cmd/server

# Runtime stage — minimal Alpine image (~10 MB total)
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata \
    && adduser -D -H -u 10001 latch

WORKDIR /app
COPY --from=builder /app/latch-backend .

USER latch

EXPOSE 8080

ENTRYPOINT ["./latch-backend"]
