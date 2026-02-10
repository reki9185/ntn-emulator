#!/bin/bash
# Teardown 3-namespace NTN setup

set -e

echo "========================================="
echo "NTN 3-Namespace Teardown"
echo "========================================="

# Check if running as root
if [ "$EUID" -ne 0 ]; then 
    echo "❌ Please run as root (sudo)"
    exit 1
fi

UE_NS="ue_ns"
RAN_NS="ns3"
VETH_CP_HOST="veth0"

echo ""
echo "[Step 1] Killing processes in namespaces..."
# Kill any processes running in namespaces
ip netns pids $UE_NS 2>/dev/null | xargs -r kill -9 2>/dev/null || true
ip netns pids $RAN_NS 2>/dev/null | xargs -r kill -9 2>/dev/null || true
sleep 1

echo "[Step 2] Removing namespaces..."
ip netns del $UE_NS 2>/dev/null || true
ip netns del $RAN_NS 2>/dev/null || true

echo "[Step 3] Removing host routes..."
ip route del 10.0.2.0/24 2>/dev/null || true

echo "[Step 4] Cleaning iptables rules..."
# Remove NAT rules
iptables -t nat -D POSTROUTING -s 10.0.2.0/24 -j MASQUERADE 2>/dev/null || true

# Remove forwarding rules
iptables -D FORWARD -i $VETH_CP_HOST -o $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
iptables -D FORWARD -i $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
iptables -D FORWARD -o $VETH_CP_HOST -j ACCEPT 2>/dev/null || true

echo ""
echo "========================================="
echo "✅ Teardown Complete!"
echo "========================================="
echo ""
echo "Namespaces removed: $UE_NS, $RAN_NS"
echo "Veth pairs automatically deleted"
echo ""
