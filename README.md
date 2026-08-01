# DDNS

DDNS 是一个基于 Go 语言实现的轻量级动态域名解析同步工具。它会定时获取当前公网 IP，并自动更新 DNS 服务商中的解析记录，支持多少方式获取IP地址。适合家庭网络、NAS、软路由等场景。

## 功能特点

- 支持定时检测当前公网 IP
- 支持将 IP 自动同步到 DNS 解析记录
- 支持多种获取 IP 的方式：命令行、网卡、URL、DUID
- 支持 IPv4 / IPv6
- 支持热加载配置文件变化
- 提供 Docker 部署方式
- 各 DNS 服务商客户端内置 API 限流设计
- 支持失败重试与强制同步策略
- 支持异步 Webhook 通知

## 当前支持

- DNS 服务商：aliyun（阿里云）、baidu（百度云）、tencent（腾讯云）、huawei（华为云）
- IP 获取方式：
  - `cmd`：执行系统命令
  - `nic`：读取本机网卡 IP
  - `url`：通过 HTTP 请求获取公网 IP
  - `duid`：适用于 OpenWrt 设备


## 快速开始

### 1. GitHub 下载使用

从 [GitHub Releases](https://github.com/lgyong511/ddns/releases) 下载对应系统和架构的已编译版本，解压后进入程序目录，并准备配置文件：

```bash
./ddns -c conf.yaml
```

如果配置文件名为 `conf.yaml` 且位于程序同目录，也可以直接运行：

```bash
./ddns
```

### 2. Docker 运行

项目当前提供两种镜像构建目标：

- `generic`：轻量通用版，基于 Alpine，适合普通场景，多架构镜像
- `openwrt`：面向软路由 OpenWrt 场景的镜像，适合挂载 `ubus` socket，只有amd64（x86-64）

#### 运行通用版

**说明：若不使用网卡获取IP地址，请去除 `--net=host`**

```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v /app/:/app/config/ \
  ghcr.io/lgyong511/ddns:latest
```

#### 运行 OpenWrt 版

**说明：如果要使用 DUID 获取 IPv6 地址需要挂载 ubus，不需要时可不挂载**

```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v /app/:/app/config/ \
  -v /var/run/ubus/ubus.sock:/var/run/ubus/ubus.sock \
  ghcr.io/lgyong511/ddns:latest-openwrt
```

### 3. 源码编译

如果需要自行编译，可以从 GitHub 克隆源码：

```bash
git clone https://github.com/lgyong511/ddns.git
cd ddns
go build -o ddns ./cmd/ddns
```

也可以直接使用 Makefile：

```bash
make build
```

### 4. 准备配置文件

在项目根目录创建或修改 `conf.yaml`，示例：

```yaml
#通用配置
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
# OpenWrt 使用duid配置
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
  - name: Myz-NAS-tencent
    provider: tencent
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
### 5. 启动程序

```bash
./ddns
```

如果配置文件不在默认路径，可以通过参数指定：

```bash
./ddns -c /path/to/conf.yaml
```

### 6. 使用 Makefile 运行

```bash
make run
```

## 配置说明

### providers

- `name`：必选，当前 Provider 的名称
- `provider`：必选，DNS 服务商类型， `aliyun`、`baidu`、`tencent`、`huawei`
- `keyId`：必选，API访问KEY
- `keySecret`：必选，API访问Secret
- `forceInterval`：可选，强制同步的时间间隔，单位分钟，默认15分钟，可配置范围5-30分钟
- `records`：必选，要同步的解析记录列表

### records

- `name`：必选，记录组名称
- `subDomains`：必选，要更新的子域名列表
- `ipVersion`：必选，`4` 表示 IPv4，`6` 表示 IPv6
- `ttl`：可选，DNS 记录生存时间，单位秒，默认600秒，可配置范围1-86400秒，警告：请确定服务商支持小的生效时间
- `getType`：必选，IP 获取方式，cmd、url、nic、duid
- `getValue`：必选，对应获取方式的参数
- `interval`：可选，检测周期，单位秒，默认30秒，可配置范围10-60秒
- `rule`：可选，IP 过滤规则，可配置范围：[跳转到rule说明](#rule说明)

### webhook

`webhook` 用于在 DNS 记录创建、更新或同步失败时发送通知。未配置 `url` 时不会发送通知。

- `url`：Webhook 请求地址，必填
- `body`：POST 请求体。为空时使用 GET 请求，并将模板变量进行 URL 编码；不为空时使用 POST 请求，模板变量直接替换
- `headers`：请求头列表，每项使用 `Header-Name: Header-Value` 格式

支持以下模板变量：

- `{{Domain}}`：发生变化的域名
- `{{OldAddr}}`：旧 IP 地址；创建记录时通常为空
- `{{NewAddr}}`：新 IP 地址
- `{{Provider}}`：DNS 服务商名称
- `{{State}}`：操作状态，可能为 `创建记录成功`、`更新记录成功` 或以 `同步失败:` 开头的错误信息
- `{{Date}}`：通知时间，格式为 `YYYY-MM-DD HH:mm:ss`

#### GET 请求示例

当 `body` 为空时，程序会将模板变量替换到 URL 中并发送 GET 请求：
**Server酱示例：**
```yaml
webhook:
  url: "https://sctapi.ftqq.com/[YOU_SENDKEY].send?title=公网IP变了&desp=域名：{{Domain}}，旧地址：{{OldAddr}} ，新地址：{{NewAddr}} ，服务商：{{Provider}} ，状态：{{State}}"
  body: ""
  headers:
    - ""
```

#### POST 请求示例

填写 `body` 后会发送 POST 请求，适合接收 JSON 格式通知：
**企业微信示例：**
```yaml
webhook:
  url: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=[YOU_KEY]
  body: '{"msgtype":"text","text":{"content":"【DDNS】域名 {{Domain}} 解析记录 {{State}}！\n- 服务商：{{Provider}}\n- 旧地址：{{OldAddr}}\n- 新地址：{{NewAddr}}\n- 时间：{{Date}}"}}'
  headers:
    - "Content-Type: application/json"
```

Webhook 发送失败只记录日志，不会阻塞 DNS 轮询。

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

适用于 OpenWrt 设备。

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
- 请妥善保管 `key` 与 `Secret`
