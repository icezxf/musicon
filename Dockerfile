FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /musicon-go .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /musicon-go /app/musicon-go
COPY static/ /app/static/
RUN mkdir -p /app/data/covers
ENV LISTEN=0.0.0.0:8000 \
    DB_PATH=/app/data/musicon.db \
    DATA_DIR=/app/data \
    STATIC_DIR=/app/static
EXPOSE 8000
VOLUME ["/app/data"]
CMD ["/app/musicon-go"]
