# 关键修复 1：在最顶部、第一个 FROM 之前声明全局 ARG，让整个文件（包括所有 FROM）都能看见它
ARG OPENWRT_TAG=x86-64

# ==================== 阶段一：编译二进制 ====================
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


# ==================== 阶段三：软路由专用版底座 (支持外部传入 Tag) ====================
# 关键修复 2：在阶段内部重新继承一次全局的 OPENWRT_TAG 变量，这样 IDE 绝对不会报 Undefined 错误
ARG OPENWRT_TAG
FROM openwrt/rootfs:${OPENWRT_TAG} AS base-openwrt

# 复制证书
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/


# ==================== 阶段四：最终打包 ====================

# 4a. 通用版
FROM base-generic AS generic
WORKDIR /app
COPY --from=builder /build/ddns /app/bin/ddns
RUN chmod +x /app/bin/ddns
VOLUME ["/app/config"]
ENTRYPOINT ["/app/bin/ddns", "-c", "/app/config/conf.yaml"]

# 4b. 软路由专用版
FROM base-openwrt AS openwrt
WORKDIR /app
COPY --from=builder /build/ddns /app/bin/ddns
RUN chmod +x /app/bin/ddns
VOLUME ["/app/config"]
ENTRYPOINT ["/app/bin/ddns", "-c", "/app/config/conf.yaml"]

#构建通用版镜像
#docker buildx build   --platform linux/amd64,linux/arm64   --target generic   -t lgyong/ddns:latest   -t lgyong/ddns:alpine   --push .

#构建openwrt x86-64镜像
#docker buildx build   --platform linux/amd64   --target openwrt   --build-arg OPENWRT_TAG=x86-64   -t lgyong/ddns:openwrt-amd64   --push .

#构建openwrt arm64镜像
#docker buildx build   --platform linux/arm64   --target openwrt   --build-arg OPENWRT_TAG=aarch64_cortex-a53   -t lgyong/ddns:openwrt-arm64   --push .
