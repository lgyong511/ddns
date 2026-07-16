# ==================== 阶段一：编译 Go 二进制 ====================
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine3.24 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o ddns ./cmd/ddns


# ==================== 阶段二：Alpine 通用版底座 ====================
FROM alpine:3.24 AS base-generic
RUN apk add --no-cache ca-certificates tzdata


# ==================== 阶段三：软路由专用版底座 ====================
FROM openwrt/rootfs:x86-64 AS base-openwrt

# 🚀 优化：既然自带 apk，直接一次性完整安装 inotifywait、证书和时区数据，更加工业化
RUN apk add --no-cache inotifywait ca-certificates zoneinfo-all


# ==================== 阶段四：最终输出目标 ====================

# 4a. 最终打包：通用版（支持多架构 amd64, arm64）
FROM base-generic AS generic
WORKDIR /app
COPY --from=builder /build/ddns /app/bin/ddns
RUN chmod +x /app/bin/ddns
VOLUME ["/app/config"]
ENTRYPOINT ["/app/bin/ddns", "-c", "/app/config/conf.yaml"]

# 4b. 最终打包：软路由专用版（由于 Actions 矩阵已改，实际仅构建 amd64）
FROM base-openwrt AS openwrt
WORKDIR /app
COPY --from=builder /build/ddns /app/bin/ddns
RUN chmod +x /app/bin/ddns
VOLUME ["/app/config"]
ENTRYPOINT ["/app/bin/ddns", "-c", "/app/config/conf.yaml"]


# ==============================================================================
# 本地手动构建备忘命令（清理了已作废的 ARM 软路由构建命令）
# ==============================================================================
# 构建通用版镜像（双架构）：
# docker buildx build --platform linux/amd64 --target generic -t lgyong/ddns:latest -t lgyong/ddns:alpine --push .
#
# 构建 OpenWrt x86-64 软路由镜像：
# docker buildx build --platform linux/amd64 --target openwrt  -t lgyong/ddns:openwrt-amd64 --push .