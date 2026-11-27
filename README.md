# FlowXPay 社区版 · 海外四方支付系统

[快速开始](#快速开始) · [API 文档](docs/merchant-api.md) · [上游适配](docs/channel-integrations.md) · [提交反馈](https://github.com/ancientboy/flowxpay-community/issues) · [Telegram 商务 @FlowXPay_nova](https://t.me/FlowXPay_nova)

FlowXPay 是一套开源、可自部署的海外四方支付系统，面向跨境支付服务商、聚合支付团队和商户技术团队。平台统一管理商户、API、通道路由、收银台、查单、退款和对账。

社区版内置模拟代收和代付工作流，可在本地验证订单流程。全部交易和余额使用模拟数据。接入真实上游时，客户使用自己的上游账号，由我们按需协助适配和联调。

**FlowXPay Community Edition** is an open-source, self-hosted cross-border payment orchestration platform. Its sandbox lets teams test merchant onboarding, payment APIs, checkout, webhooks, and reconciliation with simulated transactions. For help integrating your own payment provider, contact [@FlowXPay_nova on Telegram](https://t.me/FlowXPay_nova).

## 快速开始

准备 Go（版本以 [go.mod](backend/go.mod) 为准）、Node.js 22、npm、Python 3、OpenSSL 和 PostgreSQL 命令行工具（包含 `pg_config`、`initdb` 和 `pg_ctl`）。在普通用户下运行：

```bash
git clone https://github.com/ancientboy/flowxpay-community.git
cd flowxpay-community
npm --prefix web ci
./scripts/local-sandbox.sh run
```

1. 打开终端打印的本地地址，默认是 `http://127.0.0.1:4173/`。
2. 选择「使用沙盒 API 密钥」并粘贴终端提示文件中的本次密钥。也可选择「使用本地成员会话」体验商户后台。
3. 新建模拟订单，打开收银台，点「模拟付款」并查单。首次启动也会生成三笔演示订单。
4. 按 Ctrl+C 停止服务。每次启动使用独立的临时数据库和随机访问凭据。交互模式保留本次临时目录，服务停止后可按需要清理。

本地启动器使用回环地址和私有数据库套接字。请在本地开发机或受控测试环境运行，保持 PostgreSQL 和模拟后台不对公网开放。

## API 与通道

[商户 API 指南](docs/merchant-api.md) · [OpenAPI](docs/openapi.yaml) · [上游适配指南](docs/channel-integrations.md)

当前内置模拟 USD 卡支付、BRL Pix 和 INR 网关。客户对接真实通道时使用自己的上游账号。各上游的签名、回调与账单格式需要分别验证。需要接入新上游、部署支持或其他合作，请[联系商务](https://t.me/FlowXPay_nova)。

## 验证与反馈

```bash
./scripts/local-sandbox.sh test
npm --prefix web run typecheck
npm --prefix web run build
```

用 [GitHub Issues](https://github.com/ancientboy/flowxpay-community/issues) 提交安装问题、模拟交易问题或通道需求。附上版本、运行环境和可复现步骤，请删除密钥、真实账户、订单、支付凭证及客户资料。安全报告请走 [SECURITY.md](SECURITY.md) 的私下渠道。

## 许可证

FlowXPay 社区版采用 [Apache License 2.0](LICENSE)。
