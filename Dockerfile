# 1. 依然用 BUILDPLATFORM 跑 Go 编译器，速度最快
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai
WORKDIR /src
COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /musicon-go .

# 2. 拉取 NCM 镜像（自动拉取对应 TARGETARCH 的 NCM）
FROM ghcr.io/icezxf/ncm-server:latest AS ncm

# 3. 运行阶段
# 注意：这里我们使用 alpine，但把 apk add 放到 BUILDPLATFORM 里去下包，
# 或者直接复制宿主机的证书进去，这样就不需要 QEMU 执行了。
FROM alpine:3.20
# 复制构建阶段的证书，避免在目标镜像里执行 apk（不需要 QEMU）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /musicon-go /app/musicon-go
COPY static /app/static
COPY --from=ncm /app/ncm-server /app/ncm-server
RUN chmod +x /app/ncm-server

EXPOSE 8000
CMD ["sh", "-c", "/app/ncm-server & exec /app/musicon-go"]
