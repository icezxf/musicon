# 从你的 GHCR 拉 ncm-server 镜像作为一个 stage
FROM ghcr.io/icezxf/ncm-server:latest AS ncm

# Go 构建阶段
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /musicon-go .

# 运行阶段
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /musicon-go /app/musicon-go
COPY static /app/static
COPY --from=ncm /app/ncm-server /app/ncm-server
RUN chmod +x /app/ncm-server

EXPOSE 8000
CMD ["/app/musicon-go"]
