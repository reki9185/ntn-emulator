# Configuration Guide

## Overview

Both RAN and UE now support YAML configuration files, following the same pattern as free-ran-ue.

## Configuration Files

- **configs/ran.yaml**: RAN/gNB configuration (network addresses, gNB ID, satellite, etc.)
- **configs/ue.yaml**: UE configuration (authentication keys, PLMN, DNN, etc.)

## Running with Configuration

### RAN Process

```bash
# Build RAN
go build -o /tmp/ntn_ran ./cmd/ran.go

# Run with default config (configs/ran.yaml + configs/ue.yaml for auth)
sudo ip netns exec ns3 /tmp/ntn_ran

# Run with custom config path
sudo ip netns exec ns3 /tmp/ntn_ran -config path/to/ran.yaml -ue-config path/to/ue.yaml

# Override IMSI from command line
sudo ip netns exec ns3 /tmp/ntn_ran -imsi 208930000000002
```

### UE Process

```bash
# Build UE
go build -o /tmp/ntn_ue ./cmd/ue.go

# Run with default config (configs/ue.yaml)
sudo ip netns exec ue_ns /tmp/ntn_ue -ue-ip <IP_FROM_PDU_SESSION>

# Run with custom config
sudo ip netns exec ue_ns /tmp/ntn_ue -ue-ip <IP> -config path/to/ue.yaml

# Override specific values
sudo ip netns exec ue_ns /tmp/ntn_ue -ue-ip <IP> -ran-addr 10.0.2.1:31414 -imsi 208930000000001
```

## Key Configuration Parameters

### RAN (configs/ran.yaml)

- `gnb.amfN2Ip`: AMF N2 IP address (default: 10.0.1.1)
- `gnb.ranN2Ip`: RAN N2 IP address (default: 10.0.1.2)
- `gnb.satellite`: Satellite name (default: STARLINK-180)
- `gnb.gnbId`: gNB ID in hex (default: 000314)
- `gnb.tai.tac`: Tracking Area Code in hex (default: 000001)

### UE (configs/ue.yaml)

- `ue.authenticationSubscription.encPermanentKey`: K key (hex string)
- `ue.authenticationSubscription.encOpcKey`: OPC key (hex string)
- `ue.authenticationSubscription.authenticationManagementField`: AMF (default: 8000)
- `ue.authenticationSubscription.sequenceNumber`: SQN (default: 000000000023)
- `ue.plmnId.mcc`: Mobile Country Code (default: 208)
- `ue.plmnId.mnc`: Mobile Network Code (default: 93)
- `ue.msin`: MSIN (default: 0000000001)

## Authentication Keys

Make sure the authentication keys in `configs/ue.yaml` match the subscriber data in free5GC webconsole:

1. Open http://localhost:5000
2. Add/edit subscriber with IMSI 208930000000001
3. Set K and OPC to match the values in ue.yaml:
   - K: `8baf473f2f8fd09487cccbd7097c6867`
   - OPC: `8e27b6af0e692e750f32667a3b14605d`

## Command Line Flags

### RAN Flags

- `-config`: Path to RAN config file (default: configs/ran.yaml)
- `-ue-config`: Path to UE config file for authentication (default: configs/ue.yaml)
- `-imsi`: Override IMSI from config
- `-ue-n3-ip`: UE N3 IP for GTP-U (default: 127.0.0.100)

### UE Flags

- `-config`: Path to UE config file (default: configs/ue.yaml)
- `-ue-ip`: UE IP address (required, from PDU session)
- `-ran-addr`: Override RAN data plane address
- `-imsi`: Override IMSI from config
- `-tun`: Override TUN interface name

## Example: Full Workflow with Config

```bash
# 1. Setup network namespaces
sudo ./setup.sh

# 2. Make sure free5GC is running
cd ~/free5gc && ./run.sh

# 3. Build executables
go build -o /tmp/ntn_ran ./cmd/ran.go
go build -o /tmp/ntn_ue ./cmd/ue.go

# 4. Run RAN (it will read configs/ran.yaml and configs/ue.yaml)
sudo ip netns exec ns3 /tmp/ntn_ran

# 5. In another terminal, run UE (it will read configs/ue.yaml)
# Use the UE IP from the RAN output
sudo ip netns exec ue_ns /tmp/ntn_ue -ue-ip <UE_IP_FROM_RAN_OUTPUT>

# 6. Test connectivity
sudo ip netns exec ue_ns ping -I uetun0 8.8.8.8
```
