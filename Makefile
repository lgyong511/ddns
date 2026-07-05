.PHONY: all build run clean docker-build docker-run

# 变量定义
BINARY_NAME=ddns
MAIN_PATH=./cmd/ddns
DOCKER_IMAGE_NAME=my-ddns-service:latest

all: build

## build: 本地编译当前架构的二进制
build:
	@echo "正在本地编译 $(BINARY_NAME)..."
	go build -ldflags="-s -w" -o $(BINARY_NAME) $(MAIN_PATH)
	@echo "编译完成！"

## run: 本地直接运行（默认读取根目录下的 conf.yaml）
run: build
	@echo "正在启动 $(BINARY_NAME)..."
	./$(BINARY_NAME)

## clean: 清理编译出的产物
clean:
	@echo "清理编译产物..."
	@rm -f $(BINARY_NAME)
	@echo "清理完毕。"

## docker-build: 一键构建 Docker 镜像
docker-build:
	@echo "正在构建 Docker 镜像 $(DOCKER_IMAGE_NAME)..."
	docker build -t $(DOCKER_IMAGE_NAME) .
	@echo "镜像构建成功！"

## docker-run: 快速在容器中测试运行
docker-run:
	@echo "正在启动容器..."
	docker run --rm -it \
		-v $(PWD)/conf.yaml:/app/conf.yaml \
		$(DOCKER_IMAGE_NAME)