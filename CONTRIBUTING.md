# 贡献说明

请先用 Issue 说明具体问题或场景。涉及手机自动动作的修改，需要说明输入、前台页面、拟执行动作与失败后的处理方式。PR 应保留设备租约、执行前重新观察、动作账本、结果不确定时暂停以及人工最终核验等约束。

本地环境和配置见 [部署指南](docs/deployment.md)。提交前运行：

```sh
make test
make vet
make check-public
cd frontend
npm ci
npm test
npm run build
```

数据库集成需要专用 PostgreSQL 和 `TEST_DATABASE_URL`，详见 [验证范围](docs/verification.md)。没有真机验证时请明确标注模拟范围，不要把测试通过写成真实发布或发送成功。

测试尽量使用合成 PNG、虚构账号和模拟 Portal / 模型。不要提交运行数据、截图、密钥、设备 serial、构建产物或个人电脑路径；不要修改生成的 `internal/store/db` 而遗漏查询源与 sqlc 配置。
