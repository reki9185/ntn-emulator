#!/bin/bash

UE_NS="ue_ns"
RAN_NS="ran_ns"

VETH_CP_HOST="veth0"
VETH_CP_RAN="veth1"
CP_HOST_IP="10.0.1.1"
CP_RAN_IP="10.0.1.2"

VETH_UP_RAN="veth2"
VETH_UP_UE="veth3"
UP_RAN_IP="10.0.2.1"
UP_UE_IP="10.0.2.2"

NETMASK="24"

usage() {
    echo "Usage: $0 [ up | down | ran-ns | ue-ns ]"
    exit 1
}

delete_if_exists() {
    local dev=$1
    if ip link show "$dev" &>/dev/null; then
        echo "Deleting device: $dev"
        sudo ip link delete "$dev" 2>/dev/null || true
    fi
}

cleanup_network_namespace() {
    echo "Cleaning old NTN namespace topology..."

    sudo pkill -9 ntn_ran 2>/dev/null || true
    sudo pkill -9 ntn_ue 2>/dev/null || true

    sudo ip netns delete "$RAN_NS" 2>/dev/null || true
    sudo ip netns delete "$UE_NS" 2>/dev/null || true

    delete_if_exists "$VETH_CP_HOST"
    delete_if_exists "$VETH_CP_RAN"
    delete_if_exists "$VETH_UP_RAN"
    delete_if_exists "$VETH_UP_UE"

    sudo ip route del 10.0.2.0/24 2>/dev/null || true

    DEFAULT_IF=$(ip route | awk '/^default/ {print $5; exit}')

    sudo iptables -D FORWARD -i "$VETH_CP_HOST" -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -o "$VETH_CP_HOST" -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -s 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -d 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -i upfgtp -j ACCEPT 2>/dev/null || true
    sudo iptables -D FORWARD -o upfgtp -j ACCEPT 2>/dev/null || true
    sudo ip route del 10.0.1.2/32 2>/dev/null || true

    if [ -n "$DEFAULT_IF" ]; then
        sudo iptables -t nat -D POSTROUTING -s 10.0.2.0/24 -o "$DEFAULT_IF" -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -D POSTROUTING -s 10.60.0.0/16 -o "$DEFAULT_IF" -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -D POSTROUTING -o "$DEFAULT_IF" -j MASQUERADE 2>/dev/null || true
    fi

    echo "Cleanup done."
}

setup_network_namespace() {
    cleanup_network_namespace
    echo

    echo "Creating namespaces..."
    sudo ip netns add "$RAN_NS"
    sudo ip netns add "$UE_NS"

    echo "Creating veth pairs..."
    sudo ip link add "$VETH_CP_HOST" type veth peer name "$VETH_CP_RAN"
    sudo ip link add "$VETH_UP_RAN" type veth peer name "$VETH_UP_UE"

    echo "Moving veth interfaces..."
    sudo ip link set "$VETH_CP_RAN" netns "$RAN_NS"
    sudo ip link set "$VETH_UP_RAN" netns "$RAN_NS"
    sudo ip link set "$VETH_UP_UE" netns "$UE_NS"

    echo "Assigning IP addresses..."
    sudo ip addr add "$CP_HOST_IP/$NETMASK" dev "$VETH_CP_HOST"
    sudo ip netns exec "$RAN_NS" ip addr add "$CP_RAN_IP/$NETMASK" dev "$VETH_CP_RAN"
    sudo ip netns exec "$RAN_NS" ip addr add "$UP_RAN_IP/$NETMASK" dev "$VETH_UP_RAN"
    sudo ip netns exec "$UE_NS" ip addr add "$UP_UE_IP/$NETMASK" dev "$VETH_UP_UE"

    echo "Bringing interfaces up..."
    sudo ip link set "$VETH_CP_HOST" up

    sudo ip netns exec "$RAN_NS" ip link set lo up
    sudo ip netns exec "$RAN_NS" ip link set "$VETH_CP_RAN" up
    sudo ip netns exec "$RAN_NS" ip link set "$VETH_UP_RAN" up

    sudo ip netns exec "$UE_NS" ip link set lo up
    sudo ip netns exec "$UE_NS" ip link set "$VETH_UP_UE" up

    echo "Setting routes..."
    sudo ip netns exec "$RAN_NS" ip route add default via "$CP_HOST_IP"
    sudo ip netns exec "$UE_NS" ip route add default via "$UP_RAN_IP"
    sudo ip route add 10.0.2.0/24 via "$CP_RAN_IP" dev "$VETH_CP_HOST" 2>/dev/null || true

    echo "Enabling forwarding..."
    sudo sysctl -w net.ipv4.ip_forward=1 >/dev/null
    sudo ip netns exec "$RAN_NS" sysctl -w net.ipv4.ip_forward=1 >/dev/null

    sudo sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null
    sudo sysctl -w net.ipv4.conf.default.rp_filter=0 >/dev/null
    sudo sysctl -w net.ipv4.conf."$VETH_CP_HOST".rp_filter=0 >/dev/null 2>&1 || true

    echo "Setting iptables..."
    sudo iptables -A FORWARD -i "$VETH_CP_HOST" -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -o "$VETH_CP_HOST" -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -s 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -d 10.60.0.0/16 -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -i upfgtp -j ACCEPT 2>/dev/null || true
    sudo iptables -A FORWARD -o upfgtp -j ACCEPT 2>/dev/null || true
    sudo ip route replace 10.0.1.2/32 dev $VETH_CP_HOST src $CP_HOST_IP

    DEFAULT_IF=$(ip route | awk '/^default/ {print $5; exit}')
    if [ -n "$DEFAULT_IF" ]; then
        echo "NAT via interface: $DEFAULT_IF"
        sudo iptables -t nat -A POSTROUTING -s 10.0.2.0/24 -o "$DEFAULT_IF" -j MASQUERADE 2>/dev/null || true
        sudo iptables -t nat -A POSTROUTING -s 10.60.0.0/16 -o "$DEFAULT_IF" -j MASQUERADE 2>/dev/null || true
    fi

    echo
    echo "Setup complete."
    echo "Host:   $CP_HOST_IP on $VETH_CP_HOST"
    echo "RAN:    $CP_RAN_IP on $VETH_CP_RAN, $UP_RAN_IP on $VETH_UP_RAN"
    echo "UE:     $UP_UE_IP on $VETH_UP_UE"
    echo
    echo "Test:"
    echo "  sudo ip netns exec $RAN_NS ping $CP_HOST_IP"
}

main() {
    if [ $# -ne 1 ]; then
        usage
    fi

    case "$1" in
        up)
            setup_network_namespace
            ;;
        down)
            cleanup_network_namespace
            ;;
        ran-ns)
            sudo ip netns exec "$RAN_NS" bash
            ;;
        ue-ns)
            sudo ip netns exec "$UE_NS" bash
            ;;
        *)
            usage
            ;;
    esac
}

main "$@"