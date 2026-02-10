# NTN Emulator - Missing Components & Roadmap

This document lists components described in the README but not yet fully implemented.

## ✅ Implemented Components

### cmd/
- ✅ `ran.go` - RAN control plane entry point
- ✅ `ue.go` - UE data plane entry point

### ran/
- ✅ `ngap/client.go` - NGAP transport layer
- ✅ `ngap/setup.go` - NG Setup procedure
- ✅ `gtp/tunnel.go` - GTP-U tunnel implementation
- ✅ `link/dataplane.go` - RAN data plane (UE ↔ RAN ↔ UPF)
- ✅ `link/gnb_dataplane.go` - gNB data plane abstraction
- ✅ `pdu_session.go` - PDU Session establishment
- ✅ `api.go` - gNB registration API

### ue/
- ✅ `context.go` - UE context management
- ✅ `nas/codec.go` - NAS encoding/decoding with security
- ✅ `nas/registration.go` - Registration procedure
- ✅ `tun/interface.go` - TUN interface management
- ✅ `link/dataplane.go` - UE data plane (raw IP over UDP)

### ntn-link/
- ✅ `json_watcher.go` - ns-3 state file monitor
- ✅ `link.go` - Link abstraction
- ✅ `delay.go` - Delay models
- ✅ `scheduler.go` - Packet scheduler

### configs/
- ✅ `ue.yaml` - UE configuration template
- ✅ `ran.yaml` - RAN configuration template
- ✅ `ntn-link.yaml` - NTN link configuration template

---

## 🔧 Missing/Incomplete Components

### 1. RAN RRC Layer (`ran/rrc/`)

**Status:** Directory created, no implementation

**Required Files:**
- `measurement.go` - RRC measurement reports
- `handover.go` - RRC handover procedures
- `reconfig.go` - RRC reconfiguration
- `state.go` - RRC connection state management

**Purpose:**
- Trigger handovers based on satellite change
- Manage RRC connection lifecycle
- Handle measurement reports

**Integration:**
- Should be called by `ntn-link` when satellite changes
- Interfaces with NGAP for core network handover signaling

---

### 2. UE RRC Layer (`ue/rrc/`)

**Status:** Directory created, no implementation

**Required Files:**
- `measurement.go` - Measurement report generation
- `handover.go` - UE-side handover handling
- `reconfig.go` - RRC reconfiguration acceptance

**Purpose:**
- Respond to RAN RRC commands
- Generate measurement reports
- Handle connection reconfiguration

**Note:** In current architecture, UE is data-plane only. RRC would only be needed if UE runs full control plane.

---

### 3. NTN-Link Integration

**Status:** Core modules exist, integration pending

**Missing Integration Points:**

#### 3.1 NGAP Socket Integration
```go
// ran/ngap/client.go needs modification
type NGAPClient struct {
    // ... existing fields
    scheduler *ntnlink.Scheduler  // ← Add this
}

// Wrap Send/Receive with scheduler
func (c *NGAPClient) Send(pdu []byte) error {
    c.scheduler.Enqueue(pdu)  // ← Apply NTN delay
    // ... wait for delivery
}
```

#### 3.2 GTP-U Socket Integration
```go
// ran/gtp/tunnel.go needs modification
type GTPTunnel struct {
    // ... existing fields
    uplinkScheduler   *ntnlink.Scheduler  // ← Add
    downlinkScheduler *ntnlink.Scheduler  // ← Add
}
```

#### 3.3 Link State Watcher
```go
// cmd/ran.go or cmd/ue.go needs to start watcher
watcher := ntnlink.NewJSONWatcher("/path/to/ntn_state.json", 100*time.Millisecond)
watcher.RegisterCallback(func(old, new *ntnlink.NTNState) {
    // Update delay model
    // Trigger handover if satellite changed
})
watcher.Start()
```

---

### 4. Configuration File Parsing

**Status:** YAML templates exist, parser missing

**Required:**
```go
// pkg/config/config.go
package config

type UEConfig struct {
    IMSI     string
    DNN      string
    SNssai   SNssaiConfig
    TUN      TUNConfig
    NTNLink  NTNLinkConfig
}

func LoadUEConfig(path string) (*UEConfig, error) {
    // Parse ue.yaml
}
```

**Integration:**
- Replace hardcoded constants in `cmd/ran.go` and `cmd/ue.go`
- Enable runtime configuration

---

### 5. Handover Logic

**Status:** Placeholder only

**Required Components:**

#### 5.1 Handover Decision (`ran/rrc/handover.go`)
```go
// Triggered by satellite change detection
func (h *HandoverDecider) OnSatelliteChange(oldSat, newSat string) {
    // 1. Initiate handover preparation
    // 2. Send Handover Required to AMF
    // 3. Wait for Handover Command
    // 4. Execute handover
}
```

#### 5.2 Integration with JSONWatcher
```go
watcher.RegisterCallback(func(old, new *ntnlink.NTNState) {
    if old != nil && old.ServingSatellite != new.ServingSatellite {
        handoverDecider.OnSatelliteChange(
            old.ServingSatellite,
            new.ServingSatellite,
        )
    }
})
```

---

### 6. Metrics and Monitoring

**Status:** Not implemented

**Recommended Infrastructure:**

#### 6.1 Link Metrics (`ntn-link/metrics.go`)
```go
type LinkMetrics struct {
    PacketsScheduled  uint64
    PacketsDelivered  uint64
    PacketsDropped    uint64
    AverageDelay      time.Duration
    HandoverCount     uint64
}
```

#### 6.2 Prometheus Exporter (optional)
- Expose metrics via HTTP endpoint
- Monitor delay, jitter, handover frequency

---

### 7. Multi-UE Support

**Status:** Single UE only

**Required Changes:**

#### 7.1 RAN Process
- Currently handles one UE context
- Should maintain `map[string]*UEContext`  (keyed by SUPI)
- Handle concurrent NGAP sessions

#### 7.2 Data Plane
- `ran/link/dataplane.go` already supports multiple UEs via UDP address mapping
- Control plane needs similar multiplexing

---

### 8. Testing Infrastructure

**Status:** No tests

**Recommended Tests:**

#### 8.1 Unit Tests
```
ntn-link/json_watcher_test.go
ntn-link/scheduler_test.go
ran/ngap/client_test.go
ue/nas/codec_test.go
```

#### 8.2 Integration Tests
```
tests/integration/registration_test.go
tests/integration/pdu_session_test.go
tests/integration/handover_test.go
```

#### 8.3 End-to-End Tests
- Full registration + PDU establishment
- Handover scenario
- Data plane throughput

---

## 🎯 Priority Roadmap

### Phase 1: Core Functionality (Current State ✅)
- [x] Basic registration
- [x] PDU session establishment
- [x] Data plane (UE ↔ RAN ↔ UPF)
- [x] JSON state monitoring

### Phase 2: NTN Integration (Next)
1. Integrate scheduler with NGAP/GTP sockets
2. Start JSONWatcher in cmd/ran.go
3. Add configuration file parsing
4. Test with ns-3 integration

### Phase 3: Handover Support
1. Implement RAN RRC handover logic
2. Connect satellite change → handover trigger
3. Test handover with ns-3

### Phase 4: Production Readiness
1. Add comprehensive tests
2. Implement metrics/monitoring
3. Multi-UE support
4. Performance optimization
5. Documentation

---

## 📋 Summary

### Component Status
| Component | Status | Priority |
|-----------|--------|----------|
| Basic Registration | ✅ Complete | - |
| PDU Session | ✅ Complete | - |
| Data Plane | ✅ Complete | - |
| JSON Watcher | ✅ Complete | - |
| Scheduler | ✅ Complete | - |
| **NTN Integration** | 🔧 TODO | **HIGH** |
| **Config Parsing** | 🔧 TODO | **HIGH** |
| **RRC Layer** | 🔧 TODO | MEDIUM |
| **Handover Logic** | 🔧 TODO | MEDIUM |
| Metrics | 🔧 TODO | LOW |
| Multi-UE | 🔧 TODO | LOW |
| Testing | 🔧 TODO | MEDIUM |

### Key Integration Points
1. **NGAP/GTP Sockets** → Wrap with NTN scheduler
2. **JSONWatcher** → Start in main() and wire callbacks
3. **Config Files** → Replace hardcoded values
4. **Handover** → Connect satellite change to RRC procedures

### Next Steps
1. Create `pkg/config/` package and implement YAML parsing
2. Modify `ran/ngap/client.go` to integrate scheduler
3. Modify `ran/gtp/tunnel.go` to integrate scheduler
4. Update `cmd/ran.go` to start JSONWatcher
5. Implement `ran/rrc/handover.go` for satellite-driven handover
