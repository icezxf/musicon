# 1. 拉取 NCM 镜像作为 stage
# 【关键修复】删除了 --platform=$BUILDPLATFORM
# 这样在多平台构建时，Docker 会自动拉取和目标架构（TARGETARCH）一致的 ncm-server 镜像
FROM ghcr.io/icezxf/ncm-server:latest AS ncm

# 2. Go 构建阶段
# 这里保留 $BUILDPLATFORM 是正确的，让 Go 编译器以最高效的原生架构运行
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai
WORKDIR /src
COPY . .

# 接收目标平台架构，进行交叉编译
ARG TARGETOS
ARG TARGETARCH
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /musicon-go .

# 3. 运行阶段（会自动使用目标平台的基础镜像）
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /musicon-go /app/musicon-go
COPY static /app/static

# 从 ncm 阶段复制对应架构的二进制文件
COPY --from=ncm /app/ncm-server /app/ncm-server
RUN chmod +x /app/ncm-server

EXPOSE 8000

# 直接启动主程序，NCM 会由 Go 代码自动在后台拉起
CMD ["/app/musicon-go"]
