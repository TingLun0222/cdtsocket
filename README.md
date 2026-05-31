# cdtsocket — 即時 WebSocket 通訊服務

大學生心理輔導系統的即時訊息微服務，透過 WebSocket 協議實現學生與輔導教師的匿名雙向即時通訊。支援多台伺服器平行運作，透過 Redis 佇列解決跨伺服器的訊息路由問題。

## 技術棧

| 項目 | 技術 |
|------|------|
| 語言 | Go 1.18 |
| WebSocket | [Gorilla WebSocket](https://github.com/gorilla/websocket) v1.5.0 |
| ORM | [GORM](https://github.com/jinzhu/gorm) v1.9.16 |
| 資料庫 | MySQL |
| 訊息佇列 | Redis |

## 專案結構

```
cdtsocket/
├── main.go              # 主程式，含 WebSocket 處理與跨伺服器訊息路由邏輯
├── index.html           # 學生測試用 WebSocket 客戶端介面
├── teacherindex.html    # 教師測試用 WebSocket 客戶端介面
├── go.mod               # Go 模組定義
└── go.sum               # 依賴鎖定檔
```

## 環境需求

- Go 1.18+
- MySQL 資料庫（與 cdtuser 共用 `cdt` 資料庫）
- Redis（訊息佇列與跨伺服器用戶路由）

## 設定說明

目前設定直接寫於 `main.go`，部署前請修改以下參數：

| 參數 | 說明 |
|------|------|
| MySQL 連線字串 | 資料庫主機、帳號、密碼 |
| Redis 連線位址 | Redis 主機位址 |
| Redis 密碼 | Redis 認證密碼 |

## 啟動方式

```bash
# 下載依賴
go mod tidy

# 啟動服務（預設監聽 :80）
go run main.go
```

或編譯後執行：

```bash
go build -o cdtsocket .
./cdtsocket
```

> **多伺服器部署**：可在不同機器上同時啟動多個 cdtsocket 實例，Load Balancer 負責分配連線。所有實例共用同一組 MySQL 與 Redis，訊息路由由 Redis 協調。

## HTTP 路由

| 路由 | 方法 | 說明 |
|------|------|------|
| `/test` | GET | 顯示學生測試用 WebSocket 頁面 |
| `/teachertest` | GET | 顯示教師測試用 WebSocket 頁面 |
| `/ws` | WebSocket | 學生 WebSocket 連線端點 |
| `/teacherws` | WebSocket | 教師 WebSocket 連線端點 |

## WebSocket 通訊協議

### 建立連線與認證

客戶端建立 WebSocket 連線後，**必須立即**傳送認證訊息。服務端驗證失敗則關閉連線。

```json
{
  "sender": "a1b2c3",
  "receiver": "",
  "content": {},
  "password": "使用者密碼"
}
```

### 傳送訊息

認證成功後，即可傳送訊息至指定接收者（學生傳給教師，或教師傳給學生）：

```json
{
  "sender": "a1b2c3",
  "receiver": "d4e5f6",
  "content": {
    "text": "您好，老師"
  },
  "password": "使用者密碼"
}
```

**欄位說明**

| 欄位 | 型別 | 說明 |
|------|------|------|
| sender | string | 發送者 userid |
| receiver | string | 接收者 userid |
| content | object | 訊息內容（任意 JSON 物件） |
| password | string | 發送者密碼（每則訊息皆須驗證） |

### 心跳保活

為防止 WebSocket 因閒置而被自動關閉，客戶端須每 **30 秒**傳送一次心跳訊息：

```json
"health"
```

### 接收訊息

服務端會主動推送佇列中的訊息，格式與傳送格式相同：

```json
{
  "sender": "d4e5f6",
  "receiver": "a1b2c3",
  "content": { "text": "同學你好" },
  "password": ""
}
```

## 多伺服器訊息路由機制

本服務設計為多台機器平行運作，同一用戶的 WebSocket 可能連線至任意一台伺服器。為解決跨伺服器路由問題，採用以下機制：

```
用戶連線時
  → 將「用戶 userid ↔ 本機 IP」寫入 Redis
  → 初始化 Redis 中該用戶的訊息佇列

傳送訊息時
  → 將訊息寫入 Redis（以接收者 userid 為鍵）
  → 背景 goroutine 輪詢所有已連線用戶的佇列
      ├── 接收者在本機 → 直接推送至 WebSocket
      └── 接收者在其他伺服器 → 該台伺服器的背景 goroutine 負責投遞

訊息投遞後
  → 立即從 Redis 清除，避免重複推送
```

**執行緒設計**（對應 PDF 中的訊息讀取設計）：
- 每條 WebSocket 連線建立一個獨立 goroutine 用於**讀取訊息**
- 收到訊息後再開一個新 goroutine 進行**訊息處理與轉發**，確保讀取迴圈不被阻塞
- 推播（訊息投遞）使用**單一 goroutine** 統一管理，避免大量並發 Redis 查詢

## 與其他服務的關係

本服務僅負責即時通訊，不處理帳號建立或管理。使用者需先透過 [cdtuser](../cdtuser/README.md) 完成註冊與登入，取得 `userid` 後才能使用本服務建立 WebSocket 連線。
