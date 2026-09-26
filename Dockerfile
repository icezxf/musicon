# 从你的 GHCR 拉 ncm-server 镜像作为一个 stage
# 关键：明确使用 $BUILDPLATFORM，确保拉取的是构建机原生架构的镜像
FROM --platform=$BUILDPLATFORM ghcr.io/icezxf/ncm-server:latest AS ncm

# Go 构建阶段
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai
WORKDIR /src
COPY . .

# 关键：传入 TARGETOS 和 TARGETARCH，实现交叉编译
ARG TARGETOS
ARG TARGETARCH
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /musicon-go .

# 运行阶段（使用目标平台的基础镜像）
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /musicon-go /app/musicon-go
COPY static /app/static
# 从 ncm stage 复制对应架构的二进制文件
COPY --from=ncm /app/ncm-server /app/ncm-server
RUN chmod +x /app/ncm-server

EXPOSE 8000
CMD ["/app/musicon-go"]
