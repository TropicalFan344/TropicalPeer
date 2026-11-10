# 裝置在線狀態設計草稿

裝置卡片要在使用者選檔案**之前**就說出對方能不能收：在線的卡片床邊有一個綠點，
不在線的寫「Offline · seen 2 hr. ago」。以前要等選完檔案、送出失敗，才跳出
「Moon Laptop is offline」的提示（違反產品原則 4「Say what's happening」）。

## 狀態

| 步驟 | 內容 | 狀態 |
|---|---|---|
| 0 | 這份草稿；隱私做法二選一 | 完成：選簡單版（2026-11-11，見「決定」） |
| 1 | 協定：`watch`、`presence`、`CapPresence` | 完成 |
| 2 | 伺服器：互相列出才回報、20 秒寬限、換清單通知 | 完成，有測試 |
| 3 | 引擎（node）：註冊後送清單、配對或移除後重送、記錄「上次看到」 | 完成，有測試 |
| 4 | appcore：`PeerDTO.presence`、`lastSeen`、`peers:changed` | 完成 |
| 5 | 介面：卡片綠點、離線文字、點下去先提醒、分享選單、10 種語言 | 完成；Windows 上用 `wails dev` + 本機伺服器 + `tpcli` 實測上線、下線、提醒 |
| 6 | 實際網路：PC、Mac VM、手機三台一起測；新舊版混用 | 大致完成（2026-11-11，公開伺服器 share.tropicalpeer.win:7777）：手機在另一台路由器（CHT Wi-Fi 105747-1），PC dev 版、手機 dev 版、Mac VM 上的 tpcli 互相配對；三方都看到彼此在線；PC→手機 20 MB 直連（不走中繼）、PC→Mac 直連，SHA-256 相符；手機→PC、手機→Mac 文字送到；手機 App 和 Mac 關掉後 PC 30 秒內顯示離線。新舊版混用、Mac 的正式 App 未測 |
| 7 | 部署：公開伺服器換新版 | 完成（2026-11-11，PC 上的 releaseserver，舊版留成 .exe.old） |

## 決定（2026-11-11）

使用者原本想要更隱私的做法，比較之後選了**簡單版**，理由是目前每個伺服器都是
使用者自己架、自己用。

- **簡單版（採用）**：每台裝置把配對過的裝置 ID 告訴伺服器；兩台都列出對方時，
  伺服器才回報彼此在線。陌生人知道 ID 也查不到。代價：伺服器知道每台裝置的配對清單。
  伺服器介紹連線時本來就看得到誰連誰，所以多知道的只有「從沒連過的配對」。
- **隱私版（以後再說）**：兩台裝置用身分金鑰算出只有彼此知道的密鑰，每天換一個
  有方向的代碼，在線時公布；查詢的一方下載整份在線代碼自己比對，伺服器不知道誰在看誰。
  大約兩倍的工作量，介面一樣。

**要重新考慮的時機**：決定要不要經營公開的預設伺服器時（PRODUCT.md 的待決定事項）。
協定一旦出去就很難換，舊版會一直送清單；換成隱私版時要兩種並存一段時間。

## 協定（`rendezvous/proto.go`）

都走既有的 presence 連線（註冊之後那一條）：

```go
TypeWatch    = "watch"    // 裝置 → 伺服器：Watch 是配對過的裝置 ID（整份，取代上一份）
TypePresence = "presence" // 伺服器 → 裝置：ID 是否 Online

Watch  []string `json:"watch,omitempty"`
Online bool     `json:"online,omitempty"`

const CapPresence = "presence" // 伺服器在 registered 的 Caps 裡宣告
const MaxWatch = 1000
```

## 伺服器（`server/watch.go`）

- 清單只存在記憶體，跟著那次註冊一起消失。
- 收到 `watch`：對清單上每個 ID 回一則 `presence`（雙方互列且在線才是 true，
  其他情況一律 false，所以陌生人分不出「離線」和「沒列我」）；剛變成互列的那一方
  也收到「上線」；從清單拿掉的、原本互列的那一方收到「下線」。
- 斷線後等 `OfflineGrace`（預設 20 秒）才通知下線；這段時間內重新註冊、而且
  清單上還有對方，就什麼都不說（換 Wi-Fi、重連不會讓卡片閃）。
- `pushMu` 讓「下線」和同一台的下一次「上線」不會互相超車。

## 引擎（`node/presence.go`）

- 註冊成功、伺服器有 `CapPresence` 才送清單；在線狀態只對這一次註冊有效，
  連線斷了全部變回「不知道」。
- 配對（`addPeer`）和移除（`RemovePeer`）之後重送清單。
- `peers.Peer.LastSeen`（unix 毫秒）：看到上線、看到下線、我們自己的連線斷掉時
  （那時還在線的）都記一次；關 App 的路上不寫檔。
- `PeerPresence(id) (known, online bool)`。

## 三種狀態（appcore 決定）

| `presence` | 條件 | 卡片 |
|---|---|---|
| `"online"` | 伺服器說在線 | 床右下角綠點；讀屏多念「Online」 |
| `"offline"` | 伺服器說不在線，**而且以前看過它在線**（`LastSeen > 0`），而且沒有把我們移除 | 床變淡、「Offline · seen 2 hr. ago」取代政策那一行 |
| `""` | 其他：舊伺服器、舊版 App（從沒看過在線）、我們沒連上伺服器、還沒回答 | 跟以前一樣 |

「以前看過」這條是為了舊版 App：它不送清單，伺服器只會說 false，但它其實可能在線。
新配對的兩台在配對完會立刻互列，所以馬上就有 `LastSeen`。

## 介面

- **卡片**（`PeerCard.svelte`）：綠點（`ok`，2.5px surface 外框）；離線時床 45% 透明加去色，
  文字保持原色（對比度）。點離線的卡片先跳提示「X is offline. Make sure TropicalPeer is open on it.」
  加一個「Send anyway」（狀態可能慢一點，或對方正在開）。
- **分享選單**（`ShareSheet.svelte`，Android）：離線的裝置把 ID 那行換成離線文字。
- 「seen 2 hr. ago」用瀏覽器的 `Intl.RelativeTimeFormat`，10 種語言都自動翻好；
  一分鐘內只寫「Offline」。
- 新字串：`peer.online`、`peer.offline`、`peer.offlineSeen`、`peer.sendAnyway`。

## 同時做的介面修正（第二次 critique）

- **通知不要疊滿畫面**：toast 最多 2 個（先丟沒有按鈕的舊的）；配對成功不再另外跳
  toast（對話框已經說了）；在 Transfers 頁面時不跳「已送出」；有邀請時未讀文字只顯示 1 則。
- **配對完導向第一次傳送**：成功畫面的主按鈕是「Send files to X」，「Done」變成次要。
- **卡片選單**：`role="menu"`、打開時焦點進去、上下鍵、Home/End、Escape 關閉並回到 ⋯ 按鈕。
- **卡片狀態給讀屏**：整張卡的按鈕用 `aria-describedby` 指向狀態那幾行。
- **被封鎖的卡片**：只有床變淡，「Blocked」和名字保持原色（以前 60% 透明只有 2.75:1）。
- **錯誤 toast** 用 `role="alert"`。

## 測試

- `server/watch_test.go`：只有互列才看得到；陌生人永遠是離線；寬限期內重連不會報下線；
  拿掉清單會通知對方。
- `node/presence_test.go`：配對完雙方馬上看到在線並記下 `LastSeen`；一方關掉後另一方看到離線；
  移除之後不再知道。
- 手動：本機伺服器（這個分支）+ `wails dev` + `tpcli`：配對、上線綠點、關掉 20 秒後離線、
  點離線卡片的提示、選單鍵盤操作。

## 還沒做

- 三台實機（PC、Mac VM、手機）跨網路測；Android 背景「Stay ready to receive」開與關。
- 新版 App 對舊版伺服器（應該全部是「不知道」）、新舊版 App 互配（舊的那台應該不顯示離線）。
- 公開伺服器換新版之前，新功能在那台伺服器上不會出現（App 照舊運作）。
