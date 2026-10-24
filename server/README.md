# 对讲转发服务（第一阶段）

这是单实例 Go WebSocket 服务。服务端注册本机 ID、限制同时只能上传一段语音，并把原始 Opus 帧转发给同一房间的其他在线设备。设备初始进入默认房间，可创建或加入其他房间。只有一台设备在线时也可以正常连接和发送，`end_ack` 的 `ok` 为 `true`、`receivers` 为 `0`，不会回放给发送者。

调试 WAV 默认开启。服务端将收到的 Opus 解码为 16 kHz、单声道 PCM，完整上传后保存为 WAV。中断的会话删除临时文件。WAV 仅用于本地调试，不提供离线补播。

需要 Go 1.24 或更高版本、CGO 编译器、`pkg-config`，以及 `libopus` 和 `libopusfile` 开发包。安装原生依赖：

```sh
# macOS（Homebrew）
brew install opus opusfile pkg-config

# Debian/Ubuntu
sudo apt install libopus-dev libopusfile-dev pkg-config
```

如果 `go run` 报错 `Package 'opusfile' not found`，说明编译时无法找到 `libopusfile`。Go 依赖 `github.com/hraban/opus` 默认同时链接 `opus` 和 `opusfile`，只安装 `opus` 不够；即使传入 `-save-wav=false`，仍需要这些编译依赖。安装后可用 `pkg-config --modversion opus opusfile` 检查两个库是否都能找到，再重新运行服务。如果仍报错，检查 `PKG_CONFIG_PATH` 是否包含相应 `.pc` 文件所在目录。

在仓库根目录运行：

```sh
cd server
go run . -listen 0.0.0.0:8080 -db airadio.db
```

管理页面地址为 `http://<服务器局域网IP>:8080/`，用户名为 `admin`，密码为 `admin`。会话有效期为 12 小时，重启后需要重新登录；HTTPS 连接下 Cookie 会启用 Secure 标记。

`-db airadio.db` 启用 SQLite 持久化，需要系统安装 `sqlite3` 命令；不传 `-db` 时房间和服务配置仅保存在内存。管理端关闭房间使用 `POST /api/rooms/<房间ID>/close`，在线成员会停止原房间播放、退出托管并收到默认房间状态。

默认录音目录为当前工作目录下的 `recordings/`，按上面的命令运行时即 `server/recordings/`。可用 `-record-dir /path/to/recordings` 指定目录，或用 `-save-wav=false` 关闭保存。服务启动时会打印目录，完整录音保存后会打印文件路径。WAV 含真实语音，不会自动清理；服务只适合可信局域网调试。

设备进入现有 Wi-Fi 配网模式后，在 `http://192.168.4.1` 的“高级设置”中填写各不相同的本机 ID，以及 `ws://<服务器局域网IP>:8080/ws`。两项都留空表示停用对讲。保存后完成 Wi-Fi 配网并退出配网模式，设备联网后配置生效。设备屏幕首次点击“录音”，再次点击“发送”；录制中按 X 可取消，BOOT 键仍用于原有助手操作。

目标板型是 `waveshare/esp32-s3-touch-lcd-3.5b`。固件使用现有 ESP-IDF 音频编解码器和独立对讲 WebSocket。触摸、录放音、断线恢复与 PSRAM 峰值仍需至少两台实机验证。设备 WebSocket 目前没有鉴权、内建加密、离线补播或送达保证；管理 API 使用独立的管理员会话鉴权。
