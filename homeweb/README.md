# 灵矽 AIRadio 产品展示主页

纯 HTML / CSS / JavaScript，无构建依赖、无登录、无后端。可直接打开 `index.html`，也可启动本地服务：

```sh
cd homeweb
python3 -m http.server 8765 --bind 127.0.0.1
```

浏览器访问 http://127.0.0.1:8765 。部署时发布本目录的 `index.html`、`styles.css`、`app.js` 和 `assets/` 即可。所有展示资源均在本地，无外部字体或 CDN 依赖。

## 内容与交互

- 首屏 CSS 立体设备，可点击切换开讲状态。
- 四项产品能力：一键对讲、语音操控、在线 AI 托管、近期消息回顾。
- 四组可交互场景：发言、选房、托管切换、近期总结。仅前端演示，不录音、不连接设备、不调用 AI 服务。
- 五张真实设备照片，支持切换和放大；弹窗支持 Escape 关闭。
- 桌面、平板、手机适配；支持键盘切换体验标签、减少动态效果偏好。

## 图片来源

用户提供的实机照片已复制到 `assets/device-*.png`；原始文件目录：`/Users/liuzhe/new-work/xrobotd/xrobotd/debug/review/`。

场景照片下载自 Unsplash：

- `assets/truck.jpg`: 图片端点：https://images.unsplash.com/photo-1519003722824-194d4455a60c
- `assets/warehouse.jpg`: 图片端点：https://images.unsplash.com/photo-1586528116311-ad8dd3c8310d

首屏设备和能力插图使用 CSS / 内联 SVG 绘制，为界面概念演示，真实固件界面见实机区域。

## 检查

使用 Playwright + 无头 Chrome 检查桌面及 375 / 390 / 768 / 1024 像素视口，检查横向溢出、图片加载、浏览器脚本错误和主要交互。浏览器截图保存在 `review/`，不需部署。
