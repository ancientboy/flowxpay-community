# 参与 FlowXPay

欢迎提交可复现的安装问题、沙盒行为差异和通道需求。新功能先开 Issue 说明使用场景。提交代码前请运行后端集成测试、前端类型检查与构建：

```bash
./scripts/local-sandbox.sh test
npm --prefix web run typecheck
npm --prefix web run build
```

为支付流程变更使用合成订单和模拟账户。请勿上传访问凭据、上游私有文档、真实交易和客户资料。安全问题请参照 [安全报告](SECURITY.md)。
