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

- DNS 服务商：aliyun（阿里云）、baidu（百度云）、dnsla（DNSLA）、tencent（腾讯云）、huawei（华为云）、volcengine（火山引擎）
- IP 获取方式：
  - `cmd`：执行系统命令
  - `nic`：读取本机网卡 IP
  - `url`：通过 HTTP 请求获取公网 IP
  - `duid`：适用于 OpenWrt 设备


## 快速开始

### 1. GitHub 下载使用

从 [GitHub Releases](https://github.com/lgyong511/ddns/releases) 下载对应系统和架构的已编译版本，解压后进入程序目录，并准备配置文件：

```bash
./ddns -c config/config.yaml
```

不指定 `-c` 时，程序使用可执行文件同目录下的 `config/config.yaml`：

```bash
./ddns
```

首次运行且默认配置不存在时，程序会创建目录和最小配置文件。如果程序目录存在有效的旧 `conf.yaml`，会自动迁移到新路径并保留旧文件。如果使用 `go run`，可执行文件通常位于临时目录，建议使用 `-c` 显式指定配置文件路径。

使用 `-web` 参数启动 Web 控制台，`-p` 参数指定端口：

```bash
./ddns -web -c config/config.yaml -p 8686
```

启动后访问 [http://127.0.0.1:8686](http://127.0.0.1:8686)。首次启动需要先设置登录账号和密码。Web 与普通命令行模式始终使用同一个配置文件，不会另行创建用户目录配置。首次设置页可导入已有 YAML 配置；登录后可在页面顶部菜单导入或导出配置。更多配置说明请参阅下方的 [Web 控制台](#web-控制台)章节。


### 3. Docker 运行

项目当前提供两种镜像构建目标：

- `generic`：轻量通用版，基于 Alpine，适合普通场景，多架构镜像
- `openwrt`：面向软路由 OpenWrt 场景的镜像，适合挂载 `ubus` socket，只有amd64（x86-64）

Docker 镜像固定读取 `/etc/ddns/config.yaml`。建议将宿主机配置文件映射到该路径，并保持可写，以便 Web 控制台保存配置。宿主机文件必须在启动容器前创建，否则 Docker 可能将不存在的源路径创建成目录：

```bash
mkdir -p config
printf 'providers: []\nwebhook:\n  headers: []\n' > config/config.yaml
```

后续示例中的 `$(pwd)/config/config.yaml` 可以替换为宿主机上的任意绝对路径，例如 `/etc/ddns/config.yaml`。

#### 运行通用版

**说明：若不使用网卡获取IP地址，请去除 `--net=host`**

镜像默认启动 Web 控制台，监听 `8686` 端口。使用 `--net=host` 时可直接访问 [http://127.0.0.1:8686](http://127.0.0.1:8686)。

```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  ghcr.io/lgyong511/ddns:latest
```

不使用 host 网络时，通过端口映射访问 Web 控制台：

```bash
docker run -d --name ddns --restart always \
  -p 8686:8686 \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  ghcr.io/lgyong511/ddns:latest
```

访问 [http://127.0.0.1:8686](http://127.0.0.1:8686)。

#### 运行 OpenWrt 版

**说明：如果要使用 DUID 获取 IPv6 地址需要挂载 ubus，不需要时可不挂载**

镜像默认启动 Web 控制台，监听 `8686` 端口。使用 `--net=host` 时可直接访问 [http://127.0.0.1:8686](http://127.0.0.1:8686)。

```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  -v /var/run/ubus/ubus.sock:/var/run/ubus/ubus.sock \
  ghcr.io/lgyong511/ddns:latest-openwrt
```

不使用 host 网络时，通过端口映射访问 Web 控制台：

```bash
docker run -d --name ddns --restart always \
  -p 8686:8686 \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  -v /var/run/ubus/ubus.sock:/var/run/ubus/ubus.sock \
  ghcr.io/lgyong511/ddns:latest-openwrt
```

访问 [http://127.0.0.1:8686](http://127.0.0.1:8686)。

### 4. 源码编译

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

在 Git tag 上执行 `make build` 时，版本号会自动使用当前 tag；非 tag 构建默认使用 `dev`。也可以通过 `VERSION` 手动指定版本：

```bash
VERSION=v1.6.0 make build
```

### 5. 准备配置文件

在程序目录的 `config/config.yaml` 创建或修改配置。源码开发时也可以将示例复制到 `config/config.yaml`，示例：

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
### 6. 启动程序

```bash
./ddns
```

程序启动时会在日志中输出当前版本。也可以使用 `-version` 只查看版本号并退出：

```bash
./ddns -version
```

输出示例：

```text
v1.6.0
```

如果配置文件不在默认路径，可以通过参数指定：

```bash
./ddns -c /path/to/config.yaml
```

`-c` 指定的相对路径按当前工作目录解析；显式指定的配置文件不存在时程序会直接报错。只有默认路径会自动初始化。

### 7. 使用 Makefile 运行

```bash
make run
```

## Web 控制台

程序支持通过 Web 控制台管理 DDNS 配置。控制台可以创建、编辑和删除 DNS 服务商及解析记录，配置 Webhook，查看运行日志，以及修改登录密码。登录后的页面顶部菜单栏会显示当前程序版本。

### 启动 Web 控制台

源码编译后使用 `-web` 参数启动：

```bash
./ddns -web
```

默认监听 `:8686`，启动后访问 [http://127.0.0.1:8686](http://127.0.0.1:8686)。可以通过 `-p` 修改端口：

```bash
./ddns -web -p 9090
```

首次启动时，如果配置文件中还没有 Web 账号，访问地址会自动跳转到首次设置页面。设置用户名和密码后，使用该账号登录控制台。

### 配置文件说明

Web 模式与普通命令行模式共享启动时解析出的 `ConfigPath`：

- 指定 `-c` 时使用该文件；
- 不指定 `-c` 时使用可执行文件同目录的 `config/config.yaml`；
- Web 保存配置会使用同目录临时文件、同步后原子替换该文件；
- 配置管理会尽量保留原 YAML 的注释、未知字段和未修改字段格式；
- 文件被手工修改后，Web 保存会检测到冲突并拒绝覆盖，重新加载后再保存即可。
- 使用文本编辑器修改配置且校验通过后，程序会自动热加载，并通过 SSE 通知已登录的 Web 页面；
- 配置首页会自动刷新，正在编辑 Provider、Record、Webhook、导入导出或密码表单的页面只显示刷新提示，不会丢弃未提交内容；
- 外部修改 `auth.username` 或 `auth.passwordHash` 后，全部 Web 会话会立即失效，并要求使用最新账号密码重新登录；无效 YAML 不会刷新页面，错误会写入日志。

例如，使用已有配置启动 Web 控制台：

```bash
./ddns -web -c /path/to/config.yaml
```

### 通过页面导入和导出

首次设置页可选择本地 `.yaml` 或 `.yml` 文件导入已有配置；登录后，页面顶部菜单也提供导入和导出入口。

- 导入会覆盖当前所有 DNS 服务商和 Webhook 设置，保存过程不生成备份文件；导入页面会显示覆盖警示；
- 导入默认不处理 Web 登录账号，始终保留当前控制台账号；勾选“同时导入 Web 账号和密码配置”后，才会导入 `auth.username` 和 bcrypt `auth.passwordHash`，不包含明文密码。账号配置缺失或哈希无效时，整个导入会被拒绝；
- 勾选导入账号配置并成功保存后，所有 Web 会话会失效，需要使用导入账号重新登录；首次设置阶段勾选后直接进入登录页，未勾选时仍需创建账号；
- 导出默认仅包含 DNS 服务商和 Webhook。导出页面可选择是否包含 `auth`；勾选后文件包含用户名和 bcrypt 密码哈希，文件名带有 `with-auth`；
- 导出文件包含服务商密钥和可能含敏感信息的 Webhook 内容，请妥善保存，不要提交到公开仓库；
- 单个导入请求体最大 1 MiB，导入成功后会自动热加载配置。

### Docker 使用 Web 控制台

Docker 镜像默认启动 Web 控制台，监听 `8686` 端口，无需覆盖启动命令：

- 使用 `--net=host` 时无需端口映射；
- 不使用 `--net=host` 时必须添加 `-p 8686:8686`，也可以将宿主机端口改为其他值，例如 `-p 9090:8686`；
- 如果使用网卡方式获取宿主机 IP，仍建议使用 `--net=host`；
- 镜像内程序位于 `/usr/local/bin/ddns`，启动命令固定使用 `-c /etc/ddns/config.yaml -web`，默认端口为 `8686`。

```bash
docker run -d --name ddns --restart always \
  --net=host \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  ghcr.io/lgyong511/ddns:latest
```

不使用 host 网络时：

```bash
docker run -d --name ddns --restart always \
  -p 8686:8686 \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  ghcr.io/lgyong511/ddns:latest
```

Docker 镜像默认使用 `/etc/ddns/config.yaml`。镜像内包含最小配置；映射宿主机文件后，Web 修改会直接保存到宿主机的 `./config/config.yaml`。不要添加 `:ro`，否则 Web 控制台无法保存配置：

```bash
mkdir -p config
printf 'providers: []\nwebhook:\n  headers: []\n' > config/config.yaml
docker run -d --name ddns --restart always \
  -p 8686:8686 \
  -v "$(pwd)/config/config.yaml:/etc/ddns/config.yaml:rw" \
  ghcr.io/lgyong511/ddns:latest
```

仓库中的 `docker-compose.yml` 使用相同的文件映射。准备好 `./config/config.yaml` 后可直接启动：

```bash
docker compose up -d
```

Web 控制台默认监听所有网卡。部署在公网或局域网环境时，请通过防火墙、反向代理或其他访问控制措施限制访问，并妥善保管登录密码和 DNS 服务商密钥。

### 字符串长度限制

长度按 UTF-8 字节数计算，Web 页面会同步限制输入长度，服务端也会再次校验：

- 服务商名称、记录名称：最多 64 字节；Access Key ID、Secret：最多 256 字节；
- URL：最多 2048 字节；系统命令：最多 4096 字节；网卡名称：最多 256 字节；DUID：最多 128 字节；筛选规则：最多 512 字节；
- 域名：单个标签最多 63 字节，完整域名最多 253 字节；中文域名按转换后的 ASCII（Punycode）长度计算；
- Webhook URL：最多 2048 字节；请求体：最多 64 KiB；单个请求头：最多 1024 字节，所有请求头合计最多 8 KiB；
- Web 登录账号最多 64 字节，密码最多 72 字节；单个 POST 请求体最多 1 MiB。

## 配置说明

### providers

- `name`：必选，当前 Provider 的名称
- `provider`：必选，DNS 服务商类型， `aliyun`、`baidu`、`dnsla`、`tencent`、`huawei`、`volcengine`
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
- 若未显式指定配置文件，程序使用可执行文件同目录下的 `config/config.yaml`
- 请妥善保管 `key` 与 `Secret`
