# ==================== 阶段一：编译二进制 ====================
ARG TARGETOS
ARG TARGETARCH

FROM golang:1.26.4-alpine3.24 AS builder

ARG TARGETOS
ARG TARGETARCH

# 设置容器内的工作目录
WORKDIR /build

# 换源加速（如果在国内服务器构建，建议保留；海外可删掉下一行）
ENV GOPROXY=https://goproxy.cn,direct

# 1. 优先复制依赖文件并下载，利用 Docker 缓存层
COPY go.mod go.sum ./
RUN go mod download

# 2. 复制剩下的所有源码
COPY . .

# 3. 静态编译（去掉 CGO，并剔除调试符号以缩减体积）
# 注意：入口是在 cmd/ddns/main.go，所以编译目标路径写 ./cmd/ddns
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o ddns ./cmd/ddns

# ==================== 阶段二：最小运行镜像 ====================
FROM openwrt/rootfs:latest

# OpenWrt 根文件系统已经带有 ubus 相关能力，保留必要的工作目录
WORKDIR /app
RUN mkdir -p /app/bin /app/config

# 从 builder 阶段把编译好的二进制文件偷过来
COPY --from=builder /build/ddns /app/bin/ddns

# 声明挂载点（容器内配置文件目录）
VOLUME ["/app/config"]

# 终极清爽启动命令：
# 显式指定容器内配置文件路径
ENTRYPOINT ["/app/bin/ddns", "-c", "/app/config/conf.yaml"]