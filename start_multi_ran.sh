#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RAN_NS="ran_ns"
UE_NS="ue_ns"
RAN_BIN="$ROOT_DIR/bin/ntn_ran"
UE_BIN="$ROOT_DIR/bin/ntn_ue"
START_DELAY_SECONDS="${START_DELAY_SECONDS:-10}"
START_TIME="${START_TIME:-}"
LOG_DIR="${LOG_DIR:-/tmp/ntn-multi-ran-logs}"

usage() {
    echo "Usage: $0 [--start-time unix_ts] [--delay-seconds N] [--log-dir DIR]"
    echo "  --start-time     Absolute UNIX timestamp shared by all processes"
    echo "  --delay-seconds  Offset from now when --start-time is not provided (default: 10)"
    echo "  --log-dir        Directory for ran1.log, ran2.log, ue.log"
    exit 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --start-time)
            [ $# -ge 2 ] || usage
            START_TIME="$2"
            shift 2
            ;;
        --delay-seconds)
            [ $# -ge 2 ] || usage
            START_DELAY_SECONDS="$2"
            shift 2
            ;;
        --log-dir)
            [ $# -ge 2 ] || usage
            LOG_DIR="$2"
            shift 2
            ;;
        *)
            usage
            ;;
    esac
done

if [ ! -x "$RAN_BIN" ]; then
    echo "Missing executable: $RAN_BIN"
    echo "Build it with: go build -o bin/ntn_ran ./cmd/ran.go"
    exit 1
fi

if [ ! -x "$UE_BIN" ]; then
    echo "Missing executable: $UE_BIN"
    echo "Build it with: go build -o bin/ntn_ue ./cmd/ue.go"
    exit 1
fi

if [ -z "$START_TIME" ]; then
    START_TIME="$(awk -v now="$(date +%s.%N)" -v delay="$START_DELAY_SECONDS" 'BEGIN { printf "%.6f", now + delay }')"
fi

mkdir -p "$LOG_DIR"

echo "Shared timeline start: $START_TIME"
echo "Shared timeline start (local): $(date -d "@$START_TIME" '+%F %T.%6N %Z')"
echo "Shared timeline start (UTC): $(date -u -d "@$START_TIME" '+%F %T.%6N %Z')"
echo "Logs:"
echo "  $LOG_DIR/ran1.log"
echo "  $LOG_DIR/ran2.log"
echo "  $LOG_DIR/ue.log"

cleanup() {
    trap - INT TERM EXIT
    local pids=()
    [ -n "${RAN1_PID:-}" ] && pids+=("$RAN1_PID")
    [ -n "${RAN2_PID:-}" ] && pids+=("$RAN2_PID")
    [ -n "${UE_PID:-}" ] && pids+=("$UE_PID")
    if [ "${#pids[@]}" -gt 0 ]; then
        kill "${pids[@]}" 2>/dev/null || true
        wait "${pids[@]}" 2>/dev/null || true
    fi
}

trap cleanup INT TERM EXIT

sudo ip netns exec "$RAN_NS" "$RAN_BIN" \
    -config "$ROOT_DIR/configs/ran.yaml" \
    -xn-listen 10.0.1.2:9001 \
    -xn-peer 10.0.1.3:9002 \
    -start-time "$START_TIME" \
    > "$LOG_DIR/ran1.log" 2>&1 &
RAN1_PID=$!

sudo ip netns exec "$RAN_NS" "$RAN_BIN" \
    -config "$ROOT_DIR/configs/ran_2.yaml" \
    -xn-listen 10.0.1.3:9002 \
    -xn-peer 10.0.1.2:9001 \
    -start-time "$START_TIME" \
    > "$LOG_DIR/ran2.log" 2>&1 &
RAN2_PID=$!

sudo ip netns exec "$UE_NS" "$UE_BIN" \
    -config "$ROOT_DIR/configs/ue.yaml" \
    -start-time "$START_TIME" \
    > "$LOG_DIR/ue.log" 2>&1 &
UE_PID=$!

echo "Started processes:"
echo "  RAN-1 PID: $RAN1_PID"
echo "  RAN-2 PID: $RAN2_PID"
echo "  UE PID:    $UE_PID"
echo
echo "Follow logs with:"
echo "  tail -f $LOG_DIR/ran1.log $LOG_DIR/ran2.log $LOG_DIR/ue.log"

wait "$RAN1_PID" "$RAN2_PID" "$UE_PID"
