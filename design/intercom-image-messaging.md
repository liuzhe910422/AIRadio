# 对讲图片消息设计与当前实现

状态：当前代码实现说明，核对日期 2026-10-31。

## 1. 功能范围

设备通过已有的 `self.camera.take_photo` 拍照。拍照成功后，固件将最近一帧编码为 JPEG，上传到 Intercom Server，并向当前房间广播图片消息。房间内设备收到消息后，在对讲记录中显示“收到一张图片”和缩略图；点击图片记录或缩略图可打开大图弹窗，弹窗右上角的 `X` 关闭后返回主界面。

图片广播与小智 MCP 会话、实时 Opus 对讲使用独立链路。系统视觉告警也复用这条图片链路：只有 VLLM 判断命中后才向目标房间广播图片 URL。图片内容不放进 WebSocket 控制帧；控制帧只携带图片 URL 和消息元数据。

## 2. 数据流程

```text
MCP self.camera.take_photo
  -> Camera::Capture()
  -> Camera::GetCapturedJpeg()
  -> HTTP POST /api/intercom/images
  -> 返回 /api/intercom/images/<id>
  -> WebSocket image_message 广播到当前房间
  -> 设备保存临时聊天记录
  -> 下载 JPEG、解码、显示缩略图
  -> 点击记录或缩略图，下载并显示大图弹窗

系统视觉告警流程为：

```text
服务端定时器
  -> 指定设备 alert_capture_request(image)
  -> 设备拍照并上传 JPEG
  -> 服务端下载并转 Base64 调用托管 LLM
  -> VLLM alert=true
  -> 广播 image_message URL、文字和 TTS
```
```

设备从已配置的 Intercom WebSocket 地址派生图片 HTTP 地址：`ws://` 转为 `http://`，`wss://` 转为 `https://`，并保留服务器 IP 和端口。

## 3. 服务端接口

`POST /api/intercom/images` 接收 multipart 字段 `file`，当前限制为 512 KiB，并要求 JPEG 文件头。响应示例：

```json
{"image_url":"/api/intercom/images/0123456789abcdef"}
```

`GET /api/intercom/images/<id>` 返回 `image/jpeg`。当前实现将图片保存在进程内存中，尚未提供鉴权、持久化、过期清理或缩略图接口，适用于可信内网联调。

设备通过 WebSocket 发送：

```json
{"type":"image_message","image_url":"/api/intercom/images/<id>","text":"收到一张图片"}
```

服务端补充 `message_id` 和 `source_id`，并向同一房间的已启用聊天设备广播。

## 4. 固件实现

`Camera` 增加 `GetCapturedJpeg()`。`EspVideo` 使用现有 JPEG 编码器将最近捕获帧转换为 JPEG。MCP 拍照工具保留原有照片解释返回，同时尝试把 JPEG 发送到 Intercom；图片广播失败不会阻止照片解释结果返回。

`IntercomChatMessage` 增加图片 URL、尺寸和图片标记。收到 `image_message` 后，固件将相对 URL 转换为 HTTP(S) 完整 URL，并加入当前聊天记录。告警图片只有 VLLM 命中后才广播。

3.5B 显示层在独立任务中下载图片并调用 JPEG 解码器。缩略图和大图当前均使用 JPEG 解码后的 RGB565 缓冲；弹窗使用独立 LVGL 容器和关闭按钮。网络回调不直接操作 LVGL。

## 5. 资源和已知限制

800×600 RGB565 解码缓冲约需要 960 KiB，叠加 JPEG 原文、解码临时内存、LVGL 和音频资源后可能出现 `jpeg_to_image: Failed to allocate memory for JPEG output buffer`。聊天记录刷新也可能重复启动图片下载任务。后续应增加按 `message_id` 的下载状态和图片缓存，并由服务端生成 160×120 左右的缩略图；同一时间只允许一个解码任务，关闭弹窗后释放大图缓冲。

服务端当前图片数据随进程退出丢失，图片 URL 没有访问令牌，且没有自动清理。正式部署前需要增加大小、格式、生命周期、权限和存储限制。

## 6. 验证

需要在至少两台同房间设备上验证：拍照上传、图片广播、缩略图显示、点击记录与缩略图打开弹窗、关闭弹窗、切房和断线清理。还应观察连续图片操作下的 PSRAM、内部 RAM、任务数量和 JPEG 解码失败率。
