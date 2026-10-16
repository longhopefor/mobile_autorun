# 部署指南

## 部署组成

工作台由一个 Go 服务、PostgreSQL 17 和编译后的 Vue 前端组成。Go 服务同时提供 API、WebSocket 和前端静态文件；启动时自动运行应用及 River 迁移。当前仅支持单服务实例，不要同时启动多个副本操作同一组数据。

手机安装独立 Portal APK。USB 隧道工具、旧版图库连接器和模型 CLI 连接器运行在连接手机 / 安装 CLI 的电脑上，通过 WebSocket 连接服务端，不在容器内执行。

## Docker Compose

```sh
cp .env.example .env
chmod 600 .env
```

编辑 `.env`：

- `POSTGRES_PASSWORD`：建议用 `openssl rand -hex 24` 生成。Compose 会将其拼接到数据库 URL，使用十六进制可避免 URL 保留字符的问题。
- `ADMIN_USERNAME` / `ADMIN_PASSWORD`：首次启动创建管理员，密码至少 12 字节，建议使用另一份随机密码。
- `WEB_ORIGIN`：浏览器实际访问的完整 Origin，默认 `http://localhost:8080`，没有结尾斜杠。
- 直接模型 API 可设置 `MODEL_API_KEY`；只用本地 CLI 时留空。

```sh
docker compose up -d --build
curl --fail http://127.0.0.1:8080/healthz
docker compose logs --tail=100 server
```

默认浏览器地址为 <http://localhost:8080>；PostgreSQL 不发布宿主机端口。Compose 为 Go 服务设置容器内监听地址、数据库 URL 和 `/app/data`，`.env` 中同名的源码开发配置不会覆盖这些值。镜像以非 root 用户运行，包含 FFmpeg 和中文 Noto 字体。

`.env` 中的 `MATERIAL_*` 变量会传入服务容器。自定义字体路径必须是**容器内部**可读的路径；宿主机字体需要额外挂载。`MATERIAL_GENERATOR_URL` 指向你自行部署的生成适配器，协议见 [业务工作流](business-workflows.md)。不配置时仍可用商品原图排版和图片合成 MP4。

### 局域网

在 `.env` 增加 `SERVER_BIND_IP=0.0.0.0`，将 `WEB_ORIGIN` 改为电脑的实际地址，例如 `http://192.168.1.100:8080`，重新运行 `docker compose up -d`。

手机在同一网络使用 `ws://192.168.1.100:8080/device/connect?transport=ws`。根据实际网卡地址替换示例 IP，并限制防火墙访问范围。USB 模式可继续通过本机 `127.0.0.1:8080` 访问。

### 远程 HTTPS / WSS

在服务前配置支持 WebSocket Upgrade 的 TLS 反向代理，转发整个站点至 `127.0.0.1:8080`，包括 `/device/connect`、`/device/media-connect`、`/model/connect` 和 `/api/`。为 WebSocket 及任务事件流设置足够长的空闲超时，事件流关闭代理缓冲。

- `WEB_ORIGIN=https://workbench.example.com`。
- 手机：`wss://workbench.example.com/device/connect?transport=ws`。
- 模型连接器：`wss://workbench.example.com/model/connect`。
- 若代理在另一台主机或容器中，调整服务监听 / Docker 网络及防火墙，使代理能访问服务。

当前登录 Cookie 的 Secure 属性依赖 Go 请求是否直接使用 TLS；Go 服务本身未提供 TLS 配置。单纯在外部代理终止 TLS，不会自动设置此属性。远程部署应由反向代理为 `session` Cookie 补上 Secure，限制 HTTP 访问，并先验收登录、退出、事件流及各连接器。当前未提供已验证的公网部署方案。

## 源码启动

需要 Go 1.24+、Node.js 24 / npm 和 PostgreSQL 17；使用素材视频功能还需要 FFmpeg 和中文字体。

若没有自己的数据库，可仅用 Compose 启动 PostgreSQL，并通过开发覆盖文件发布本机 5432 端口：

```sh
cp .env.example .env
# 编辑并替换密码；DATABASE_URL 的密码必须与 POSTGRES_PASSWORD 一致
chmod 600 .env
docker compose -f compose.yaml -f compose.dev.yaml up -d postgres
make build
make frontend
set -a
. ./.env
set +a
./bin/server
```

从项目根目录运行，Go 服务依赖相对路径 `frontend/dist`，且不会自动读取 `.env`。使用自己的 PostgreSQL 时，修改 `DATABASE_URL` 并跳过 Compose 命令。数据库需预先存在，服务账号需要迁移建表权限。

开发时可在另一终端运行：

```sh
cd frontend
npm ci
npm run dev
```

Vite 默认使用 <http://localhost:5173> 并将 `/api` 转发至 `127.0.0.1:8080`。将 Go 服务的 `WEB_ORIGIN` 设置为该开发 Origin 后重启。Vite 的 `/api` 代理用于前端开发；手机与模型连接器仍直接连接后端 8080 端口。

运行 `make build` 会生成五个工具：`server`、`model-connector`、`model-probe`、`device-connect`、`media-connector`。不要覆盖仍在运行的部署产物；升级时先停止对应服务再替换。

## 配置速查

| 环境变量 | 用途 / 默认值 |
|---|---|
| `DATABASE_URL` | 必填，PostgreSQL 连接串；Compose 自动设置 |
| `ADMIN_USERNAME` | 管理员名称，默认 `admin` |
| `ADMIN_PASSWORD` | 必填且至少 12 字节；启动仍会检查，已存在用户不更新密码 |
| `LISTEN_ADDR` | 源码默认 `127.0.0.1:8080`，Compose 为 `0.0.0.0:8080` |
| `WEB_ORIGIN` | 允许的前端 Origin；示例配置为 `http://localhost:8080` |
| `DATA_DIR` | 原始素材、截图及生成文件目录，源码默认 `.data` |
| `MODEL_API_KEY` | 直接模型 API 的密钥；模型配置引用环境变量名 |
| `MATERIAL_FONT_PATH` | 可选中文 TTF / OTF / TTC 字体路径 |
| `MATERIAL_FFMPEG_BINARY` | FFmpeg 可执行文件，默认 `ffmpeg` |
| `MATERIAL_GENERATOR_URL` | 可选 AI 场景图 / 视频适配器地址 |
| `MATERIAL_GENERATOR_API_KEY` | 可选适配器 Bearer 密钥 |
| `SERVER_BIND_IP` | 仅 Compose 的宿主机发布地址，默认 `127.0.0.1` |
| `TEST_DATABASE_URL` | 仅测试使用，指向允许创建独立数据库的专用 PostgreSQL |

模型配置中的密钥变量名须满足 `MODEL_[A-Z0-9_]+`。Compose 默认传入 `MODEL_API_KEY`；若使用其他名字，需要在 Compose 的 `server.environment` 中明确加入。

## 持久化、备份与升级

Compose 使用 `postgres_data` 保存数据库、`app_data` 保存素材和截图。备份必须同时包含两者；不要提交数据库、截图或日志到 Git。以下为停机一致性备份示例，先接管 / 取消任务并等待停止，再执行：

```sh
mkdir -p backups
chmod 700 backups
docker compose stop server
docker compose exec -T postgres pg_dump -U mobile_autorun -d mobile_autorun -Fc > backups/database.dump
docker compose run --rm --no-deps --user 0 --entrypoint tar server -C /app/data -czf - . > backups/app-data.tar.gz
chmod 600 backups/*
docker compose start server
```

先验证备份成功，再做升级。恢复请在隔离环境停服后使用 `pg_restore` 导入数据库，并将文件恢复到 `app_data` 根目录、归属 UID 10001；不要对活动数据库或非空数据卷直接覆盖。数据库和文件应使用同一停机窗口的备份。

升级前备份，取得新源码后运行 `docker compose up -d --build`，检查 `/healthz`、登录、手机观察和连接器状态。涉及迁移时，回滚二进制不等于回滚数据库；按迁移内容决定是否需要恢复备份。`docker compose down` 保留 named volume；**`docker compose down -v` 会删除数据库及素材卷**。

## 常见问题

- **启动要求 DATABASE_URL / 密码太短**：源码运行需要先加载 `.env`，模板密码必须替换；Compose 日志用于确认启动错误。
- **改了管理员密码但登录失败**：初始化采用插入且不覆盖已有用户；改环境变量不会重置密码，当前没有专用密码重置 CLI。
- **页面空白或 404**：确认在项目根目录运行，且执行过 `make frontend`；Docker 镜像自动构建前端。
- **手机离线 / 图片无法选取**：分别检查 ADB 授权、Portal 连接与图库能力，见 [手机接入](device-setup.md)。
- **模型报登录失效 / 不支持模型**：在 CLI 所在电脑核实登录及 `allowed_models`；API 检查视觉与工具调用兼容性。
- **任务暂停或动作结果不明**：核对手机实际页面和动作回放后再继续；不要通过反复恢复来重试可能已发生的发布 / 发送。

当前清理环境未安装 Docker，Compose 镜像构建、容器启动及上述备份恢复步骤尚未实机验收，不能将配置审查视为部署成功。
