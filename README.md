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

---

## Repository Structure

```
ntn-emulator/
├── README.md
|
├── configs/
│ ├── ue.yaml
│ ├── ran.yaml
│ └── ntn-link.yaml
│
├── ntn-link/
│ ├── link.go
│ ├── delay.go
│ ├── scheduler.go # Packet scheduling queue
│ └── json_watcher.go # ns-3 link-state reader
│
├── ran/
│ ├── ngap/
│ ├── rrc/
│ ├── gtp/
│ └── link/
│
├── ue/
│ ├── nas/
│ ├── rrc/
│ ├── tun/
│ └── link/
│
└──────
```

---

## Interaction with Other Repositories

### 1️⃣ ns-3 Repository (External)

- Produces time-varying link conditions
- Exports results as JSON
- No knowledge of 5G protocols

Example output:
```json
{
  "timestamp": 120.5,
  "satellite_id": "SAT-08",
  "delay_ms": 42.3,
  "jitter_ms": 3.1,
  "rsrp": -95,
  "doppler_hz": 980
}
```


ntn-emulator continuously monitors this output and updates its internal link model.

### 2️⃣ free5GC Repository (External)

https://free5gc.org/guide/3-install-free5gc/

---

⏱️ NTN-Link: The Only Place Where Delay Exists

All control-plane and data-plane traffic passes through the NTN-Link:

- NGAP (N2)
- GTP-U (N3)
- RRC signaling
- NAS messages

Packets are enqueued and released by the NTN-Link scheduler:

```
Send() → Queue → Delay Model → Scheduler → Socket
```

This guarantees:

- Consistent delay across planes
- Clean protocol logic
- Easy replacement of delay models

---

## License