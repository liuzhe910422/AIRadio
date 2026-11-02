# 灵矽 AIRadio 产品展示主页

纯 HTML / CSS / JavaScript，无构建依赖、无登录、无后端。可直接打开 `index.html`，也可启动本地服务：

```sh
cd homeweb
python3 -m http.server 8765 --bind 127.0.0.1
```

浏览器访问 http://127.0.0.1:8765 。部署时发布本目录的 `index.html`、`styles.css`、`app.js` 和 `assets/` 即可。所有展示资源均在本地，无外部字体或 CDN 依赖。

## 内容与交互

- 深海军蓝背景、冷色光晕和网格，CSS 设备模型支持点击切换开讲状态。
- 按沟通协作、现场信息、任务处理组织产品能力。
- 五步高温处理演示：感知异常、人员拍照、视觉判断、人工任务、复查恢复。支持点选步骤和循环查看。
- 七组本地交互：对讲、选房、托管、总结、英文翻译、图片协作与任务状态。
- 八张实机图，支持切换、放大和 Escape 关闭；图片协作演示中的截图也可放大。
- 纯前端场景模拟，不录音、不连接设备、不调用 AI，不包含视频、嵌入播放器或外部字体依赖。
- 桌面、平板、手机适配，支持键盘切换标签和减少动态效果偏好。

## 图片来源

用户提供的实机照片保留在 `assets/device-*.png`，页面使用压缩的 `assets/device-*.webp`；原始文件目录：`/Users/liuzhe/new-work/xrobotd/xrobotd/debug/review/`。

新增 `device-image.webp` 与 `device-task.webp` 分别截取自项目 `design/video/拍照功能-成片.mp4`（约16秒）和 `design/video/告警agent-成片.mp4`（约55秒），只发布静态截图。`device-translation.webp` 截取自 `design/video/翻译功能.mp4` 约34秒，画面同时包含中文原话、Translation 标签和英文译文；裁去部分桌面背景，保留实际屏幕内容。

场景照片下载自 Unsplash：

- `assets/truck.jpg`: 图片端点：https://images.unsplash.com/photo-1519003722824-194d4455a60c
- `assets/warehouse.jpg`: 图片端点：https://images.unsplash.com/photo-1586528116311-ad8dd3c8310d

首屏设备和能力插图使用 CSS / 内联 SVG 绘制，为界面概念演示，真实固件界面见实机区域。

## 检查

使用 Playwright + 无头 Chrome 检查 320 / 375 / 390 / 768 / 1024 / 1440 像素视口，检查横向溢出、图片加载、浏览器脚本错误、七组演示、五步流程、图片弹窗、键盘切换与减少动态效果偏好。网页改动不涉及固件或硬件行为。浏览器验证截图位于 `/tmp/airadio-web-review/`，不需部署。
