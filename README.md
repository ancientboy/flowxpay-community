# FlowXPay 社区版 · 面向多个国家的海外四方支付系统

**简体中文** · [English](README.en.md)

[在线演示](https://ancientboy.github.io/flowxpay-community/?lang=zh-CN) · [快速开始](#快速开始) · [一分钟沙盒演示](https://github.com/ancientboy/flowxpay-community/releases/tag/v0.1.0) · [API 文档](docs/merchant-api.md) · [上游适配](docs/channel-integrations.md) · [提交反馈](https://github.com/ancientboy/flowxpay-community/issues) · [Telegram 商务 @FlowXPay_nova](https://t.me/FlowXPay_nova)

FlowXPay 是一套面向多国家和地区的开源海外四方支付系统，可部署在自己的环境中。它用统一的商户 API 和运营后台管理代收、代付、通道路由、查单、退款和对账，方便跨境支付团队接入和管理自己的上游。

在线演示直接打开社区版的运营后台和商户后台，供免登录查看示例数据。自部署沙盒运行 Go API 和 PostgreSQL，支持后台操作和模拟交易。

## 社区版内置三个模拟市场

| 市场 | 币种 | 沙盒支付方式 |
| --- | --- | --- |
| 巴西 | BRL | Pix 二维码 |
| 美国 | USD | 银行卡模拟收银台 |
| 印度 | INR | 模拟支付网关 |

三个市场均使用模拟交易。**一分钟演示以巴西 Pix 为例**。本地启动后，可在创建代收订单时切换支付方式，体验美国和印度的模拟流程。

## 快速开始

### Docker Compose 一键启动

安装 Docker Engine 28 或更新版本及 Compose v2，再启动 Docker。克隆仓库后，只需一条命令构建并启动社区版：

```bash
git clone https://github.com/ancientboy/flowxpay-community.git
cd flowxpay-community
docker compose up -d --build --wait
```

等待启动完成后，打开 [http://127.0.0.1:4173/](http://127.0.0.1:4173/)。在终端运行下面的命令读取本次沙盒密钥，再选择「使用沙盒 API 密钥」登录。请在本机读取和使用密钥。

```bash
docker compose exec -T api cat /run/flowxpay/api/merchant-key
```

进入工作台后，新建一笔模拟代收订单，打开收银台，完成模拟支付后查单。网页端口只绑定本机回环地址。PostgreSQL 通过容器间的 Unix 套接字连接。

- **停用和恢复**：用 `docker compose down` 停止，订单和密钥保留在卷里。再次执行启动命令即可恢复。
- **换端口**：默认端口被占用时，可用 `FLOWXPAY_WEB_PORT=4180 docker compose up -d --build --wait`，再访问 `http://127.0.0.1:4180/`。
- **服务器上试用**：保持回环绑定，从自己的电脑用 SSH 转发访问，例如 `ssh -N -L 4173:127.0.0.1:4173 user@your-server`。
- **查看启动状态**：用 `docker compose ps -a` 和 `docker compose logs init db api web`。

### 启用运营后台和商户后台

安装 `htpasswd` 和 Python 3，在本机设置运营账号密码。下面的命令只把密码哈希交给 Compose。

```bash
operator_account=$(htpasswd -nBC 12 operator) &&
  export FLOWXPAY_LOCAL_PLATFORM_ACCOUNTS="$(printf '%s' "$operator_account" |
    python3 -c 'import json,sys; name, hashed = sys.stdin.read().split(":", 1); print(json.dumps({name: {"hash": hashed.strip(), "role": "operator"}}))')" &&
  docker compose up -d --wait
unset operator_account
```

打开 [本地运营后台](http://127.0.0.1:4173/ops?lang=zh-CN)，以 `operator` 登录。从「商户」开户并生成管理员邀请。打开本机邀请链接并设置密码，再从 [商户后台](http://127.0.0.1:4173/?lang=zh-CN) 登录。商户通道由运营后台分配。

### 本地开发启动

上方的沙盒演示视频采用下面的手动安装方式，适合在本机调试源码。

准备 Go（版本以 [go.mod](backend/go.mod) 为准）、Node.js 22、npm、Python 3、OpenSSL 和 PostgreSQL 命令行工具（包含 `pg_config`、`initdb` 和 `pg_ctl`）。在普通用户下运行：

```bash
npm --prefix web ci
./scripts/local-sandbox.sh run
```

1. 打开终端打印的本地地址，默认是 `http://127.0.0.1:4173/`。
2. 选择「使用沙盒 API 密钥」并粘贴终端提示文件中的本次密钥。也可选择「使用本地成员会话」体验商户后台。
3. 新建模拟订单，打开收银台，点「模拟付款」并查单。首次启动也会生成三笔演示订单。
4. 按 Ctrl+C 停止服务。每次启动使用独立的临时数据库和随机访问凭据。交互模式保留本次临时目录，服务停止后可按需要清理。

本地启动器使用回环地址和私有数据库套接字。请在本地开发机或受控测试环境运行，保持 PostgreSQL 和模拟后台不对公网开放。

## API 与通道

[中文 API 指南](docs/merchant-api.md) · [English API guide](docs/merchant-api.en.md) · [在线 API 文档](https://ancientboy.github.io/flowxpay-community/?workspace=merchant&page=api-docs&lang=zh-CN) · [OpenAPI](docs/openapi.yaml) · [上游适配指南](docs/channel-integrations.md)

真实通道使用客户自己的上游账号。各家上游的签名、回调与账单协议需要分别适配和验证。需要接入新上游、部署支持或其他合作，请[联系商务](https://t.me/FlowXPay_nova)。

## 验证与反馈

```bash
./scripts/local-sandbox.sh test
npm --prefix web run typecheck
npm --prefix web run build
```

用 [GitHub Issues](https://github.com/ancientboy/flowxpay-community/issues) 提交安装问题、模拟交易问题或通道需求。附上版本、运行环境和可复现步骤，请删除密钥、真实账户、订单、支付凭证及客户资料。安全报告请走 [SECURITY.md](SECURITY.md) 的私下渠道。

## 许可证

FlowXPay 社区版采用 [Apache License 2.0](LICENSE)。
