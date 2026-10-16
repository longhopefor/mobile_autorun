# 模型与本机连接器

## 直接模型 API

在服务端环境中设置 `MODEL_API_KEY`，重启服务。在工作台新增模型配置，选择「直接 API」，填写支持图片输入及原生工具调用的 OpenAI 兼容 Base URL、模型名和密钥环境变量名 `MODEL_API_KEY`。服务端将 Base URL 拼接 `/chat/completions`，所以 Base URL 通常包含 `/v1`。

配置中保存的是环境变量名，密钥由服务端读取。自定义变量名须满足 `MODEL_[A-Z0-9_]+`；Compose 需要另行显式透传该变量。外部 API 地址须使用 HTTPS，loopback 本地服务可使用 HTTP。不同供应商对图片和工具调用的兼容性需实际验证。

## 本机 CLI 连接器

在安装了 CLI 的电脑构建连接器：

```sh
make build
cp configs/connector.example.json configs/connector.local.json
chmod 600 configs/connector.local.json
```

1. 在「设备与连接」创建**模型连接器**配对，填写配置的 `token`。
2. `server_url` 为服务端的 `/model/connect` WebSocket 地址，本机默认 `ws://127.0.0.1:8080/model/connect`，远程使用 WSS。
3. Codex 方式配置 `codex_binary`；WorkBuddy 方式填写 `workbuddy_node` 和 `workbuddy_entry` 的实际可执行文件 / CLI 入口路径，不需要的字段可留空。
4. 在该电脑的终端中先完成对应 CLI 登录，再运行：

```sh
./bin/model-connector --config configs/connector.local.json
```

5. 工作台新增 Codex CLI 或 WorkBuddy CLI 模型配置，关联已在线的连接器。

模型标识留空表示继承 CLI 默认配置。指定模型时，该标识也必须加入本机配置的 `allowed_models`。不要把手机配对 token 用在模型连接器上。

连接器用固定参数关闭 CLI 的本机工具，传入截图并解析受限结构化决策；实际手机动作由服务端再次校验后下发。CLI 参数及输出格式存在版本差异，升级 CLI 后应重新验证。历史 Codex 验证版本为 `0.144.1`，并不保证未来版本兼容。

## WorkBuddy 登录

独立 CLI 的登录态需要单独验证，桌面应用已登录不能证明 CLI 能继承。`workbuddy_node` 应为能运行该 CLI 的 Node 可执行文件，`workbuddy_entry` 为本机实际的 CLI JavaScript 入口，均需按安装方式填写。

在空目录中使用这两个路径启动交互 CLI，并按原生 `/login` 提示完成登录；不要把账号、验证码或登录输出提交到仓库。连接器检测到认证提示会返回 `workbuddy_login_required`，不会把它当作成功决策。WorkBuddy 真实视觉与手机完整闭环尚未完成历史验收。

## 只读探测

先准备不含个人信息的 PNG，例如纯色测试图：

```sh
./bin/model-probe --config configs/connector.local.json --provider codex_cli --image /absolute/path/fixture.png
# WorkBuddy 使用 --provider workbuddy_cli
```

探测会将图片发送至所选 CLI 及其模型服务并消耗额度，返回结构化决策，不执行手机动作。工作台「模型配置」中的只读测试也会真实调用模型，应使用允许交给该服务处理的图片。

认证失效、额度不足和本机工具被拦截不会自动重试。网络、限流和部分无效结果可有限重试；固定错误码会进入任务记录，原始 CLI 诊断与凭据不应公开。
