# 构建阶段（照旧）
# ...

# 运行阶段，无操作系统，不需要 apk，极速构建！
FROM scratch
WORKDIR /app
# 复制证书和时区（从 builder 拿）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
ENV TZ=Asia/Shanghai

# 复制静态编译的 Go 主程序
COPY --from=builder /musicon-go /app/musicon-go

# 复制 NCM 二进制（注意：如果是静态的可以直接跑，如果依赖动态库，就不能用 scratch）
COPY --from=ncm /app/ncm-server /app/ncm-server

EXPOSE 8000
CMD ["sh", "-c", "/app/ncm-server & exec /app/musicon-go"]
