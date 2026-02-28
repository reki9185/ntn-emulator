# NTN Emulator

`ntn-emulator` is a lightweight 5G Non-Terrestrial Network (NTN) emulation framework that bridges **ns-3 physical-layer simulations** with **5G core and RAN protocol stacks** (e.g., free5GC).

This repository focuses on **control-plane and data-plane behavior under NTN characteristics**, such as long propagation delay, jitter, and satellite-driven handover, while keeping the physical-layer simulation fully decoupled.

---

## Design Philosophy

This project strictly follows a **separation-of-concerns** principle:

| Component | Responsibility |
|---------|----------------|
| ns-3 | Physical world simulation (satellites, mobility, propagation) |
| ntn-emulator | 5G protocol behavior + NTN link emulation |
| free5GC | 5G Core Network (AMF / UPF / SMF) |

---

## Repository Scope

This repository implements:

- UE and RAN protocol stacks
- NTN link emulation
- Handover decision logic (RRC-based)
- Interfaces to free5GC (NGAP, GTP-U)
- Dynamic link condition updates from ns-3

## Demo

### Build

```bash
cd /home/ntn/ntn-emulator

go mod tidy
go mod download

# Build RAN executable
go build -o /tmp/ntn_ran ./cmd/ran.go

# Build UE executable  
go build -o /tmp/ntn_ue ./cmd/ue.go
```

### Test

```bash
go run test_watcher.go
```

### Run

1. Setup namespace

```bash
# Create namespace
sudo ./setup.sh up
```

2. Start free5GC

```bash
# Replace amf with amf-ntn
cd NFs
git clone https://github.com/reki9185/amf.git
git checkout ntn

# Recompile free5GC
cd .. && make amf
```

```bash
# Run free5GC
./reload.sh enp0s3
./run.sh
```

3. Modify NF configuration:

    - ~/free5gc/config/amfcfg.yaml

        Replace `ngapIpList` IP from `127.0.0.18` to `10.0.1.1`:

        ```yaml
        ngapIpList:
          - 10.0.1.1
        ```

    - ~/free5gc/config/smfcfg.yaml

        Replace N3 interface's endpoints IP from `127.0.0.8` to your `10.0.1.1`:

        ```yaml
        interfaces:
          - interfaceType: N3
            endpoints:
              - 10.0.1.1
        ```

    - ~/free5gc/config/upfcfg.yaml

        Replace N6 interface address IP from `127.0.0.8` to `10.0.1.1`:

        ```yaml
        gtpu:
          forwarder: gtp5g
          iifList:
            - addr: 10.0.1.1
        ```

4. Register UE in webconsole
- Open http://localhost:5000
- Add subscriber with IMSI: `208930000000001`

5. Start ntn-emulator

```bash
# Start ran
sudo ip netns exec ran_ns /tmp/ntn_ran -imsi imsi-208930000000001

# Start UE in another terminal
sudo ip netns exec ue_ns /tmp/ntn_ue -ue-ip 10.60.0.1 -ran-addr 10.0.2.1:31414 -imsi 208930000000001
```

6. Test connectivity

```bash
sudo ip netns exec ue_ns ping -c 3 -I ueTun0 8.8.8.8
```

### Clean

1. Shutdown RAN and UE

2. Close free5GC
```bash
./force_kill.sh
./force_kill.sh -db
```

3. Delete namespace
```bash
sudo ./setup down
```

## Repository Structure

```
ntn-emulator/
├── cmd/                    # Main executables
│   ├── ran.go             # RAN control plane (was: cmd_ran.go)
│   └── ue.go              # UE data plane (was: cmd_ue.go)
│
├── configs/               # Configuration templates
│   ├── ue.yaml
│   ├── ran.yaml
│   └── ntn-link.yaml
│
├── ntn-link/              # NTN link emulation
│   ├── json_watcher.go    # NEW: ns-3 state monitor
│   ├── link.go            # NEW: Link abstraction
│   ├── delay.go           # NEW: Delay models
│   ├── scheduler.go       # NEW: Packet scheduler
│   └── README.md          # NEW: Module documentation
│
├── ran/                   # RAN components
│   ├── api.go             # gNB API (was: gnb_api.go)
│   ├── pdu_session.go     # PDU session handling
│   ├── ngap/
│   │   ├── client.go      # NGAP client (was: ngap_client.go)
│   │   └── setup.go       # NG Setup (was: ng_setup.go)
│   ├── gtp/
│   │   └── tunnel.go      # GTP-U tunnel (was: gtp.go)
│   ├── link/
│   │   ├── dataplane.go   # RAN data plane (was: ran_dataplane.go)
│   │   └── gnb_dataplane.go
│   └── rrc/               # NEW: Directory for RRC (not implemented)
│
└── ue/                    # UE components
    ├── context.go         # UE context (was: ue_context.go)
    ├── nas/
    │   ├── codec.go       # NAS codec (was: nas_codec.go)
    │   └── registration.go # Registration (was: registration.go)
    ├── tun/
    │   └── interface.go   # TUN interface (was: tun.go)
    ├── link/
    │   └── dataplane.go   # UE data plane (was: ue_dataplane.go)
    └── rrc/               # NEW: Directory for RRC (not implemented)
```

## Interaction with Other Repositories

### 1️⃣ ns-3 Repository (External)

- Produces time-varying link conditions
- Exports results as JSON
- No knowledge of 5G protocols

Example output:
```json
{
  "timestamp": 120,
  "serving_satellite": "STARLINK-2692",
  "delay_ms": 3
}
```

ntn-emulator continuously monitors this output and updates its internal link model.

### 2️⃣ free5GC Repository (External)

https://free5gc.org/guide/3-install-free5gc/


## Architecture Flow

```
┌─────────────┐
│   ns-3      │ ← Physical layer simulation
│ (satellite) │
└──────┬──────┘
       │ ntn_state.json (delay, satellite ID)
       ↓
┌─────────────────────────────────────────┐
│          ntn-link (emulator)            │
│  - json_watcher: monitors state         │
│  - scheduler: applies NTN delay         │
│  - delay models: propagation delay      │
└──────┬──────────────────────────────────┘
       │
       ↓
┌──────────────┐         ┌──────────────┐
│  RAN (cp)    │←──N2───→│   free5GC    │
│  cmd/ran.go  │         │   (AMF/SMF)  │
└──────┬───────┘         └──────┬───────┘
       │                        │
       │ raw IP/UDP             │ N3 (GTP-U)
       ↓                        ↓
┌──────────────┐         ┌──────────────┐
│  UE (dp)     │         │  UPF         │
│  cmd/ue.go   │         │              │
└──────┬───────┘         └──────┬───────┘
       │                        │
       └────────Internet────────┘
```

## License
