#!/bin/bash
# 3-Namespace setup for NTN architecture:
# - UE Namespace (ue_ns): UE with uetun0 at 10.0.2.2
# - RAN Namespace (ran_ns): RAN acting as router between UE and Core
# - Host: free5GC (AMF, UPF, etc.)

# Configuration
UE_NS="ue_ns"
RAN_NS="ran_ns"

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

usage() {
    echo "Usage: $0 [ up | down | ran-ns | ue-ns ]"
    echo "  up     - Setup both RAN and UE namespaces"
    echo "  down   - Cleanup both namespaces"
    echo "  ran-ns - Enter RAN namespace"
    echo "  ue-ns  - Enter UE namespace"
    exit 1
}

setup_network_namespace() {
    # Remove existing network namespace
    echo "Removing existing network namespace..."
    cleanup_network_namespace
    echo

    # Create network namespace
    echo "Creating network namespace..."
    sudo ip netns add $RAN_NS 2>/dev/null || true
    sudo ip netns add $UE_NS 2>/dev/null || true
    echo

    # Create veth pairs
    echo "Creating veth pairs..."
    sudo ip link add $VETH_CP_HOST type veth peer name $VETH_CP_RAN
    sudo ip link add $VETH_UP_RAN type veth peer name $VETH_UP_UE
    echo

    # Move veth pairs to network namespace
    echo "Moving veth pairs to network namespace..."
    sudo ip link set $VETH_CP_RAN netns $RAN_NS
    sudo ip link set $VETH_UP_RAN netns $RAN_NS
    sudo ip link set $VETH_UP_UE netns $UE_NS
    echo

    # Bring up the interface in host namespace
    echo "Bring up the interface in host namespace..."
    sudo ip link set $VETH_CP_HOST up
    echo

    # Bring up the interface in RAN namespace
    echo "Bring up the interface in RAN namespace..."
    sudo ip netns exec $RAN_NS ip link set $VETH_CP_RAN up
    sudo ip netns exec $RAN_NS ip link set $VETH_UP_RAN up
    sudo ip netns exec $RAN_NS ip link set lo up
    echo

    # Bring up the interface in UE namespace
    echo "Bring up the interface in UE namespace..."
    sudo ip netns exec $UE_NS ip link set $VETH_UP_UE up
    sudo ip netns exec $UE_NS ip link set lo up
    echo

    # Set up IP address
    echo "Setting up IP address..."
    # Host: 10.0.1.1/24 network
    sudo ip addr add $CP_HOST_IP/$NETMASK dev $VETH_CP_HOST
    # RAN namespace: Control plane (10.0.1.2) and User plane (10.0.2.1)
    sudo ip netns exec $RAN_NS ip addr add $CP_RAN_IP/$NETMASK dev $VETH_CP_RAN
    sudo ip netns exec $RAN_NS ip addr add $UP_RAN_IP/$NETMASK dev $VETH_UP_RAN
    # UE namespace: 10.0.2.2/24
    sudo ip netns exec $UE_NS ip addr add $UP_UE_IP/$NETMASK dev $VETH_UP_UE
    echo

    # Set up default route
    echo "Setting up default route..."
    sudo ip netns exec $RAN_NS ip route add default via $CP_HOST_IP
    sudo ip netns exec $UE_NS ip route add default via $UP_RAN_IP
    echo

    # Enable IP forwarding in RAN (router between UE and Core)
    echo "Enabling IP forwarding in RAN namespace..."
    sudo ip netns exec $RAN_NS sysctl -w net.ipv4.ip_forward=1 > /dev/null
    echo

    # Set up routing on Host
    echo "Setting up routing on Host..."
    # Route to UE veth subnet via RAN
    sudo ip route add 10.0.2.0/24 via $CP_RAN_IP dev $VETH_CP_HOST 2>/dev/null || true
    # Note: Do NOT add route for 10.60.0.0/x - UPF handles this via upfgtp interface
    # Enable IP forwarding on host
    sudo sysctl -w net.ipv4.ip_forward=1 > /dev/null
    echo

    # Set up iptables for NAT (internet access from UE)
    echo "Setting up iptables for NAT..."
    # Allow forwarding
    sudo iptables -A FORWARD -i $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -o $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -s 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -d 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -i upfgtp -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -o upfgtp -j ACCEPT 2>/dev/null || true
    # NAT for internet access
    DEFAULT_IF=$(ip route | grep default | awk '{print $5}' | head -1)
    if [ -n "$DEFAULT_IF" ]; then
        echo "  NAT via interface: $DEFAULT_IF"
        sudo iptables -t nat -A POSTROUTING -s 10.0.2.0/24 -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -A POSTROUTING -s 10.60.0.0/16 -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -A POSTROUTING -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
    fi
    echo

    echo "$RAN_NS namespace setup complete"
    echo "$UE_NS namespace setup complete"
    echo "Network topology:"
    echo "  Host ($CP_HOST_IP) <---> RAN namespace ($CP_RAN_IP | $UP_RAN_IP) <---> UE namespace ($UP_UE_IP)"
}

cleanup_network_namespace() {
    echo "Removing network namespace..."

    # Bring down interface
    sudo ip link set $VETH_CP_HOST down 2>/dev/null || true

    # Delete veth pairs
    sudo ip link delete $VETH_CP_HOST 2>/dev/null || true
    sudo ip link delete $VETH_UP_RAN 2>/dev/null || true

    # Remove routes
    sudo ip route del 10.0.2.0/24 2>/dev/null || true
    # Note: Do NOT remove 10.60.0.0/x routes - those belong to UPF

    # Remove iptables rules
    DEFAULT_IF=$(ip route | grep default | awk '{print $5}' | head -1)
    if [ -n "$DEFAULT_IF" ]; then
        sudo iptables -t nat -D POSTROUTING -s 10.0.2.0/24 -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -D POSTROUTING -s 10.60.0.0/16 -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -D POSTROUTING -o $DEFAULT_IF -j MASQUERADE 2>/dev/null || true
    fi
    sudo iptables -D FORWARD -i $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -o $VETH_CP_HOST -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -s 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -d 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -i upfgtp -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -o upfgtp -j ACCEPT 2>/dev/null || true

    # Delete network namespace
    sudo ip netns delete $RAN_NS 2>/dev/null || true
    sudo ip netns delete $UE_NS 2>/dev/null || true

    echo "$RAN_NS namespace removed"
    echo "$UE_NS namespace removed"
}

main() {
    if [ $# -ne 1 ]; then
        usage
    fi

    case "$1" in
        "up")
            setup_network_namespace
        ;;
        "down")
            cleanup_network_namespace
        ;;
        "ran-ns")
            sudo ip netns exec $RAN_NS bash
        ;;
        "ue-ns")
            sudo ip netns exec $UE_NS bash
        ;;
        *)
            usage
        ;;
    esac
}

main "$@"
