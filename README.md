# Mobile Autorun

用自然语言操作 Android 手机的自托管工作台。通过手机截图和无障碍界面信息，让模型选择动作，由服务端校验并交给 [Mobilerun Portal](https://github.com/droidrun/mobilerun-portal) 执行。

服务端使用 Go、Gin、Eino ADK、PostgreSQL 和 River；前端使用 Vue 3、TypeScript 和 Element Plus。模型支持 OpenAI 兼容 API，以及运行在自己电脑上的 Codex CLI / WorkBuddy CLI 连接器。

## 能做什么

- **手机操作**：截图、点击、滑动、输入、返回、启动应用；支持 USB 隧道和直接 WS / WSS 连接。
- **任务管理**：排队、定时、接管、取消、追加决策预算、任务归档；查看完整输入和带截图的动作回放，导出独立 HTML。
- **素材与附件**：上传图片和文件、传到手机、校验内容；每个任务最多 9 个附件，单个最大 16 MiB。
- **业务模板**：小红书图文编辑 / 草稿、微信客服、小红书获客运维，以及商品图文与 MP4 素材工作流。
- **执行保护**：设备租约、执行前重新观察、页面变化时丢弃旧决策、重复动作检测、动作账本和人工最终核验。结果不确定的动作不会自动重发。

项目目前适合本地或个人自托管实验，采用单服务实例。历史真机验收覆盖 Android 基础控制、Codex CLI 设置搜索、单图小红书草稿及局域网 WS 连接；这些流程曾需要暂停和人工处理。公开发布、微信发送、新业务模板、WSS 和不同机型 / 模型的完整闭环仍需分别验收，见 [验证范围](docs/verification.md)。

## 快速启动：Docker Compose

需要 Docker Engine / Desktop 与 Compose v2。

```sh
cp .env.example .env
# 编辑 .env，替换 POSTGRES_PASSWORD 和 ADMIN_PASSWORD 为两个不同的随机密码
# 建议使用 openssl rand -hex 24 生成密码，避免数据库 URL 的特殊字符转义问题
chmod 600 .env
docker compose up -d --build
curl --fail http://127.0.0.1:8080/healthz
```

打开 <http://localhost:8080>，使用 `.env` 中的管理员账号登录。数据库迁移随服务启动自动执行，管理员首次启动时创建；修改 `.env` 不会重置已有账号密码。数据保存在两个 Docker named volume 中。

Compose 默认仅允许本机访问。局域网连接、远程 WSS、源码开发、备份及升级见 [部署指南](docs/deployment.md)。

## 连接手机与模型

1. 在 Android 手机上安装 Portal，授权无障碍、截图及所需文件权限，在工作台「设备与连接」生成手机配对信息。
2. 按 [手机接入指南](docs/device-setup.md) 配置 USB 或 WS / WSS，然后在工作台确认手机在线且能获取截图。
3. 在「模型配置」选择直接 API，或按 [模型连接器指南](docs/model-connectors.md) 连接自己电脑上的 CLI。
4. 先创建“打开设置并搜索蓝牙，不更改开关”的基础任务，再测试业务模板。完成候选需要人工核验后才会标记成功。

CLI 连接器在 CLI 所在电脑运行；服务端 Docker 镜像不包含 CLI 或 ADB。支持原生 `media/index` 的 Portal 扩展 APK 不在本仓库内，普通 Portal 的图库同步可使用 USB MediaConnector，详见手机指南。

## 源码开发与测试

需要 Go 1.24+、Node.js 24、npm，以及 PostgreSQL 17。完整步骤见 [源码启动](docs/deployment.md#源码启动)。

```sh
make build
make test
make vet
cd frontend
npm ci
npm test
npm run build
```

Go 测试未设置 `TEST_DATABASE_URL` 时会跳过数据库集成测试。设置该变量时，账号必须能创建数据库；测试创建并删除独立的 `mobile_test_*` 数据库。请使用专用测试 PostgreSQL，见 [测试说明](docs/verification.md)。

## 项目结构

```text
cmd/          server、模型连接器、模型探测、USB 接入与图库连接器
internal/     Agent、设备协议、业务校验、HTTP API、数据库与素材生成
frontend/     Vue 工作台和前端测试
configs/      不含凭据的连接器配置示例
scripts/      发布前敏感信息扫描与干净源码导出
docs/        部署、设备、模型、业务、架构与验证说明
```

## 文档

- [部署指南](docs/deployment.md) · [手机接入](docs/device-setup.md) · [模型连接器](docs/model-connectors.md)
- [架构与执行边界](docs/architecture.md) · [验证范围](docs/verification.md)
- [小红书适配](docs/xhs-adaptation.md) · [业务工作流与媒体生成协议](docs/business-workflows.md) · [任务归档与输入](docs/task-archive-and-inputs.md)
- [贡献说明](CONTRIBUTING.md) · [安全说明](SECURITY.md) · [开源清理记录](docs/open-source-preparation.md)

## 许可证

本项目原创代码采用 [MIT](LICENSE) 许可证。Portal 是独立 Android 项目；协议参考文档及其他第三方材料保留各自许可证，见 [第三方声明](THIRD_PARTY_NOTICES.md)。
