#!/bin/bash
# 3-Namespace setup for NTN architecture:
# - UE Namespace (ue_ns): UE with uetun0 at 10.0.2.2
# - RAN Namespace (ns3): RAN acting as router between UE and Core
# - Host: free5GC (AMF, UPF, etc.)

set -e

echo "========================================="
echo "NTN 3-Namespace Setup"
echo "========================================="

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "❌ Please run as root (sudo)"
    exit 1
fi

# Configuration
UE_NS="ue_ns"
RAN_NS="ns3"

# Control Plane Network (10.0.1.x): AMF <-> RAN <-> UPF
VETH_CP_HOST="veth0"    # Host side (AMF/UPF)
VETH_CP_RAN="veth1"     # RAN side
CP_HOST_IP="10.0.1.1"   # AMF N2 and UPF N3
CP_RAN_IP="10.0.1.2"    # RAN N2 and N3

# User Plane Network (10.0.2.x): UE <-> RAN
VETH_UP_RAN="veth2"     # RAN side
VETH_UP_UE="veth3"      # UE side
UP_RAN_IP="10.0.2.1"    # RAN Data Plane
UP_UE_IP="10.0.2.2"     # UE (will also have uetun0)

NETMASK="24"

echo ""
echo "[Step 1] Cleaning up existing namespaces (if any)..."
ip netns del $UE_NS 2>/dev/null || true
ip netns del $RAN_NS 2>/dev/null || true
ip link del $VETH_CP_HOST 2>/dev/null || true
ip link del $VETH_UP_RAN 2>/dev/null || true
sleep 1

echo "[Step 2] Creating namespaces: $UE_NS and $RAN_NS"
ip netns add $UE_NS
ip netns add $RAN_NS

echo "[Step 3] Creating Control Plane veth pair: $VETH_CP_HOST <-> $VETH_CP_RAN"
ip link add $VETH_CP_HOST type veth peer name $VETH_CP_RAN

echo "[Step 4] Creating User Plane veth pair: $VETH_UP_RAN <-> $VETH_UP_UE"
ip link add $VETH_UP_RAN type veth peer name $VETH_UP_UE

echo "[Step 5] Moving veth interfaces to namespaces"
ip link set $VETH_CP_RAN netns $RAN_NS
ip link set $VETH_UP_RAN netns $RAN_NS
ip link set $VETH_UP_UE netns $UE_NS

echo "[Step 6] Configuring Host side (Control Plane)"
ip addr add $CP_HOST_IP/$NETMASK dev $VETH_CP_HOST
ip link set $VETH_CP_HOST up

echo "[Step 7] Configuring RAN namespace (ns3)"
# Control Plane interface
ip netns exec $RAN_NS ip addr add $CP_RAN_IP/$NETMASK dev $VETH_CP_RAN
ip netns exec $RAN_NS ip link set $VETH_CP_RAN up

# User Plane interface
ip netns exec $RAN_NS ip addr add $UP_RAN_IP/$NETMASK dev $VETH_UP_RAN
ip netns exec $RAN_NS ip link set $VETH_UP_RAN up

# Loopback
ip netns exec $RAN_NS ip link set lo up

# Enable IP forwarding in RAN (router between UE and Core)
ip netns exec $RAN_NS sysctl -w net.ipv4.ip_forward=1 > /dev/null

echo "[Step 8] Configuring UE namespace (ue_ns)"
ip netns exec $UE_NS ip addr add $UP_UE_IP/$NETMASK dev $VETH_UP_UE
ip netns exec $UE_NS ip link set $VETH_UP_UE up
ip netns exec $UE_NS ip link set lo up

# Default route to RAN
ip netns exec $UE_NS ip route add default via $UP_RAN_IP dev $VETH_UP_UE

echo "[Step 9] Setting up routing on Host"
# Route to UE subnet via RAN
ip route add 10.0.2.0/24 via $CP_RAN_IP dev $VETH_CP_HOST 2>/dev/null || true

# Enable IP forwarding on host
echo 1 > /proc/sys/net/ipv4/ip_forward

echo "[Step 10] Setting up iptables for NAT (internet access from UE)"
# Allow forwarding
iptables -A FORWARD -i $VETH_CP_HOST -o $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
iptables -A FORWARD -i $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
iptables -A FORWARD -o $VETH_CP_HOST -j ACCEPT 2>/dev/null || true

# NAT for internet access (8.8.8.8, etc.)
# Find default interface
DEFAULT_IF=$(ip route | grep default | awk '{print $5}' | head -1)
if [ -n "$DEFAULT_IF" ]; then
    echo "   NAT via interface: $DEFAULT_IF"
    iptables -t nat -A POSTROUTING -s 10.0.2.0/24 -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
fi

echo ""
echo "========================================="
echo "✅ 3-Namespace Setup Complete!"
echo "========================================="
echo ""
echo "Network Architecture:"
echo "┌─────────────────────────────────────────────────────────────┐"
echo "│ UE Namespace (ue_ns)         │ RAN Namespace (ns3)         │ Host              │"
echo "│                              │                             │                   │"
echo "│ veth3: $UP_UE_IP          ├─┤ veth2: $UP_RAN_IP          │                   │"
echo "│                              │ veth1: $CP_RAN_IP          ├─┤ veth0: $CP_HOST_IP   │"
echo "│                              │                             │ AMF: $CP_HOST_IP     │"
echo "│                              │                             │ UPF: $CP_HOST_IP     │"
echo "└─────────────────────────────────────────────────────────────┘"
echo ""
echo "Next Steps:"
echo "1. Update ~/free5gc/config/upfcfg.yaml:"
echo "   gtpu.ifList[0].addr: $CP_HOST_IP"
echo ""
echo "2. Update ~/free5gc/config/amfcfg.yaml:"
echo "   ngapIpList: [$CP_HOST_IP]"
echo ""
echo "3. Restart free5GC:"
echo "   cd ~/free5gc && ./force_kill.sh && ./run.sh"
echo ""
echo "4. Build and run RAN in ns3:"
echo "   go build -o /tmp/ntn_ran cmd_ran.go"
echo "   sudo ip netns exec $RAN_NS /tmp/ntn_ran -imsi 208930000000001 -satellite NS-TEST"
echo ""
echo "5. Build and run UE in ue_ns:"
echo "   go build -o /tmp/ntn_ue cmd_ue.go"
echo "   sudo ip netns exec $UE_NS /tmp/ntn_ue -ue-ip <IP> -ran-addr $UP_RAN_IP:31414 -imsi 208930000000001"
echo ""
echo "6. Test ping from UE:"
echo "   sudo ip netns exec $UE_NS ping -I uetun0 8.8.8.8"
echo ""
