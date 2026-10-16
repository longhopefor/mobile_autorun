# 手机接入指南

手机需要 Android、Mobilerun Portal 及相应权限。历史验证设备为 Android 12 / HarmonyOS 4.2；其他系统版本须重新验收。Portal 独立维护，下载入口见 [上游项目](https://github.com/droidrun/mobilerun-portal)，本仓库不提供 APK。

## 准备

1. 安装 Portal，按其引导开启无障碍、截图和文件权限；需要中文输入时配置 Portal 输入法。
2. 使用 USB 工具时安装 Android Platform Tools，运行 `adb devices -l`，在手机上允许 USB 调试。设备必须显示 `device`，而非 `unauthorized`。
3. 启动工作台，在「设备与连接 → 新建配对 → Portal 手机」生成 token，15 分钟内首次连接。手机 token 与模型连接器 token 用途不同。
4. 配对 token、设备序列号与下载的配置保存在 `configs/*.local.json`，不要上传或贴到公开 Issue。

## USB 自动维护

服务端需能在连接手机的电脑上通过 `127.0.0.1:8080` 访问。源码构建工具：

```sh
make build
cp configs/device-usb.example.json configs/device.local.json
chmod 600 configs/device.local.json
```

编辑配置：`adb_binary` 填 ADB 可执行文件路径，`serial` 填 `adb devices -l` 第一列，`token` 填手机配对 token。已有配对可删除 token 字段以保留 Portal 原值。

```sh
./bin/device-connect --config configs/device.local.json
```

工具核对固定设备的 ADB 授权，创建 `adb reverse`，并配置 Portal 的反向连接。每 5 秒检查一次，重新插线后维护隧道。USB 模式仅支持本机 loopback WS；默认手机端口与服务端端口一致，可通过 `phone_port` 调整。

也可手动创建隧道，并在 Portal 的 Reverse Connection 中填写 `ws://127.0.0.1:8080/device/connect?transport=usb_reverse` 和 token：

```sh
adb -s YOUR_DEVICE_SERIAL reverse tcp:8080 tcp:8080
```

工具“接入已配置”不代表手机在线；在工作台检查状态并获取实际截图。退出工具后保留隧道和 Portal 配置，但不再维护；删除隧道使用 `adb -s YOUR_DEVICE_SERIAL reverse --remove tcp:8080`。

## 直接 WS / WSS

服务端需要监听手机可访问的地址，Compose 局域网部署见 [部署指南](deployment.md#局域网)。手机 Portal → Reverse Connection 填写以下一种地址及手机 token：

```text
ws://192.168.1.100:8080/device/connect?transport=ws
wss://workbench.example.com/device/connect?transport=ws
```

示例 IP / 域名需替换为实际部署地址。手机直连电脑时不能填写手机自己的 `127.0.0.1`。远程连接需配置 TLS 反向代理和 WSS。

可以直接在手机设置，不需要 ADB；或在初次 USB 连接时写入配置：

```sh
cp configs/device-ws.example.json configs/device-ws.local.json
# 编辑地址、ADB 路径、设备 serial 和手机 token
chmod 600 configs/device-ws.local.json
./bin/device-connect --config configs/device-ws.local.json --once
```

同一手机同一时刻只有一个生效连接。切换前接管任务并停止 USB helper，然后配置目标模式。连接被替换时旧观察失效，旧动作不转发或重发到新连接。USB helper 发现手机被改成无线地址后会退出，避免抢回配置。

## 图库索引

文件上传成功不代表手机图库已收录。工作台上传图片并读回核对字节后，还需要媒体索引，才开始选图。

在设备页点击「检测图库能力」：

- `portal_native`：当前 Portal 支持 `media/capabilities`、`media/index` 扩展且已授权；通过现有手机连接扫描任务图片，无需额外连接器。此扩展 APK 需单独取得，不能假设官方 APK 已支持。
- `usb_legacy`：旧版 Portal 可通过电脑的 ADB MediaConnector 索引。
- `unavailable`：检查手机、连接器或权限。原生权限拒绝、索引失败、超时不会自动回退为成功。

旧版兼容方式：

```sh
cp configs/media.example.json configs/media.local.json
# 编辑固定 ADB 路径、serial，使用已激活的手机 token
chmod 600 configs/media.local.json
./bin/media-connector --config configs/media.local.json
```

该工具只处理 `Pictures/mobile_autorun_<任务UUID>/01.png` 等任务图片，不提供任意 Shell RPC。原生扫描返回的媒体 URI 顺序不等于图库视觉顺序，选图仍需观察确认。

## 验收顺序

1. 工作台显示在线，能获得真实截图、前台应用和界面树。
2. 仅测试设置页面的启动、点击、滑动、返回与中文输入。
3. 模型只读识别合成图片，再执行“打开设置并搜索蓝牙，不更改开关”。
4. 验证素材上传与图库可见性，再验证草稿保存及重新打开核对。
5. 在具体账号、内容和目标明确后另行验收发送 / 发布。

登录、验证码、安全提示、权限不足或结果不明时暂停接管。ADB 授权、Portal 在线、模型识别和完整业务任务应分别验收，不能相互替代。
