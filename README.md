# DDNS

DDNS 是一个基于 Go 语言实现的轻量级动态域名解析同步工具。它会定时获取当前公网 IP，并自动更新 DNS 服务商中的解析记录，适合家庭网络、NAS、软路由、云服务器等场景。

## 功能特点

- 支持定时检测当前公网 IP
- 支持将 IP 自动同步到 DNS 解析记录
- 支持多种获取 IP 的方式：命令行、网卡、URL、DUID
- 支持 IPv4 / IPv6
- 支持热加载配置文件变化
- 提供 Docker 部署方式

## 当前支持

- DNS 服务商：Aliyun（阿里云）
- IP 获取方式：
  - `cmd`：执行系统命令
  - `nic`：读取本机网卡 IP
  - `url`：通过 HTTP 请求获取公网 IP
  - `duid`：适用于 OpenWrt / Linux 设备

## 项目结构

- `cmd/ddns`：程序入口
- `pkg/config`：配置加载与校验
- `pkg/engine`：主执行引擎
- `pkg/addr`：IP 获取与过滤
- `pkg/provider`：DNS 服务商接口与实现

## 快速开始

### 1. 下载并编译

```bash
https://github.com/lgyong511/ddns.git
cd ddns
go build -o ddns ./cmd/ddns
```

也可以直接使用 Makefile：

```bash
make build
```

### 2. 准备配置文件

在项目根目录创建或修改 `conf.yaml`，示例：

```yaml
providers:
  - name: aliyun-example
    provider: aliyun
    keyId: YOUR_ACCESS_KEY_ID
    keySecret: YOUR_ACCESS_KEY_SECRET
    forceInterval: 5
    records:
      - name: ipv4-record
        subDomains:
          - www.example.com
        ipVersion: 4
        ttl: 600
        getType: url
        getValue: https://myip.ipip.net,https://ipw.cn
        interval: 30
        rule: ""
```
```yaml
# OpenWrt配置
providers:
  - name: Myz-NAS-Aliyun
    provider: aliyun
    keyId: YOUR_ACCESS_KEY_ID
    keySecret: YOUR_ACCESS_KEY_SECRET
    forceInterval: 5
    records:
      - name: Nas_cmd_6
        subDomains:
          - myz.lgyong.cc
        ipVersion: 6
        ttl: 600
        getType: cmd
        getValue: ip addr show br-lan
        interval: 30
        rule: "splice@1@9209:d0ff:fe09:781d"
      - name: Home_nic_6
        subDomains:
          - home.lgyong.cc
        ipVersion: 6
        ttl: 600
        getType: nic
        getValue: br-lan
        interval: 30
        rule: "2408"
      - name: Nas_duid_6
        subDomains:
          - test1.lgyong.cc
          - test2.lgyong.cc
        ipVersion: 6
        ttl: 600
        getType: duid
        getValue: 000300019009d009781d
        interval: 30
        rule: ""
      - name: test_ipv4
        subDomains:
          - test1.lgyong.cc
          - test2.lgyong.cc
        ipVersion: 4
        ttl: 600
        getType: url
        getValue: https://myip.ipip.net, https://ddns.oray.com/checkip, https://ip.3322.net, https://4.ipw.cn, https://v4.yinghualuo.cn/bejson
        interval: 30
        rule: ""

```
### 3. 启动程序

```bash
./ddns
```

如果配置文件不在默认路径，可以通过参数指定：

```bash
./ddns -c /path/to/conf.yaml
```

### 4. 使用 Makefile 运行

```bash
make run
```

## Docker 运行

项目当前提供两种镜像构建目标：

- `generic`：轻量通用版，基于 Alpine，适合普通场景，多架构镜像
- `openwrt`：面向软路由 OpenWrt 场景的镜像，适合挂载 `ubus` socket，只有amd64（x86-64）

### 1. 构建通用版镜像

```bash
docker buildx build --platform linux/amd64 --target generic -t lgyong/ddns:latest -t lgyong/ddns:alpine --push .
```

### 2. 构建 OpenWrt 版镜像

```bash
docker buildx build --platform linux/amd64 --target openwrt  -t lgyong/ddns:openwrt-amd64 --push .
```

### 3. 运行容器

通用版：
:red_circle: **说明：若不使用网卡获取IP地址，请去除 --net=host**
```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v /app/:/app/config/ \
  ghcr.io/lgyong511/ddns:latest
```

OpenWrt 版 ：
:red_circle: **说明：如果要使用DUID获取IPv6地址需要挂载ubus，若不需要可不挂载**
```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v /app/:/app/config/ \
  -v /var/run/ubus/ubus.sock:/var/run/ubus/ubus.sock \
  ghcr.io/lgyong511/ddns:latest-openwrt
```

### 4. GitHub Actions 自动发布

推送标签后，工作流会自动：
- 构建 Go 二进制并上传到 GitHub Release
- 推送 `generic` 和 `openwrt` 两种 Docker 镜像到 GHCR

## 配置说明

### providers

- `name`：当前 Provider 的名称
- `provider`：DNS 服务商类型， `aliyun`、`tencent`
- `keyId`：AccessKey ID
- `keySecret`：AccessKey Secret
- `forceInterval`：强制同步的时间间隔，单位分钟
- `records`：要同步的解析记录列表

### records

- `name`：记录组名称
- `subDomains`：要更新的子域名列表
- `ipVersion`：`4` 表示 IPv4，`6` 表示 IPv6
- `ttl`：DNS 记录生存时间，单位秒
- `getType`：IP 获取方式
- `getValue`：对应获取方式的参数
- `interval`：检测周期，单位秒
- `rule`：IP 过滤规则

## 示例：不同获取方式

### 命令行方式

```yaml
records:
  - name: ipv6-cmd
    subDomains:
      - home.example.com
    ipVersion: 6
    ttl: 600
    getType: cmd
    getValue: ip addr show br-lan
    interval: 30
    rule: ""
```

### URL 方式

```yaml
records:
  - name: ipv4-url
    subDomains:
      - home.example.com
    ipVersion: 4
    ttl: 600
    getType: url
    getValue: https://myip.ipip.net,https://ip.cn
    interval: 30
    rule: ""
```

### NIC 方式

适用于从本机网卡中读取 IP 地址。

```yaml
records:
  - name: ipv4-nic
    subDomains:
      - home.example.com
    ipVersion: 4
    ttl: 600
    getType: nic
    getValue: eth0
    interval: 30
    rule: ""
```

### DUID 方式

适用于 OpenWrt / Linux 设备，通常通过 DUID 相关命令获取 IP。

```yaml
records:
  - name: ipv6-duid
    subDomains:
      - home.example.com
    ipVersion: 6
    ttl: 600
    getType: duid
    getValue: "000300019009d009781d" 
    interval: 30
    rule: ""
```

## rule说明
- 1，空值选择第一个IP地址
- 2，index@n, 选择第n个IP地址，n从1开始计数，超出范围选择第一个IP地址
- 3，splice@n@后缀，选择第n个IP地址的前64位拼接后缀，后缀可以是8字节的数组、切片，或者标准的IPv6后缀字符串（如 "::1"、“::9009:d09f:fd09:751d“ 或 "0:0:0:1"）
- 4，contain@substr，选择包含substr的第一个IP地址

## 注意事项

- 配置文件修改后会自动触发热加载
- 若未显式指定配置文件，程序会优先使用可执行文件同目录下的 `conf.yaml`
- 请妥善保管 `keyId` 与 `keySecret`
- 某些获取方式依赖当前系统环境，实际效果与系统命令输出有关

## GitHub 发布

可以通过 GitHub Actions 实现自动打包发布。

### 发布步骤

```bash
git add .
git commit -m "release: v1.0.0"
git tag v1.0.0
git push origin main --tags
```

推送标签后，仓库会自动触发工作流，生成以下平台的二进制文件并上传到 GitHub Release：

- linux/amd64
- linux/arm64
- darwin/amd64
- darwin/arm64
- windows/amd64
- openwrt/amd64

## 许可证

本项目为示例性代码仓库，使用时请根据实际需求自行评估合规性与安全性。

