# ==================== 阶段一：编译二进制 ====================
ARG TARGETOS
ARG TARGETARCH

FROM golang:1.26.4-alpine3.24 AS builder

ARG TARGETOS
ARG TARGETARCH

# 设置容器内的工作目录
WORKDIR /build

# 1. 优先复制依赖文件并下载，利用 Docker 缓存层
COPY go.mod go.sum ./
RUN go mod download

# 2. 复制剩下的所有源码
COPY . .

# 3. 静态编译（去掉 CGO，并剔除调试符号以缩减体积）
# 注意：入口是在 cmd/ddns/main.go，所以编译目标路径写 ./cmd/ddns
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o ddns ./cmd/ddns

# ==================== 阶段二：准备多架构的 OpenWrt 底座 ====================
FROM openwrt/rootfs:x86-64 AS base-amd64
FROM openwrt/rootfs:aarch64_generic AS base-arm64

# ==================== 阶段三：动态选择并打包 ====================
FROM base-${TARGETARCH}

ARG TARGETOS
ARG TARGETARCH

# OpenWrt 的包管理器是 opkg，安装证书保障 HTTPS 请求
RUN opkg update && \
    opkg install ca-bundle && \
    rm -rf /var/opkg-lists/*

WORKDIR /app
RUN mkdir -p /app/bin /app/config

# 从 builder 阶段把编译好的二进制文件拿过来
COPY --from=builder /build/ddns /app/bin/ddns
RUN chmod +x /app/bin/ddns

# 声明挂载点
VOLUME ["/app/config"]

ENTRYPOINT ["/app/bin/ddns", "-c", "/app/config/conf.yaml"]