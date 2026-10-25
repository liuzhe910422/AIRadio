# Lingxi AIRadio：業務現場向け AI インターコム

（[English](README.md) | [中文](README_zh.md) | 日本語）

<p align="center"><img src="homeweb/assets/airadio-architecture.svg" alt="Lingxi AIRadio システム構成" width="900"></p>

Lingxi AIRadio は、XiaoZhi の音声インタラクションと ESP32 ファームウェアを基盤にした業務向けインターコムソリューションです。車両隊列、物流パーク、倉庫、現場チームを対象に、リアルタイム半二重通話、音声によるルーム操作、オンライン AI 管理応答、直近の会話確認を一つのワークフローにまとめます。

> **ワンタップで通話。ひとことで操作。忙しい時は AI がサポート。**

## 解決したい課題

ドライバーや倉庫・現場作業員は、運転や搬送で両手がふさがっていることが多くあります。従来の無線機は即時性に優れる一方、ルーム管理には手動操作が必要です。音声アシスタントは柔軟ですが、緊急の発言開始には向きません。

AIRadio は操作ごとに適した入力を使います。**即時発言は大きなボタン、複数手順の操作は音声、すぐに応答できない時は AI** が担当します。チームは素早く話し、少ない操作でグループを切り替え、直前の内容を確認できます。

## 主な機能

- **ワンタップのリアルタイム通話**：480×320 タッチ画面の大きなボタンで開始・終了。専用 WebSocket が Opus フレームを同じルームの端末へ転送します。
- **音声によるルーム管理**：XiaoZhi MCP ツールでルームの一覧、作成、参加、退出、招待の受付、托管の有効・無効を操作できます。ルーム一覧は画面で確認できます。
- **オンライン AI 托管**：人の発言を ASR で認識した後、托管端末が直近の文脈から返信要否を判断します。必要な場合は TTS でルームに返答します。人の発言が優先され、自動音声は中断できます。
- **会話の振り返り**：発言者と時間を含む短期文字起こしを表示し、音声を再生できます。「直前の会話をまとめて」と音声で要約も依頼できます。
- **現場で分かりやすい状態表示**：通常モードは青、托管モードは黄色で表示し、ルーム、発言者、システム通知を画面に明示します。
- **管理コンソールとシミュレーター**：Go サービスで端末・ルーム管理、ブラウザ Opus シミュレーター、ASR・TTS・托管 LLM の設定とテストを提供します。

製品ページ：[AIRadio ホームページを開く](http://154.94.238.56/)。

## プロジェクト紹介動画

- サーバーから直接ダウンロード：[http://154.94.238.56/video/ready.mp4](http://154.94.238.56/video/ready.mp4)
- 動画ファイルは比較的大きいため、ダウンロードしてからの視聴をおすすめします。
- Baidu Netdisk の予備リンク：[10月25日(1).mp4](https://pan.baidu.com/s/1T_n8MzPlYifnVEFZb5InGw)
- 抽出コード：`hty9`

> このデモサイトは現在 HTTP を使用しています。ブラウザによってはプライバシーまたは安全性の警告が表示されます。アドレスが `154.94.238.56` であることを確認し、「続行」「アクセスを許可」などの選択肢を選んでください。

## システム構成

<p align="center"><img src="homeweb/assets/airadio-architecture.svg" alt="AIRadio システム構成" width="900"></p>

AIRadio 端末は Intercom Service を経由せず、Lingxi 音声チャネルへ直接接続して音声操作を行います。Intercom Service は端末間のリアルタイム音声、ルーム、記録を担当します。

1. **AIRadio 端末**：`main/intercom/` が端末 ID、ルーム状態、録音・再生、短期記録、MCP ツールを管理します。現在の主な対象は Waveshare ESP32-S3 Touch LCD 3.5B です。
2. **Intercom Service**：`server/` の Go サービスが WebSocket 音声転送、ルーム・メンバー管理、HTTP コンソール、SQLite 永続化を提供します。インターコム接続は XiaoZhi アシスタントのセッションと独立しています。
3. **AI サービス**：ASR は発言を文字に変換し、TTS は通知と托管返答を音声化し、Hosting LLM は返答要否を判断します。有界の非同期キューによりリアルタイム音声をブロックしません。
4. **Lingxi 音声チャネル**：AIRadio 端末が Lingxi（XiaoZhi）音声チャネルへ直接接続し、自然言語の理解と MCP ツール呼び出しを行います。実際の発言操作は大きな端末ボタンで行うため、音声操作とインターコムを並行して使えます。

音声経路は `マイク → 16 kHz モノラル Opus → WebSocket → 同じルームの端末 → ジッタバッファ → デコード再生` です。セッション終了後、サーバーが ASR を非同期実行し、条件を満たす托管端末が LLM 判定と TTS 応答を行います。

## 現在の範囲

主な対象は Waveshare ESP32-S3 Touch LCD 3.5B です。端末は直近 20 件を保持し、音声と文字はサーバーの一時メモリに保存します。端末アカウント連携、複数組織・ロール分離、オフライン再送、恒久履歴、複数インスタンス間ルーティングは未実装です。端末および再生 API は信頼できる連携環境を前提とします。托管には ASR、TTS、Hosting LLM の有効化と端末のオンライン状態が必要です。

## クイックスタート

### インターコムサービスを実行

```sh
cd server
go run . -listen 127.0.0.1:9200 -db ./data/intercom.db
```

コンソールは同じプロセスから提供されます。ログイン後に ASR、TTS、托管 LLM を設定してください。第三者 API キーはサーバー側だけに保存されます。

### ファームウェアをビルド

ESP-IDF v6.0.1 以降が必要です。`release/v6.1` ブランチを推奨し、ESP-IDF 5.x はサポートしません。

```sh
python3 scripts/build.py --list-boards
python3 scripts/build.py waveshare/esp32-s3-touch-lcd-3.5b --name default
```

ボード名は `--list-boards` の出力に合わせてください。書き込み後、Wi-Fi 詳細設定で `intercom.device_id` とインターコムサービス URL を設定します。

## ドキュメント

- [アーキテクチャ概要](design/intercom-overview.md)
- [インターコムプロトコル](design/intercom-protocol.md)
- [ASR とセッション記録](design/intercom-asr-session-chat.md)
- [AI 托管モード](design/intercom-hosting.md)
- [MCP 実装](design/intercom-mcp-implementation.md)
- [480×320 UI](design/intercom-ui-480x320.md)
- [評価ガイドと設計上の判断](design/evaluation-guide.md)

## ライセンス

本プロジェクトは MIT License で公開しています。AIRadio サービス、端末ファームウェア、製品ホームページはこのリポジトリで継続的に進化しています。
