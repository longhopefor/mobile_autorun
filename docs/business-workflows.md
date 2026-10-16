# 微信客服、电商素材、小红书获客与运营

新增入口：**微信 AI 客服 / 电商素材工作流 / 小红书获客运维**。版本接口为 0.2.0；手机与模型连接协议保持兼容，模型连接器需使用本次构建版本以获取新工具 Schema。

## 使用流程

### 微信 AI 客服

1. 配对 Portal 手机，创建 API / Codex CLI / WorkBuddy CLI 模型配置。
2. 输入微信个人资料页的实际账号名称，选择「跟进指定客户」或「处理会话列表」。跟进名单每行填写一个备注名或微信号。
3. 填写业务知识和回复提示词，设置收集数量、发送数量、间隔及操作轮数。发送上限为 0 时仅收集。
4. 加好友必须单独启用，且客户在名单中。智能体先核对客户资料页、记录线索，再打开验证页、填写申请正文并发送。
5. 线索页查看实际展示的联系方式、意向与依据，点击截图核对；CSV 可直接导入表格。

联系方式没有在页面明确出现则留空。意向 high/medium/low/unknown 是模型对原话的判断，不是购买概率。界面展示最近 2000 条记录；CSV 导出当前用户的全部线索，也可按任务筛选，包含来源、任务、页面证据与更新时间，并转义表格公式字符。

### 电商单品素材

上传 1–9 张 PNG/JPEG 商品图，填写真实商品规格与卖点、目标用户及风格要求。点击「生成全部素材」，模型输出结构化标题、正文和分镜，服务端生成：

- `detail-*.png`：960×1600 商品详情页素材。
- `xhs-*.png`：960×1280 小红书图文素材。
- `scene-*.png`：保留真实照片的场景主题排版。
- `product.mp4`：由场景排版图组成的静音 MP4，720×960，每页 3 秒。
- ZIP 中的 `manifest.json`：文案、分镜、场景生成提示词、资产 ID、执行状态、模型用量与待核对事项。

支持编辑结构化文案 / 分镜后重新渲染、下载单个文件或 ZIP。核对后勾选确认，可创建既有的 `xhs_image/draft` 手机任务：上传图片、核对账号、填写内容、保存草稿、重新打开核验。编辑 JSON 的字段定义见下方业务记录协议。

**真实照片排版与图片短视频不等于扩散模型场景生成或动态 AI 视频。** 若需要后两种输出，配置下述生成适配器并勾选 AI 服务。勾选但未配置会失败，不返回假生成结果。未安装 ffmpeg 时保留图片输出，并在结果里明确标记视频缺失。

### 小红书获客与运维

四种模式：

- `research`：按主题搜索，查看实际排序和互动数据，记录热门帖子与有需求评论。
- `draft`：先记录至少一个相关帖子，再结合主题和需求生成原创标题、正文、图文页。服务端渲染 PNG 并创建独立 `xhs_image/draft` 子任务，子任务负责手机草稿保存和核验。
- `reply`：搜索热门帖，对明确有需求的用户评论回复；先保存需求证据，再发送。
- `operate`：打开指定账号的评论通知，按提示词和知识库回复。

帖子热度来源是当前可见排序和互动数据；没有可见数据时必须标记未知，不能当成全站排名。图文参考仅使用本次任务实际记录的主题 / 帖子，不自动复制收藏库全部资料。

所有业务均可设置重复运营计划（15 分钟至 7 天）。服务器每 30 秒检查到期计划。上一轮等待接管 / 核验，以及其草稿子任务尚未结束时，不创建下一轮；停用计划不会取消正在执行的任务，可去任务工作台接管或取消。

## 发送与恢复

新工具 `record_business` 用于保存结构化业务记录；`dispatch_business` 用于发送已核对的回复 / 好友申请。普通 `phone_action` 禁止直接点击可识别的发送、回复、发布、添加到通讯录按钮。发送前检查账号、收件人、原话与编辑框正文；无法在无障碍信息中核验时请求接管，不猜点击位置。

业务记录绑定当前任务所属的观察和截图。发送由数据库原子预留记录约束账号发送间隔、单任务上限和并发去重，再使用原有设备租约、稳定页面检查与动作账本执行。

- `reported`：手机动作有明确回报，**不表示消息已送达或对方已读**，仍需页面核验。
- `reserved` / `uncertain`：已预留或结果不确定，不自动重发。
- 相同账号、收件人和同一段原话默认不重复回复（不使用模型随意生成的 key/source 去重）。没有平台消息 ID 时，相同原话的新消息也可能被保守跳过，需要人工处理。
- 好友验证页只沿用本任务刚核对的客户资料。回退、回桌面或重新启动应用会丢弃该临时对象。
- 服务重启将未完成的素材生成标记失败，允许显式重试；不会伪报 ready。
- 业务手机任务沿用原有 10 分钟执行时限、创建时 1–100 次观察决策预算（可追加至累计 500 轮）与人工最终核验。自动计划同样受这些限制，不是无限常驻聊天机器人。

目前新微信 / 小红书界面流程通过无障碍 UI 与模拟服务验证；实际 App 版本的资料页、好友验证页、评论输入框以及键盘按钮仍需真机验收。无障碍树缺失可核验字段时自动任务会停下，不能据此声称所有页面都可无人值守。

## 服务配置

```dotenv
# 中文 TTF/OTF/TTC 字体；macOS 自动尝试 Arial Unicode / STHeiti / Songti，Docker 安装 Noto CJK
MATERIAL_FONT_PATH=/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc
# 默认 PATH 中的 ffmpeg；Docker 已安装
MATERIAL_FFMPEG_BINARY=ffmpeg
# 可选 AI 场景图/视频生成适配器（HTTPS 或 loopback HTTP）
MATERIAL_GENERATOR_URL=https://your-adapter.example/generate
MATERIAL_GENERATOR_API_KEY=YOUR_PRIVATE_SERVER_KEY
```

适配器不是绑定某一家厂商。由你自己的服务将以下协议翻译到实际图像 / 视频模型；每次请求必须在 180 秒内返回。请求 `POST MATERIAL_GENERATOR_URL`，Content-Type JSON；配置密钥时使用 Bearer。支持：

```json
{
  "kind": "scene",
  "content": {
    "kind": "material", "key": "product", "name": "", "contact": "", "intent": "",
    "reason": "需要人工核对的表述", "source": "", "evidence": "",
    "title": "小红书标题", "body": "正文",
    "frames": [{"title": "卖点", "text": "真实信息", "scene": "场景或镜头描述"}]
  },
  "reference_images": ["BASE64_ORIGINAL_PRODUCT_IMAGE"],
  "preserve_product": true,
  "watermark": false
}
```

`kind` 为 `scene` 或 `video`。响应：

```json
{"outputs":[{"name":"scene.png","mime":"image/png","data_base64":"BASE64_BYTES"}]}
```

scene 只接受真实可解码 PNG/JPEG（最多 4000 万像素）；video 接受带 MP4 文件头的 `video/mp4`。1–9 个输出，每个最多 16MiB。不下载返回 URL、不接受任意本机文件路径。密钥由服务端环境变量提供，不进入用户模型配置或前端。

## API

全部接口使用原有登录认证与 owner 范围。

| 接口 | 用途 |
|---|---|
| `POST /api/v1/tasks` | template=`wechat_crm` 或 `xhs_growth`，携带 business 配置；Idempotency-Key 必须提供 |
| `GET /api/v1/business/records?kind=lead&task_id=...` | 查看线索、帖子、图文记录和截图资产 |
| `GET /api/v1/business/leads.csv` | 导出客户线索 |
| `GET /api/v1/business/outreach` | 查看已预留 / 回报 / 不确定发送 |
| `POST /api/v1/materials` | 创建单品工作流；Idempotency-Key 必须提供 |
| `GET /api/v1/materials` / `GET /api/v1/materials/:id` | 列表与详情 |
| `POST /api/v1/materials/:id/generate` | `{}` 由模型生成；或 `{content: BusinessRecord}` 编辑后重渲染 |
| `GET /api/v1/materials/:id/export` | 导出 ZIP |
| `POST /api/v1/materials/:id/draft` | `{device_id,account,confirmed:true}` 创建草稿任务 |
| `GET /api/v1/campaigns` / `POST /api/v1/campaigns` | 查看 / 创建 `{name,spec,interval_minutes}` |
| `PATCH /api/v1/campaigns/:id` | `{enabled:false}` 停用 |

数据库新增迁移 `004_business_workflows.sql`，服务启动自动应用。现有数据不删除、不覆盖原始采集目录。
