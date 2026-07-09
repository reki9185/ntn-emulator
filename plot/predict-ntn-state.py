#!/usr/bin/env python3
"""
Predict iperf timelines for ntn_state.json by combining:

* no-handover UDP measurements from iperf-data2
* handover interruption shape from iperf-data
* PDR/delay events from ntn_state.json

The output is an estimated timeline, not a replay of measured iperf output.
"""

from __future__ import annotations

import csv
import json
import math
import os
import re
from pathlib import Path
from statistics import median

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
os.environ.setdefault("MPLBACKEND", "Agg")

import matplotlib.pyplot as plt


ROOT = Path(__file__).resolve().parents[1]
PLOT_DIR = ROOT / "plot"
LEGACY_DATA_DIR = PLOT_DIR / "iperf-data"
TIMELINE_DATA_DIR = PLOT_DIR / "iperf-data2"
PREDICTED_DATA_DIR = PLOT_DIR / "predicted-data"
FIGURE_DIR = PLOT_DIR / "figure"
STATE_FILE = ROOT / "ntn_state.json"
TIMELINE_FILE = ROOT / "ntn_timeline.json"

TCP_LOSS_EXPONENT = 1.7
UDP_DEFAULT_DATAGRAMS_PER_SEC = 443.0
DEFAULT_TCP_BURST_MBPS = 35.0
REFERENCE_HANDOVER_SECONDS = 5.0


def bitrate_to_mbps(value: float, unit: str) -> float:
    if unit == "Gbits":
        return value * 1000.0
    if unit == "Kbits":
        return value / 1000.0
    if unit == "bits":
        return value / 1_000_000.0
    return value


def parse_iperf_file(path: Path) -> list[dict]:
    records = []
    interval_re = re.compile(
        r"\[\s*\d+\]\s+"
        r"(\d+\.\d+)-(\d+\.\d+)\s+sec.*?"
        r"(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec"
        r"(?:\s+(\d+\.?\d*)\s*ms\s+(\d+)/(\d+))?"
        r"(?:\s+(\d+)\s+)?"
    )

    for line in path.read_text().splitlines():
        # Summary lines duplicate the full test duration and distort plotting.
        if "[SUM]" in line or " sender" in line or " receiver" in line:
            continue

        match = interval_re.search(line)
        if not match:
            continue

        start = float(match.group(1))
        end = float(match.group(2))
        bitrate = bitrate_to_mbps(float(match.group(3)), match.group(4))
        jitter = float(match.group(5)) if match.group(5) else None
        lost = int(match.group(6)) if match.group(6) else None
        total = int(match.group(7)) if match.group(7) else None
        retr = int(match.group(8)) if match.group(8) else None

        records.append(
            {
                "start": start,
                "end": end,
                "bitrate_mbps": bitrate,
                "jitter_ms": jitter,
                "lost": lost,
                "total": total,
                "retr": retr,
            }
        )

    return records


def robust_baseline(records: list[dict], fallback: float = 5.0) -> float:
    values = [r["bitrate_mbps"] for r in records if r["bitrate_mbps"] > 0.1]
    if not values:
        return fallback

    center = median(values)
    trimmed = [value for value in values if value <= center * 1.5]
    return median(trimmed or values)


def high_envelope(records: list[dict], fallback: float = 5.0) -> float:
    values = sorted(r["bitrate_mbps"] for r in records if r["bitrate_mbps"] > 0.1)
    if not values:
        return fallback

    index = min(len(values) - 1, max(0, int(round((len(values) - 1) * 0.90))))
    return values[index]


def load_json(path: Path) -> list[dict]:
    return json.loads(path.read_text())


def find_handover_windows(state: list[dict]) -> list[tuple[float, float]]:
    starts = []
    windows = []

    for item in sorted(state, key=lambda row: row["time"]):
        event = item.get("event")
        if event == "handover_start":
            starts.append(float(item["time"]))
        elif event == "handover_end" and starts:
            windows.append((starts.pop(0), float(item["time"])))

    return windows


def value_at(points: list[dict], key: str, t: float, default: float = 0.0) -> float:
    usable = [
        (float(item["time"]), float(item[key]))
        for item in points
        if key in item
    ]
    usable.sort()

    if not usable:
        return default
    if t <= usable[0][0]:
        return usable[0][1]
    if t >= usable[-1][0]:
        return usable[-1][1]

    for idx in range(1, len(usable)):
        prev_t, prev_value = usable[idx - 1]
        next_t, next_value = usable[idx]
        if prev_t <= t <= next_t:
            if math.isclose(prev_t, next_t):
                return next_value
            ratio = (t - prev_t) / (next_t - prev_t)
            return prev_value + (next_value - prev_value) * ratio

    return usable[-1][1]


def pdr_at(state: list[dict], handovers: list[tuple[float, float]], t: float) -> float:
    for start, end in handovers:
        if start <= t < end:
            return 0.0

    return max(0.0, min(1.0, value_at(state, "pdr", t, default=1.0)))


def average_pdr(state: list[dict], handovers: list[tuple[float, float]], start: float, end: float) -> float:
    if end <= start:
        return pdr_at(state, handovers, start)

    samples = 7
    total = 0.0
    for idx in range(samples):
        t = start + (idx + 0.5) * (end - start) / samples
        total += pdr_at(state, handovers, t)
    return total / samples


def make_intervals(state: list[dict], step: float = 0.1) -> list[tuple[float, float]]:
    end_time = max(float(item["time"]) for item in state)
    boundaries = {0.0, end_time}

    tick = 0.0
    while tick < end_time:
        boundaries.add(round(tick, 6))
        tick += step

    for item in state:
        boundaries.add(round(float(item["time"]), 6))

    sorted_boundaries = sorted(boundaries)
    return [
        (start, end)
        for start, end in zip(sorted_boundaries, sorted_boundaries[1:])
        if end - start > 1e-6
    ]


def estimate_datagram_rate(records: list[dict]) -> float:
    totals = [
        r["total"] / (r["end"] - r["start"])
        for r in records
        if r.get("total") is not None and r["end"] > r["start"] and r["total"] > 0
    ]
    if not totals:
        return UDP_DEFAULT_DATAGRAMS_PER_SEC
    return median(totals)


def measured_interruption(records: list[dict]) -> float:
    return sum(
        r["end"] - r["start"]
        for r in records
        if math.isclose(r["bitrate_mbps"], 0.0, abs_tol=1e-9)
    )


def measured_recovery_burst(records: list[dict], fallback: float = DEFAULT_TCP_BURST_MBPS) -> float:
    """Find the first non-zero TCP interval immediately after a zero-bitrate run."""
    saw_zero = False
    for record in records:
        if math.isclose(record["bitrate_mbps"], 0.0, abs_tol=1e-9):
            saw_zero = True
            continue
        if saw_zero:
            return record["bitrate_mbps"]

    return fallback


def measured_tcp_sender_retransmissions(path: Path) -> int:
    content = path.read_text()
    summary_match = re.search(
        r"\[\s*\d+\]\s+\d+\.\d+-\d+\.\d+\s+sec.*?"
        r"(?:Gbits|Mbits|Kbits|bits)/sec\s+(\d+)\s+sender",
        content,
    )
    if summary_match:
        return int(summary_match.group(1))

    return sum(
        record["retr"]
        for record in parse_iperf_file(path)
        if record.get("retr") is not None
    )


def is_first_post_handover_interval(start: float, end: float, handovers: list[tuple[float, float]]) -> bool:
    for _, handover_end in handovers:
        if math.isclose(start, handover_end, abs_tol=1e-6) and end > handover_end:
            return True
    return False


def predict_timelines() -> tuple[list[dict], list[dict], dict]:
    state = load_json(STATE_FILE)
    handovers = find_handover_windows(state)

    tcp_handover = parse_iperf_file(LEGACY_DATA_DIR / "tcp-s.txt")
    udp_handover = parse_iperf_file(LEGACY_DATA_DIR / "udp-s.txt")
    tcp_client_file = LEGACY_DATA_DIR / "tcp-c.txt"
    udp_client = parse_iperf_file(LEGACY_DATA_DIR / "udp-c.txt")
    udp_timeline = parse_iperf_file(TIMELINE_DATA_DIR / "iperf-s.txt")

    tcp_base = robust_baseline(tcp_handover, fallback=5.0)
    tcp_recovery_burst = measured_recovery_burst(tcp_handover)
    tcp_retransmissions = measured_tcp_sender_retransmissions(tcp_client_file)
    udp_base = max(
        robust_baseline(udp_client, fallback=5.0),
        high_envelope(udp_timeline, fallback=5.0),
    )
    udp_datagrams_per_sec = estimate_datagram_rate(udp_timeline)

    intervals = make_intervals(state)
    tcp_rows = []
    udp_rows = []
    tcp_retrans_assigned = False

    for start, end in intervals:
        duration = end - start
        midpoint = start + duration / 2.0
        pdr = average_pdr(state, handovers, start, end)
        delay = value_at(state, "delay_ue_ran", midpoint, default=0.0)

        udp_bitrate = udp_base * pdr
        total = max(0, int(round(udp_datagrams_per_sec * duration)))
        lost = max(0, min(total, int(round(total * (1.0 - pdr)))))
        jitter = 0.6 + delay * 0.8 + (1.0 - pdr) * 8.0

        # TCP is reliable at the application layer, but loss collapses goodput
        # faster than it does UDP receiver throughput. The exponent is an
        # explicit modeling assumption for the missing TCP/no-handover run.
        tcp_bitrate = tcp_base * (pdr ** TCP_LOSS_EXPONENT)
        if is_first_post_handover_interval(start, end, handovers):
            tcp_bitrate = tcp_recovery_burst

        retr = 0
        if is_first_post_handover_interval(start, end, handovers) and not tcp_retrans_assigned:
            retr = tcp_retransmissions
            tcp_retrans_assigned = True

        udp_rows.append(
            {
                "start": start,
                "end": end,
                "time": end,
                "bitrate_mbps": udp_bitrate,
                "pdr": pdr,
                "delay_ms": delay,
                "jitter_ms": jitter,
                "lost": lost,
                "total": total,
            }
        )
        tcp_rows.append(
            {
                "start": start,
                "end": end,
                "time": end,
                "bitrate_mbps": tcp_bitrate,
                "pdr": pdr,
                "delay_ms": delay,
                "retr": retr,
            }
        )

    actual_handover_seconds = sum(end - start for start, end in handovers)
    measured_tcp_zero_seconds = measured_interruption(tcp_handover)
    measured_udp_zero_seconds = measured_interruption(udp_handover)
    tcp_interruption_seconds = measured_tcp_zero_seconds / REFERENCE_HANDOVER_SECONDS * actual_handover_seconds
    udp_interruption_seconds = measured_udp_zero_seconds / REFERENCE_HANDOVER_SECONDS * actual_handover_seconds

    metadata = {
        "tcp_base_mbps": tcp_base,
        "tcp_recovery_burst_mbps": tcp_recovery_burst,
        "tcp_retransmissions": tcp_retransmissions,
        "udp_base_mbps": udp_base,
        "udp_datagrams_per_sec": udp_datagrams_per_sec,
        "handover_windows": handovers,
        "actual_handover_seconds": actual_handover_seconds,
        "reference_handover_seconds": REFERENCE_HANDOVER_SECONDS,
        "measured_tcp_zero_seconds": measured_tcp_zero_seconds,
        "measured_udp_zero_seconds": measured_udp_zero_seconds,
        "tcp_interruption_seconds": tcp_interruption_seconds,
        "udp_interruption_seconds": udp_interruption_seconds,
        "tcp_loss_exponent": TCP_LOSS_EXPONENT,
    }
    return tcp_rows, udp_rows, metadata


def write_csv(path: Path, rows: list[dict]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as file:
        writer = csv.DictWriter(file, fieldnames=list(rows[0].keys()))
        writer.writeheader()
        writer.writerows(rows)


def plot_single_timeline(rows: list[dict], metadata: dict, title: str, color: str, output: Path) -> None:
    times = [row["time"] for row in rows]
    bitrates = [row["bitrate_mbps"] for row in rows]
    pdr = [row["pdr"] * 100.0 for row in rows]

    fig, ax = plt.subplots(figsize=(14, 7))
    ax.plot(times, bitrates, color=color, linewidth=1.8, label="Predicted bitrate")
    ax.set_xlabel("Time (sec)", fontsize=13, fontweight="bold")
    ax.set_ylabel("Bitrate (Mbits/sec)", fontsize=13, fontweight="bold")
    ax.set_ylim(bottom=0)
    ax.set_title(title, fontsize=15, fontweight="bold")
    ax.grid(True, alpha=0.28)

    ax_pdr = ax.twinx()
    ax_pdr.plot(times, pdr, color="#555555", linewidth=1.0, alpha=0.35, label="PDR")
    ax_pdr.set_ylabel("PDR (%)", fontsize=12, fontweight="bold")
    ax_pdr.set_ylim(0, 105)

    lines, labels = ax.get_legend_handles_labels()
    pdr_lines, pdr_labels = ax_pdr.get_legend_handles_labels()
    ax.legend(lines + pdr_lines, labels + pdr_labels, loc="best", fontsize=11, framealpha=0.95)

    output.parent.mkdir(parents=True, exist_ok=True)
    fig.tight_layout()
    fig.savefig(output, dpi=300, bbox_inches="tight")
    plt.close(fig)


def plot_comparison(tcp_rows: list[dict], udp_rows: list[dict], metadata: dict, output: Path) -> None:
    fig = plt.figure(figsize=(15.5, 8.6))
    grid = fig.add_gridspec(2, 3, height_ratios=[1.3, 0.9])

    ax1 = fig.add_subplot(grid[0, :])
    ax1.plot(
        [row["time"] for row in tcp_rows],
        [row["bitrate_mbps"] for row in tcp_rows],
        color="#c44e52",
        linewidth=1.8,
        label="TCP",
    )
    ax1.plot(
        [row["time"] for row in udp_rows],
        [row["bitrate_mbps"] for row in udp_rows],
        color="#4c72b0",
        linewidth=1.8,
        label="UDP",
    )
    ax1.set_xlabel("Time (sec)", fontsize=13, fontweight="bold")
    ax1.set_ylabel("Bitrate (Mbits/sec)", fontsize=13, fontweight="bold")
    ax1.set_title("Bitrate Comparison Over Time", fontsize=15, fontweight="bold", pad=15)
    ax1.set_ylim(bottom=0)
    ax1.grid(True, alpha=0.28)
    ax1.legend(loc="best", fontsize=11, framealpha=0.95)

    ax2 = fig.add_subplot(grid[1, 0])
    udp_lost = sum(row["lost"] for row in udp_rows)
    udp_total = sum(row["total"] for row in udp_rows)
    udp_loss = udp_lost / udp_total * 100.0 if udp_total else 0.0
    bars = ax2.bar(["TCP", "UDP"], [0.0, udp_loss], color=["#c44e52", "#4c72b0"], alpha=0.75, edgecolor="black", width=0.5)
    ax2.set_ylabel("Packet Loss (%)", fontsize=12, fontweight="bold")
    ax2.set_title("Packet Loss Ratio", fontsize=13, fontweight="bold")
    ax2.grid(True, alpha=0.28, axis="y")
    ax2.set_ylim(bottom=0, top=max(1.0, udp_loss * 1.25))
    ax2.set_xlim(-0.55, 1.55)
    for bar in bars:
        height = bar.get_height()
        ax2.text(bar.get_x() + bar.get_width() / 2.0, height, f"{height:.2f}%", ha="center", va="bottom", fontsize=11)

    ax3 = fig.add_subplot(grid[1, 1])
    tcp_retr = sum(row["retr"] for row in tcp_rows)
    bars = ax3.bar(["TCP", "UDP"], [tcp_retr, 0], color=["#c44e52", "#4c72b0"], alpha=0.75, edgecolor="black", width=0.5)
    ax3.set_ylabel("Retransmissions", fontsize=12, fontweight="bold")
    ax3.set_title("Retransmitted Packets", fontsize=13, fontweight="bold")
    ax3.grid(True, alpha=0.28, axis="y")
    ax3.set_ylim(bottom=0, top=max(1.0, tcp_retr * 1.25))
    ax3.set_xlim(-0.55, 1.55)
    for bar in bars:
        height = bar.get_height()
        ax3.text(bar.get_x() + bar.get_width() / 2.0, height, f"{int(height)}", ha="center", va="bottom", fontsize=11)

    ax4 = fig.add_subplot(grid[1, 2])
    tcp_interruption = metadata["tcp_interruption_seconds"]
    udp_interruption = metadata["udp_interruption_seconds"]
    bars = ax4.bar(["TCP", "UDP"], [tcp_interruption, udp_interruption], color=["#c44e52", "#4c72b0"], alpha=0.75, edgecolor="black", width=0.5)
    ax4.set_ylabel("Interruption Time (sec)", fontsize=12, fontweight="bold")
    ax4.set_title("Handover Interruption Time", fontsize=13, fontweight="bold")
    ax4.grid(True, alpha=0.28, axis="y")
    ax4.set_ylim(bottom=0, top=max(0.1, max(tcp_interruption, udp_interruption) * 1.35))
    ax4.set_xlim(-0.55, 1.55)
    for bar in bars:
        height = bar.get_height()
        ax4.text(bar.get_x() + bar.get_width() / 2.0, height, f"{height:.3f}s", ha="center", va="bottom", fontsize=11)

    output.parent.mkdir(parents=True, exist_ok=True)
    fig.subplots_adjust(left=0.06, right=0.985, top=0.92, bottom=0.08, hspace=0.32, wspace=0.22)
    fig.savefig(output, dpi=300, bbox_inches="tight")
    plt.close(fig)


def main() -> None:
    tcp_rows, udp_rows, metadata = predict_timelines()

    PREDICTED_DATA_DIR.mkdir(parents=True, exist_ok=True)
    FIGURE_DIR.mkdir(parents=True, exist_ok=True)

    write_csv(PREDICTED_DATA_DIR / "tcp-predicted.csv", tcp_rows)
    write_csv(PREDICTED_DATA_DIR / "udp-predicted.csv", udp_rows)
    (PREDICTED_DATA_DIR / "prediction-summary.json").write_text(json.dumps(metadata, indent=2) + "\n")

    plot_single_timeline(
        udp_rows,
        metadata,
        "UDP Predicted Bitrate Timeline for ntn_state.json",
        "#4c72b0",
        FIGURE_DIR / "udp-timeline.png",
    )
    plot_single_timeline(
        tcp_rows,
        metadata,
        "TCP Predicted Bitrate Timeline for ntn_state.json",
        "#c44e52",
        FIGURE_DIR / "tcp-timeline.png",
    )
    plot_comparison(tcp_rows, udp_rows, metadata, FIGURE_DIR / "tcp-udp-comparison.png")

    print("Predicted data written to:")
    print(f"  {PREDICTED_DATA_DIR / 'tcp-predicted.csv'}")
    print(f"  {PREDICTED_DATA_DIR / 'udp-predicted.csv'}")
    print(f"  {PREDICTED_DATA_DIR / 'prediction-summary.json'}")
    print("Figures written to:")
    print(f"  {FIGURE_DIR / 'tcp-timeline.png'}")
    print(f"  {FIGURE_DIR / 'udp-timeline.png'}")
    print(f"  {FIGURE_DIR / 'tcp-udp-comparison.png'}")


if __name__ == "__main__":
    main()
