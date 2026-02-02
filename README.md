# NTN 5G Control Plane Emulator

## 概述

這是一個完整的 5G UE/RAN 模擬器，專為 NTN (Non-Terrestrial Network) 場景設計，可與 free5GC v4.2.0 整合，並支援與 ns-3 NTN 模擬環境的整合。

模擬器採用**分離式架構**，將 RAN 控制平面/數據平面與 UE 數據平面分離為獨立進程，實現更貼近真實網路的模擬環境。

## 系統架構

### 分離式架構設計

```
┌──────────────────────────────────────────────────────────────┐
│                     Core Network (free5GC)                     │
│         AMF (N2)           SMF            UPF (N3/N6)          │
└────────────┬────────────────────────────────┬─────────────────┘
             │ NGAP/SCTP                      │ GTP-U
             │ (Control Plane)                │ (Data Plane)
┌────────────┴────────────────────────────────┴─────────────────┐
│                    RAN Process (cmd_ran.go)                    │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │          Control Plane (NGAP/NAS)                        │ │
│  │  - NG Setup  - Registration  - PDU Session              │ │
│  └──────────────────────────────────────────────────────────┘ │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │          Data Plane Server                               │ │
│  │  - GTP-U Tunnel (RAN ↔ UPF)                             │ │
│  │  - UDP Relay (UE ↔ RAN)                                 │ │
│  └──────────────────────────────────────────────────────────┘ │
└────────────┬────────────────────────────────────────────────┬─┘
             │ UDP (Plain IP Packets)
┌────────────┴────────────────────────────────────────────────┴─┐
│                     UE Process (cmd_ue.go)                     │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │          TUN Interface (uetun0)                          │ │
│  │  - UE IP Address (from PDU Session)                     │ │
│  │  - Packet forwarding to RAN                             │ │
│  └──────────────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────────────┘
             │
       Application (ping, curl, etc.)
```

### 模組化結構

```
ntn-emulator/
├── ue_context.go       # UE 上下文管理（身份、安全、狀態）
├── ngap_client.go      # NGAP 傳輸層抽象（SCTP 連接）
├── ng_setup.go         # NG Setup 流程處理
├── registration.go     # UE Registration 狀態機
├── pdu_session.go      # PDU Session Establishment 狀態機
├── nas_codec.go        # NAS 編解碼工具
├── ran_dataplane.go    # RAN 數據平面（GTP-U 與 UDP 轉發）
├── ue_dataplane.go     # UE 數據平面（UDP 到 RAN）
├── tun.go              # TUN 介面管理
├── gtp.go              # GTP-U 封裝/解封裝
└── gnb_dataplane.go    # gNB 數據平面（替代方案）
```

### 主程式

```
../
├── cmd_ran.go          # RAN 控制平面 + 數據平面進程
└── cmd_ue.go           # UE 數據平面進程（TUN 介面）
```

### 設計原則

1. **關注點分離**：每個模組專注於單一職責
   - NG Setup: gNB 與 AMF 的 NGAP 關聯
   - Registration: UE 註冊與 NAS 安全建立
   - PDU Session: PDU Session 建立（與 Registration 解耦）

2. **狀態機驅動**：UE 狀態清晰定義
   ```go
   UEStateIdle → UEStateRegistering → UEStateRegistered → 
   UEStateEstablishingPDU → UEStatePDUActive
   ```

3. **可測試性**：各模組獨立，便於單元測試

4. **可擴展性**：為 ns-3 整合預留接口
   - NTN 延遲可注入在 NGAP/NAS 訊息邊界
   - Handover 事件可觸發狀態轉換

5. **進程分離**：RAN 與 UE 分離執行
   - RAN 進程：控制平面 + 數據平面伺服器
   - UE 進程：數據平面客戶端（TUN 介面）
   - 符合真實網路架構

## 核心模組說明

### 控制平面模組

#### 1. UE Context (`ue_context.go`)

管理 UE 的所有狀態資訊：

- **身份資訊**: SUPI, MCC, MNC, MSIN
- **NGAP ID**: RanUeNgapId, AmfUeNgapId
- **安全上下文**: 加密/完整性演算法、NAS 金鑰、計數器
- **認證訂閱**: K, OPC, OP, AMF, SQN
- **PDU Session**: DNN, S-NSSAI, UE IP Address, TEID
- **狀態管理**: CurrentState

```go
ue := ntnemulator.NewUEContext("imsi-208930000000001", 1)
ue.SetState(UEStateRegistering)
```

#### 2. NGAP Client (`ngap_client.go`)

抽象 NGAP 傳輸層（SCTP）：

- 建立與 AMF 的 SCTP 連接
- 發送/接收 NGAP 訊息
- 連接生命週期管理

```go
client := ntnemulator.NewNGAPClient(amfIP, ranIP, amfPort, ranPort)
client.Connect()
client.Send(ngapMessage)
```

#### 3. NG Setup (`ng_setup.go`)

處理 gNB 與 AMF 的初始化：

- 構建 NG Setup Request (包含 gNB ID, TAC, PLMN)
- 支援 NTN 識別（通過 RAN Node Name）
- 處理 NG Setup Response

```go
ngSetup := ntnemulator.NewNGSetupHandler(client, gnbID, "NTN-SAT-STARLINK", tac)
ngSetup.PerformNGSetup()
```

#### 4. Registration (`registration.go`)

實作完整的 UE Registration 流程：

1. **Registration Request**: 發送初始註冊請求
2. **Authentication**: 
   - 接收 Authentication Request
   - 計算 RES*
   - 衍生 NAS 安全金鑰 (KAMF, KNasEnc, KNasInt)
   - 發送 Authentication Response
3. **Security Mode**:
   - 接收 Security Mode Command
   - 發送 Security Mode Complete (含 5GMM capability)
4. **Registration Accept**:
   - 接收 Registration Accept
   - 發送 Registration Complete
5. **Configuration Update** (optional):
   - 接收 Configuration Update Command

```go
regHandler := ntnemulator.NewRegistrationHandler(ue, nasCodec, ngapClient)
regHandler.PerformRegistration()
```

#### 5. PDU Session (`pdu_session.go`)

處理 PDU Session Establishment：

1. **PDU Session Request**: 構建 PDU Session Establishment Request
2. **UL NAS Transport**: 封裝在 UL NAS Transport 中
3. **PDU Session Accept**: 接收並解析 DL NAS Transport
4. **資源設定**: 接收 PDU Session Resource Setup Request
5. **TEID 分配**: 為 GTP-U 隧道分配 TEID

與 Registration 完全解耦，可獨立執行。

```go
pduHandler := ntnemulator.NewPDUSessionHandler(ue, nasCodec, ngapClient)
pduHandler.EstablishPDUSessionForSeparateUE(pduSessionID, "internet", snssai, ranN3IP)
```

#### 6. NAS Codec (`nas_codec.go`)

NAS 訊息的編解碼工具：

- **解碼**: 支援安全解密與完整性驗證
- **編碼**: 支援加密與 MAC 計算
- **Builder 函數**: 構建各種 NAS 訊息

```go
codec := ntnemulator.NewNASCodec(ue)
nasPdu, _ := codec.Decode(securityHeaderType, payload)
encoded, _ := codec.Encode(nasMessage, secHeaderType, true, false)
```

### 數據平面模組

#### 7. RAN Data Plane (`ran_dataplane.go`)

RAN 數據平面伺服器，負責：

- **UE 連接管理**: 監聽 UE 連接（UDP）
- **GTP-U 隧道**: 與 UPF 建立 GTP-U 隧道
- **雙向轉發**:
  - Uplink: UE → RAN → UPF (封裝 GTP-U)
  - Downlink: UPF → RAN → UE (解封裝 GTP-U)

```go
dataPlane := ntnemulator.NewRANDataPlane(
    ranDataPlaneIP, ranDataPlanePort,  // UE 連接點
    ranN3IP, ranN3Port,                 // RAN N3 端點
    upfAddr,                            // UPF N3 地址
    ulTEID, dlTEID,                     // GTP-U TEID
    imsi)
dataPlane.Start()
```

**關鍵特性**：
- 支援網路命名空間（Network Namespace）配置
- 動態 N3 端口分配
- GTP-U 封裝/解封裝
- 多 UE 支援（未來擴展）

#### 8. UE Data Plane (`ue_dataplane.go`)

UE 數據平面，負責：

- **TUN 介面整合**: 從 TUN 讀取 IP 封包
- **UDP 轉發**: 將封包轉發到 RAN
- **雙向轉發**:
  - Uplink: TUN → UDP → RAN
  - Downlink: RAN → UDP → TUN

```go
ueDataPlane := ntnemulator.NewUEDataPlane(ranAddr, imsi, tunIface)
ueDataPlane.Start()
```

#### 9. TUN Interface (`tun.go`)

管理 Linux TUN 虛擬網路介面：

- **介面創建**: 建立並配置 TUN 介面
- **IP 設定**: 設定 UE IP 地址
- **路由設定**: 配置預設路由（使用 ip 命令）

```go
tunIface := ntnemulator.NewTUNInterface("uetun0", ueIPAddress)
defer tunIface.Close()
```

**需求**：
- 需要 root 權限（sudo）
- Linux 系統（使用 TUN/TAP 驅動）

#### 10. GTP-U Tunnel (`gtp.go`)

GTP-U 協議實作：

- **封裝**: 將 IP 封包封裝為 GTP-U
- **解封裝**: 從 GTP-U 提取 IP 封包
- **TEID 管理**: 上行/下行 TEID 映射

```go
gtpTunnel := ntnemulator.NewGTPTunnel(localTEID, remoteTEID, upfIP, upfPort, tunIface)
gtpTunnel.Start()
```

## 網路配置

### 網路命名空間架構

```
┌─────────────────── Default Namespace ────────────────────┐
│                                                           │
│  AMF (127.0.0.18:38412)        UPF (10.100.200.1:2152)  │
│                                    │                      │
│                                    │ veth0                │
└────────────────────────────────────┼──────────────────────┘
                                     │
                              veth pair (10.100.200.0/24)
                                     │
┌────────────────────── gnb_ns Namespace ──────────────────┐
│                                    │ veth1                │
│                                    │                      │
│  RAN N2: 10.100.200.2:38413       │                      │
│  RAN N3: 10.100.200.2:2152 ───────┘                      │
│  RAN Data Plane: 127.0.0.50:31414                        │
│                                                           │
└───────────────────────────────────────────────────────────┘
```

### 設定網路命名空間

使用提供的腳本設定：

```bash
# 建立網路命名空間與 veth pair
sudo ./setup_netns.sh

# 測試連線
sudo ip netns exec gnb_ns ping -c 3 10.100.200.1

# 清除設定
sudo ./teardown_netns.sh
```

## 使用方式

### 前置準備

1. **啟動 free5GC**：
   ```bash
   cd ~/free5gc
   ./run.sh
   ```

2. **設定網路命名空間**：
   ```bash
   cd /home/ntn/ntn
   sudo ./setup_netns.sh
   ```

### 編譯

```bash
cd /home/ntn/ntn

# 編譯 RAN 進程
go build -o ntn_ran cmd_ran.go

# 編譯 UE 進程（需要編譯到 /tmp 以便用 sudo 執行）
go build -o /tmp/ntn_ue cmd_ue.go
```

### 執行步驟

#### Step 1: 啟動 RAN 進程（在 gnb_ns 命名空間中）

在 Terminal 1 中執行：

```bash
# 在網路命名空間中執行 RAN 進程
sudo ip netns exec gnb_ns ./ntn_ran -imsi 208930000000001 -satellite STARLINK

# 或使用自訂參數
sudo ip netns exec gnb_ns ./ntn_ran \
  -imsi 208930000000002 \
  -satellite LEO-SAT-5 \
  -ue-n3-ip 127.0.0.101
```

**RAN 進程會執行**：
1. MongoDB 連接並插入 UE 訂閱資料
2. 建立與 AMF 的 NGAP 連接（SCTP）
3. 執行 NG Setup（註冊 gNB）
4. 執行 UE Registration（認證、安全模式、註冊完成）
5. 執行 PDU Session Establishment（獲取 UE IP、TEID）
6. 啟動 RAN Data Plane Server（監聽 UE 連接 + GTP-U 隧道）

**RAN 輸出範例**：
```
✓ UE Registration successful (SUPI: imsi-208930000000001)
✓ PDU Session established (ID: 1, DNN: internet)
✓ UE IP Address: 10.60.0.1
✓ RAN N3 IP (for GTP-U): 10.100.200.2:2152
✓ UPF TEID: 0x00000001
✓ RAN TEID: 0x00000001
✓ RAN Data Plane Server started on port 31414
```

保留 RAN 進程執行中，記下 UE IP Address。

#### Step 2: 啟動 UE 進程（需要 sudo）

在 Terminal 2 中執行：

```bash
# 使用 RAN 分配的 UE IP
sudo /tmp/ntn_ue \
  -ue-ip 10.60.0.1 \
  -ran-addr 127.0.0.50:31414 \
  -imsi 208930000000001 \
  -tun uetun0
```

**UE 進程會執行**：
1. 創建 TUN 介面（uetun0）
2. 設定 UE IP 地址到 TUN
3. 連接到 RAN Data Plane Server
4. 啟動雙向封包轉發

**UE 輸出範例**：
```
✓ TUN interface created: uetun0 (10.60.0.1)
✓ Connected to RAN data plane
✓ UE Data Plane is ACTIVE!

You can now test connectivity:
  sudo ping -I uetun0 8.8.8.8
  sudo ping -I uetun0 google.com
```

#### Step 3: 測試連線

在 Terminal 3 中執行：

```bash
# 測試 DNS
sudo ping -I uetun0 8.8.8.8 -c 3

# 測試網際網路連線
sudo ping -I uetun0 google.com -c 3

# 測試 HTTP
curl --interface uetun0 http://httpbin.org/ip
```

### 執行流程詳解

#### RAN 進程流程

1. **MongoDB 連接**: 連接到 free5GC 的 MongoDB
2. **UE 資料插入**: 將 UE 訂閱資料寫入 MongoDB
3. **NGAP 連接**: 建立與 AMF 的 SCTP 連接（從 gnb_ns 到 default namespace）
4. **NG Setup**: 完成 gNB 初始化（包含 NTN 衛星識別）
5. **UE Registration**: 完整的註冊流程（含認證與安全）
6. **PDU Session Establishment**: 建立數據連線
   - 獲取 UE IP Address
   - 分配 GTP-U TEID（RAN TEID 與 UPF TEID）
   - 接收 QoS 參數
7. **RAN Data Plane 啟動**: 
   - 在 `127.0.0.50:31414` 啟動 UDP 伺服器（UE 連接點）
   - 在 `10.100.200.2:2152` 建立 GTP-U 端點（與 UPF 通訊）
   - 開始雙向轉發

#### UE 進程流程

1. **TUN 介面創建**: 建立 `uetun0` 介面
2. **IP 配置**: 設定 UE IP（從 PDU Session 獲得）
3. **連接到 RAN**: 建立 UDP 連線到 RAN Data Plane
4. **封包轉發**: 
   - Uplink: 應用層 → TUN → UDP → RAN → GTP-U → UPF → 網際網路
   - Downlink: 網際網路 → UPF → GTP-U → RAN → UDP → TUN → 應用層

#### 數據流向

```
Application (ping)
       ↓
  TUN (uetun0)
       ↓ IP packet
  UDP Socket
       ↓ UDP to 127.0.0.50:31414
  RAN Data Plane
       ↓ Encapsulate GTP-U
  UDP Socket (10.100.200.2 → 10.100.200.1:2152)
       ↓ GTP-U packet
  UPF (free5GC)
       ↓ Decapsulate & Route
  Internet
```

## 命令參數說明

### cmd_ran.go 參數

| 參數 | 預設值 | 說明 |
|------|--------|------|
| `-imsi` | `208930000000001` | UE IMSI（不含 "imsi-" 前綴） |
| `-satellite` | `UNKNOWN` | 衛星名稱（NTN 識別符，會加入 gNB 名稱） |
| `-ue-n3-ip` | `127.0.0.100` | UE N3 IP（未使用，保留供未來擴展） |

### cmd_ue.go 參數

| 參數 | 預設值 | 說明 |
|------|--------|------|
| `-ue-ip` | （必填） | UE IP 地址（從 RAN 輸出獲得） |
| `-ran-addr` | `127.0.0.1:9487` | RAN Data Plane 地址 |
| `-imsi` | （必填） | UE IMSI |
| `-tun` | `uetun0` | TUN 介面名稱 |

## 故障排除

### 常見問題

1. **RAN 進程無法連接到 AMF**
   ```
   Failed to connect to AMF: dial sctp 127.0.0.18:38412: connection refused
   ```
   **解決方法**：確認 free5GC AMF 正在執行
   ```bash
   netstat -tlnp | grep 38412
   ```

2. **UE 進程無法創建 TUN 介面**
   ```
   Failed to create TUN interface: operation not permitted
   ```
   **解決方法**：使用 sudo 執行
   ```bash
   sudo /tmp/ntn_ue -ue-ip 10.60.0.1 -ran-addr 127.0.0.50:31414 -imsi 208930000000001
   ```

3. **Ping 測試失敗（Destination Host Unreachable）**
   
   **檢查步驟**：
   - RAN 與 UE 進程都在執行中
   - UE IP 正確（從 RAN 輸出複製）
   - TUN 介面已建立：`ip addr show uetun0`
   - UPF 的 NAT/路由設定正確

4. **網路命名空間連線問題**
   ```
   Cannot connect from gnb_ns to default namespace
   ```
   **解決方法**：重新設定 veth pair
   ```bash
   sudo ./teardown_netns.sh
   sudo ./setup_netns.sh
   ```

## 與 ns-3 NTN 整合設計

### 整合架構

```
┌──────────────── ns-3 Simulation ─────────────────┐
│                                                   │
│  Satellite Mobility Model                        │
│  Channel Model (Path Loss, Doppler)              │
│  Handover Decision                                │
│                                                   │
└────────────┬──────────────────────┬───────────────┘
             │                      │
   Delay/Jitter Events      Handover Events
             │                      │
┌────────────┴──────────────────────┴───────────────┐
│         NTN Emulator (Go Process)                 │
│                                                   │
│  - NGAP Client (with delay injection)            │
│  - Handover State Machine                        │
│  - Satellite Selection Logic                     │
│                                                   │
└────────────┬──────────────────────────────────────┘
             │
       NGAP/NAS Messages
             │
┌────────────┴──────────────────────────────────────┐
│              free5GC Core Network                 │
└───────────────────────────────────────────────────┘
```

### 整合點

1. **延遲注入**:
   ```go
   // 在 NGAP Client 的 Send/Receive 中加入延遲
   func (c *NGAPClient) Send(data []byte) (int, error) {
       time.Sleep(ntnDelay) // 由 ns-3 提供
       return c.conn.Write(data)
   }
   ```

2. **Handover 事件**:
   ```go
   // ns-3 觸發 handover 事件時
   func (ue *UEContext) OnHandover(targetGnbID []byte) {
       // 執行 Xn handover 或重新 registration
   }
   ```

3. **衛星可見性**:
   ```go
   // ns-3 提供衛星可見性資訊
   func (emulator *NTNEmulator) UpdateSatelliteVisibility(satID string, visible bool) {
       if !visible {
           // 觸發重選或 handover
       }
   }
   ```

### ns-3 整合範例

```go
// ns-3 事件驅動模式
type NS3NTNEmulator struct {
    ue         *UEContext
    ngapClient *NGAPClient
    // ns-3 模擬器接口
    ns3Simulator *NS3Simulator
}

func (e *NS3NTNEmulator) OnSatelliteHandover(event *HandoverEvent) {
    // 1. 觸發 Xn handover
    // 2. 更新 UE context
    // 3. 重新建立 NGAP 連接（如需要）
}

func (e *NS3NTNEmulator) OnNTNDelayUpdate(delay time.Duration) {
    // 更新 NGAP Client 的延遲模型
    e.ngapClient.SetNTNDelay(delay)
}
```

## 架構演進

### 當前架構 vs 原始版本

| 特性 | 原始版本 (test_ntn_full_security.go) | 當前版本 (分離式架構) |
|------|-------------------------------------|----------------------|
| 結構 | 單一檔案，所有功能混合 | 13 個模組化檔案 + 2 個主程式 |
| 進程模型 | 單一進程 | RAN 與 UE 分離進程 |
| 數據平面 | 無 | 完整 GTP-U 實作 |
| TUN 介面 | 無 | 完整支援（Linux TUN/TAP） |
| 可維護性 | 低 | 高（模組化設計） |
| 可測試性 | 困難 | 容易（每個模組可獨立測試） |
| 可擴展性 | 有限 | 高（易於加入新功能） |
| PDU Session | 未完整實作 | 完整實作（含資源設定） |
| 狀態管理 | 無 | 清晰的狀態機 |
| 網路隔離 | 無 | Network Namespace 支援 |
| 真實性 | 模擬 | 接近真實網路（可實際 ping/curl） |
| ns-3 整合 | 困難 | 預留接口，易於整合 |

### 為何採用分離式架構？

1. **貼近真實網路**：RAN 與 UE 在實際環境中是獨立設備
2. **靈活性**：可獨立重啟 UE 而不影響 RAN（模擬設備更換）
3. **多 UE 支援**：未來可連接多個 UE 進程到同一 RAN
4. **測試便利**：可以直接在 UE 的 TUN 介面上使用標準工具（ping, curl, iperf）
5. **ns-3 整合**：數據平面可由 ns-3 模擬，控制平面由 Go 處理

## 符合 3GPP 標準

- **TS 23.502**: 5G System Procedures（5G 系統流程）
- **TS 24.501**: Non-Access-Stratum (NAS) protocol（NAS 協議）
- **TS 38.413**: NG Application Protocol (NGAP)
- **TS 38.821**: Solutions for NR to support non-terrestrial networks (NTN)
- **TS 29.281**: General Packet Radio System (GPRS) Tunnelling Protocol User Plane (GTPv1-U)

## 未來擴展方向

### 短期目標

1. **多 UE 支援**: RAN 同時服務多個 UE
2. **Service Request**: 從 IDLE 狀態恢復連接
3. **Deregistration**: 正常去註冊流程
4. **錯誤處理**: 更完善的異常處理與恢復

### 中期目標

1. **Xn Handover**: 支援 NTN 場景的衛星間切換
2. **TA Update**: 追蹤區域更新（衛星移動場景）
3. **QoS 管理**: 支援多個 QoS Flow
4. **效能監控**: 延遲、吞吐量、封包遺失率統計

### 長期目標（ns-3 整合）

1. **NTN 特定功能**:
   - Timing Advance 處理
   - Ephemeris 資訊交換
   - Doppler 補償
2. **衛星星座模擬**:
   - 多衛星可見性管理
   - 動態衛星選擇
   - 負載平衡
3. **通道模型**:
   - Path Loss 計算
   - 陰影衰落
   - 降雨衰減

## 開發指南

### 新增功能模組

1. 在 `ntn-emulator/` 目錄建立新的 `.go` 檔案
2. 定義清晰的介面與結構
3. 實作單元測試
4. 更新 README.md 說明

### 測試建議

```bash
# 單元測試
go test ./ntn-emulator/...

# 整合測試（需要 free5GC 執行中）
./run_integration_test.sh

# 效能測試
iperf3 -c <server_ip> -B <ue_ip>
```

## 貢獻

歡迎提交 Pull Request 或開啟 Issue。請遵循以下原則：

1. 保持模組化設計
2. 遵循 Go 語言慣例
3. 添加充分的註解
4. 符合 3GPP 標準

## 參考資料

### 相關專案

- [free5GC](https://github.com/free5gc/free5gc) - 開源 5G Core Network
- [ns-3](https://www.nsnam.org/) - Network Simulator 3
- [free5gmano](https://github.com/free5gmano/free5gmano) - 5G MANO

### 3GPP 規範

- [TS 23.501](https://www.3gpp.org/DynaReport/23501.htm) - System architecture for the 5G System
- [TS 23.502](https://www.3gpp.org/DynaReport/23502.htm) - Procedures for the 5G System
- [TS 24.501](https://www.3gpp.org/DynaReport/24501.htm) - NAS protocol
- [TS 38.413](https://www.3gpp.org/DynaReport/38413.htm) - NGAP
- [TS 38.821](https://www.3gpp.org/DynaReport/38821.htm) - NTN solutions

## 授權

與主專案相同（請參考 LICENSE 檔案）

---

**Last Updated**: 2026-02-02  
**Version**: 2.0 (分離式架構)  
**Maintainer**: NTN Research Team
