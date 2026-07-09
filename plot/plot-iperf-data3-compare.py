#!/usr/bin/env python3
"""
Compare iperf-data3 results for the original NTN channel model and the pdr=1.0
variant.

The original TCP run can get stuck after handover. This script detects that
post-handover failure from the TCP client trace and plots a trimmed option
against the pdr=1.0 option.
"""

from __future__ import annotations

import json
import os
import re
from dataclasses import dataclass
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt


ROOT = Path(__file__).resolve().parents[1]
DATA_DIR = ROOT / "plot" / "iperf-data3"
OUTPUT_DIR = ROOT / "plot" / "figure"
STATE_FILE = ROOT / "ntn_state.json"


@dataclass
class IperfSample:
    start: float
    end: float
    bitrate: float
    jitter: float | None = None
    lost: int | None = None
    total: int | None = None
    retries: int | None = None


def to_mbps(value: float, unit: str) -> float:
    if unit == "Gbits":
        return value * 1000.0
    if unit == "Kbits":
        return value / 1000.0
    if unit == "bits":
        return value / 1_000_000.0
    return value


def parse_iperf_file(path: Path) -> list[IperfSample]:
    samples: list[IperfSample] = []
    base_re = re.compile(
        r"\[\s*\d+\]\s+(\d+\.\d+)-(\d+\.\d+)\s+sec\s+"
        r".*?(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec"
    )
    udp_re = re.compile(r"\s+(\d+\.?\d*)\s*ms\s+(\d+)/\s*(\d+)")
    tcp_client_re = re.compile(r"\s+(\d+)/(\d+)\s+(\d+)\s+[-\d]+K/")

    for line in path.read_text().splitlines():
        match = base_re.search(line)
        if not match or "[SUM]" in line:
            continue

        start = float(match.group(1))
        end = float(match.group(2))

        # iperf2 summary lines look like normal intervals, but they span the
        # full test duration. Keep them out of the time series.
        if start == 0.0 and end - start > 1.0:
            continue

        bitrate = to_mbps(float(match.group(3)), match.group(4))
        tail = line[match.end() :]

        sample = IperfSample(start=start, end=end, bitrate=bitrate)
        udp_match = udp_re.search(tail)
        if udp_match:
            sample.jitter = float(udp_match.group(1))
            sample.lost = int(udp_match.group(2))
            sample.total = int(udp_match.group(3))

        tcp_client_match = tcp_client_re.search(tail)
        if tcp_client_match:
            sample.retries = int(tcp_client_match.group(3))

        samples.append(sample)

    return samples


def load_handover_window(path: Path) -> tuple[float, float]:
    state = json.loads(path.read_text())
    start = next(item["time"] for item in state if item.get("event") == "handover_start")
    end = next(item["time"] for item in state if item.get("event") == "handover_end")
    return float(start), float(end)


def moving_average(values: list[float], window: int) -> list[float]:
    if window <= 1:
        return values[:]

    averaged: list[float] = []
    total = 0.0
    queue: list[float] = []
    for value in values:
        total += value
        queue.append(value)
        if len(queue) > window:
            total -= queue.pop(0)
        averaged.append(total / len(queue))
    return averaged


def trim_until(samples: list[IperfSample], cutoff: float | None) -> list[IperfSample]:
    if cutoff is None:
        return samples
    return [sample for sample in samples if sample.end <= cutoff]


def first_long_zero_run(
    samples: list[IperfSample],
    after: float,
    min_duration: float = 0.5,
) -> float | None:
    zero_start: float | None = None
    zero_end: float | None = None

    for sample in samples:
        if sample.end < after:
            continue
        if sample.bitrate == 0.0:
            if zero_start is None:
                zero_start = sample.start
            zero_end = sample.end
            if zero_end - zero_start >= min_duration:
                return zero_start
        else:
            zero_start = None
            zero_end = None

    return None


def bitrate_stats(samples: list[IperfSample]) -> dict[str, float]:
    if not samples:
        return {"duration": 0.0, "avg": 0.0, "nonzero_avg": 0.0, "zero_time": 0.0}

    duration = samples[-1].end - samples[0].start
    avg = sum(sample.bitrate for sample in samples) / len(samples)
    nonzero = [sample.bitrate for sample in samples if sample.bitrate > 0.0]
    nonzero_avg = sum(nonzero) / len(nonzero) if nonzero else 0.0
    zero_time = sum(sample.end - sample.start for sample in samples if sample.bitrate == 0.0)
    return {
        "duration": duration,
        "avg": avg,
        "nonzero_avg": nonzero_avg,
        "zero_time": zero_time,
    }


def udp_loss_ratio(samples: list[IperfSample]) -> float:
    lost = sum(sample.lost or 0 for sample in samples)
    total = sum(sample.total or 0 for sample in samples)
    return (lost / total * 100.0) if total else 0.0


def total_retries(samples: list[IperfSample]) -> int:
    return sum(sample.retries or 0 for sample in samples)


def plot_series(
    ax: plt.Axes,
    samples: list[IperfSample],
    label: str,
    color: str,
    smooth_window: int = 50,
) -> None:
    if not samples:
        return

    times = [sample.end for sample in samples]
    bitrates = [sample.bitrate for sample in samples]
    ax.plot(times, bitrates, color=color, alpha=0.18, linewidth=0.7)
    ax.plot(
        times,
        moving_average(bitrates, smooth_window),
        label=label,
        color=color,
        linewidth=1.8,
    )


def shade_handover(ax: plt.Axes, handover_start: float, handover_end: float) -> None:
    ax.axvspan(handover_start, handover_end, color="#6b6b6b", alpha=0.18, label="Handover")
    ax.axvline(handover_start, color="#6b6b6b", linestyle="--", linewidth=0.9, alpha=0.7)
    ax.axvline(handover_end, color="#6b6b6b", linestyle=":", linewidth=0.9, alpha=0.7)


def setup_bitrate_axis(
    ax: plt.Axes,
    title: str,
    xlim: tuple[float, float] | None = None,
    ylim: tuple[float, float] | None = None,
    show_legend: bool = True,
) -> None:
    ax.set_title(title, fontsize=13, fontweight="bold")
    ax.set_xlabel("Time (sec)", fontsize=11, fontweight="bold")
    ax.set_ylabel("Bitrate (Mbits/sec)", fontsize=11, fontweight="bold")
    if ylim:
        ax.set_ylim(*ylim)
    else:
        ax.set_ylim(bottom=0)
    if xlim:
        ax.set_xlim(*xlim)
    ax.grid(True, alpha=0.28)
    if show_legend:
        ax.legend(loc="best", fontsize=9, framealpha=0.94)


def annotate_bars(ax: plt.Axes, bars, labels: list[str], fontsize: int = 10) -> None:
    for bar, label in zip(bars, labels):
        height = bar.get_height()
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            height,
            label,
            ha="center",
            va="bottom",
            fontsize=fontsize,
        )


def plot_option2_comparison(
    data: dict[str, list[IperfSample]],
    output_file: Path,
) -> None:
    tcp_server = data["tcp_pdr1_server"]
    udp_server = data["udp_pdr1_server"]
    tcp_client = data["tcp_pdr1_client"]

    fig = plt.figure(figsize=(16, 10))
    grid = fig.add_gridspec(2, 3, height_ratios=[1.6, 1.0], hspace=0.32, wspace=0.28)

    ax1 = fig.add_subplot(grid[0, :])
    plot_series(ax1, tcp_server, "TCP", "#c44e52")
    plot_series(ax1, udp_server, "UDP", "#4c72b0")
    setup_bitrate_axis(ax1, "Bitrate Comparison Over Time", (0, 30), (0, 25))
    ax1.legend(loc="upper right", fontsize=11, framealpha=0.95)

    ax2 = fig.add_subplot(grid[1, 0])
    loss_labels = ["TCP", "UDP"]
    loss_values = [0.0, udp_loss_ratio(udp_server)]
    loss_bars = ax2.bar(
        loss_labels,
        loss_values,
        color=["#c44e52", "#4c72b0"],
        alpha=0.75,
        edgecolor="black",
        width=0.5,
    )
    ax2.set_title("Packet Loss Ratio", fontsize=13, fontweight="bold")
    ax2.set_ylabel("Packet Loss (%)", fontsize=11, fontweight="bold")
    ax2.set_ylim(0, max(loss_values) * 1.22 if max(loss_values) > 0 else 1)
    ax2.grid(True, alpha=0.28, axis="y")
    annotate_bars(ax2, loss_bars, [f"{value:.2f}%" for value in loss_values], fontsize=11)

    ax3 = fig.add_subplot(grid[1, 1])
    retry_labels = ["TCP", "UDP"]
    retry_values = [total_retries(tcp_client), 0]
    retry_bars = ax3.bar(
        retry_labels,
        retry_values,
        color=["#c44e52", "#4c72b0"],
        alpha=0.75,
        edgecolor="black",
        width=0.5,
    )
    ax3.set_title("Retransmitted Packets", fontsize=13, fontweight="bold")
    ax3.set_ylabel("Retransmissions", fontsize=11, fontweight="bold")
    ax3.set_ylim(0, max(retry_values) * 1.22 if max(retry_values) > 0 else 1)
    ax3.grid(True, alpha=0.28, axis="y")
    annotate_bars(ax3, retry_bars, [str(value) for value in retry_values], fontsize=11)

    ax4 = fig.add_subplot(grid[1, 2])
    interruption_labels = ["TCP", "UDP"]
    interruption_values = [
        bitrate_stats(tcp_server)["zero_time"],
        bitrate_stats(udp_server)["zero_time"],
    ]
    interruption_bars = ax4.bar(
        interruption_labels,
        interruption_values,
        color=["#c44e52", "#4c72b0"],
        alpha=0.75,
        edgecolor="black",
        width=0.5,
    )
    ax4.set_title("Handover Interruption Time", fontsize=13, fontweight="bold")
    ax4.set_ylabel("Interruption Time (sec)", fontsize=11, fontweight="bold")
    ax4.set_ylim(0, max(interruption_values) * 1.22 if max(interruption_values) > 0 else 1)
    ax4.grid(True, alpha=0.28, axis="y")
    annotate_bars(ax4, interruption_bars, [f"{value:.3f}s" for value in interruption_values], fontsize=11)

    fig.subplots_adjust(top=0.92, bottom=0.08, hspace=0.34, wspace=0.28)
    output_file.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(output_file, dpi=300, bbox_inches="tight")
    plt.close(fig)


def plot_option2_zoom_single(
    samples: list[IperfSample],
    title: str,
    color: str,
    output_file: Path,
    ylim: tuple[float, float],
) -> None:
    fig, ax = plt.subplots(figsize=(12, 6))
    plot_series(ax, samples, title, color, smooth_window=20)
    setup_bitrate_axis(ax, title, (18.5, 21.5), ylim, show_legend=False)
    fig.tight_layout()
    output_file.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(output_file, dpi=300, bbox_inches="tight")
    plt.close(fig)


def plot_overview(
    data: dict[str, list[IperfSample]],
    tcp_cutoff: float | None,
    handover_start: float,
    handover_end: float,
    output_file: Path,
) -> None:
    original_tcp_trimmed = trim_until(data["tcp_original_server"], tcp_cutoff)
    original_udp_trimmed = trim_until(data["udp_original_server"], tcp_cutoff)

    fig = plt.figure(figsize=(18, 11))
    grid = fig.add_gridspec(3, 2, height_ratios=[1.25, 1.25, 1.0], hspace=0.36, wspace=0.24)

    ax1 = fig.add_subplot(grid[0, 0])
    shade_handover(ax1, handover_start, handover_end)
    plot_series(ax1, original_tcp_trimmed, "TCP, trimmed", "#c44e52")
    plot_series(ax1, original_udp_trimmed, "UDP, same time window", "#4c72b0")
    if tcp_cutoff:
        ax1.axvline(tcp_cutoff, color="#c44e52", linestyle="-.", linewidth=1.2, label="TCP trim point")
        ax1.text(
            0.02,
            0.92,
            f"HO: {handover_start:.3f}-{handover_end:.3f}s\nTrim: {tcp_cutoff:.3f}s",
            transform=ax1.transAxes,
            fontsize=9,
            va="top",
            bbox=dict(boxstyle="round", facecolor="white", alpha=0.82, edgecolor="#bdbdbd"),
        )
    setup_bitrate_axis(ax1, "Option 1: Original NTN Model, TCP Tail Trimmed", (0, 30), (0, 25))

    ax2 = fig.add_subplot(grid[0, 1])
    shade_handover(ax2, handover_start, handover_end)
    plot_series(ax2, data["tcp_pdr1_server"], "TCP, pdr=1.0 outside HO", "#c44e52")
    plot_series(ax2, data["udp_pdr1_server"], "UDP, pdr=1.0 outside HO", "#4c72b0")
    setup_bitrate_axis(ax2, "Option 2: pdr=1.0 Outside Handover", (0, 30), (0, 25))

    ax3 = fig.add_subplot(grid[1, :])
    shade_handover(ax3, handover_start, handover_end)
    plot_series(ax3, data["tcp_original_server"], "TCP original raw tail", "#9a9a9a")
    plot_series(ax3, original_tcp_trimmed, "TCP option 1 trimmed", "#c44e52")
    plot_series(ax3, data["tcp_pdr1_server"], "TCP option 2 pdr=1.0", "#2ca02c")
    if tcp_cutoff:
        ax3.axvline(tcp_cutoff, color="#c44e52", linestyle="-.", linewidth=1.2, label="Trim point")
    setup_bitrate_axis(ax3, "TCP Raw Tail vs Candidate Choices", (0, 47))

    ax4 = fig.add_subplot(grid[2, 0])
    labels = ["Opt1\nTCP", "Opt1\nUDP", "Opt2\nTCP", "Opt2\nUDP"]
    avg_values = [
        bitrate_stats(original_tcp_trimmed)["avg"],
        bitrate_stats(original_udp_trimmed)["avg"],
        bitrate_stats(data["tcp_pdr1_server"])["avg"],
        bitrate_stats(data["udp_pdr1_server"])["avg"],
    ]
    colors = ["#c44e52", "#4c72b0", "#c44e52", "#4c72b0"]
    bars = ax4.bar(labels, avg_values, color=colors, alpha=0.75, edgecolor="black", width=0.62)
    ax4.set_title("Average Receiver Bitrate", fontsize=13, fontweight="bold")
    ax4.set_ylabel("Mbits/sec", fontsize=11, fontweight="bold")
    ax4.grid(True, alpha=0.28, axis="y")
    ax4.set_ylim(bottom=0)
    for bar in bars:
        height = bar.get_height()
        ax4.text(bar.get_x() + bar.get_width() / 2, height, f"{height:.2f}", ha="center", va="bottom", fontsize=10)

    ax5 = fig.add_subplot(grid[2, 1])
    metric_labels = ["Opt1\nTCP zero", "Opt1\nUDP loss", "Opt2\nTCP zero", "Opt2\nUDP loss"]
    metric_values = [
        bitrate_stats(original_tcp_trimmed)["zero_time"],
        udp_loss_ratio(original_udp_trimmed),
        bitrate_stats(data["tcp_pdr1_server"])["zero_time"],
        udp_loss_ratio(data["udp_pdr1_server"]),
    ]
    metric_colors = ["#c44e52", "#4c72b0", "#c44e52", "#4c72b0"]
    bars = ax5.bar(metric_labels, metric_values, color=metric_colors, alpha=0.75, edgecolor="black", width=0.62)
    ax5.set_title("Interruption / UDP Loss", fontsize=13, fontweight="bold")
    ax5.set_ylabel("Seconds or percent", fontsize=11, fontweight="bold")
    ax5.grid(True, alpha=0.28, axis="y")
    ax5.set_ylim(bottom=0)
    for bar, value in zip(bars, metric_values):
        ax5.text(bar.get_x() + bar.get_width() / 2, value, f"{value:.2f}", ha="center", va="bottom", fontsize=10)

    fig.suptitle("iperf-data3: Original NTN Trim vs pdr=1.0 Variant", fontsize=16, fontweight="bold", y=0.965)
    fig.subplots_adjust(top=0.9, bottom=0.08, hspace=0.52, wspace=0.22)
    output_file.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(output_file, dpi=300, bbox_inches="tight")
    plt.close(fig)


def plot_handover_zoom(
    data: dict[str, list[IperfSample]],
    tcp_cutoff: float | None,
    handover_start: float,
    handover_end: float,
    output_file: Path,
) -> None:
    fig, axes = plt.subplots(2, 2, figsize=(16, 9), sharex=True, sharey=False)
    xlim = (18.5, 21.5)

    panels = [
        (axes[0, 0], data["tcp_original_server"], "TCP Original Model", "#c44e52"),
        (axes[0, 1], data["tcp_pdr1_server"], "TCP pdr=1.0 Variant", "#c44e52"),
        (axes[1, 0], data["udp_original_server"], "UDP Original Model", "#4c72b0"),
        (axes[1, 1], data["udp_pdr1_server"], "UDP pdr=1.0 Variant", "#4c72b0"),
    ]

    for ax, samples, title, color in panels:
        shade_handover(ax, handover_start, handover_end)
        plot_series(ax, samples, title, color, smooth_window=20)
        if tcp_cutoff and "TCP Original" in title:
            ax.axvline(tcp_cutoff, color="#c44e52", linestyle="-.", linewidth=1.1, label="Trim point")
        setup_bitrate_axis(ax, title, xlim, (0, 25))

    for ax in axes[0, :]:
        ax.set_xlabel("")

    fig.suptitle("Handover Zoom: 18.5-21.5 sec", fontsize=16, fontweight="bold", y=0.965)
    fig.subplots_adjust(top=0.88, bottom=0.08, hspace=0.36, wspace=0.16)
    output_file.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(output_file, dpi=300, bbox_inches="tight")
    plt.close(fig)


def print_summary(
    data: dict[str, list[IperfSample]],
    tcp_cutoff: float | None,
    handover_start: float,
    handover_end: float,
) -> None:
    original_tcp_trimmed = trim_until(data["tcp_original_server"], tcp_cutoff)
    original_udp_trimmed = trim_until(data["udp_original_server"], tcp_cutoff)
    original_tcp_client_trimmed = trim_until(data["tcp_original_client"], tcp_cutoff)
    original_udp_client_trimmed = trim_until(data["udp_original_client"], tcp_cutoff)
    rows = [
        ("Option 1 TCP trimmed", original_tcp_trimmed, original_tcp_client_trimmed),
        ("Option 1 UDP same window", original_udp_trimmed, original_udp_client_trimmed),
        ("Option 2 TCP pdr=1.0", data["tcp_pdr1_server"], data["tcp_pdr1_client"]),
        ("Option 2 UDP pdr=1.0", data["udp_pdr1_server"], data["udp_pdr1_client"]),
    ]

    print(f"Handover window: {handover_start:.6f}-{handover_end:.6f} sec")
    if tcp_cutoff:
        print(f"Detected TCP trim point: {tcp_cutoff:.4f} sec")
    print()
    print(f"{'Case':<26} {'Duration':>9} {'Avg Mbps':>9} {'Zero sec':>9} {'UDP loss %':>10} {'TCP Rtry':>9}")
    print("-" * 76)
    for label, server_samples, client_samples in rows:
        stats = bitrate_stats(server_samples)
        loss = udp_loss_ratio(server_samples)
        retries = total_retries(client_samples)
        print(
            f"{label:<26} {stats['duration']:>9.3f} {stats['avg']:>9.3f} "
            f"{stats['zero_time']:>9.3f} {loss:>10.3f} {retries:>9}"
        )


def main() -> None:
    handover_start, handover_end = load_handover_window(STATE_FILE)
    data = {
        "udp_original_server": parse_iperf_file(DATA_DIR / "server_rach.txt"),
        "tcp_original_server": parse_iperf_file(DATA_DIR / "server_rach_TCP.txt"),
        "udp_original_client": parse_iperf_file(DATA_DIR / "client_rach.txt"),
        "tcp_original_client": parse_iperf_file(DATA_DIR / "client_rach_TCP.txt"),
        "udp_pdr1_server": parse_iperf_file(DATA_DIR / "server_rach_UDP_pdr1.txt"),
        "tcp_pdr1_server": parse_iperf_file(DATA_DIR / "server_rach_TCP_pdr1.txt"),
        "udp_pdr1_client": parse_iperf_file(DATA_DIR / "client_rach_UDP_pdr1.txt"),
        "tcp_pdr1_client": parse_iperf_file(DATA_DIR / "client_rach_TCP_pdr1.txt"),
    }

    tcp_cutoff = first_long_zero_run(
        data["tcp_original_client"],
        after=handover_end,
        min_duration=0.5,
    )

    overview_file = OUTPUT_DIR / "iperf-data3-option-comparison-overview.png"
    zoom_file = OUTPUT_DIR / "iperf-data3-option-comparison-handover-zoom.png"
    option2_file = OUTPUT_DIR / "tcp-udp-comparison.png"
    option2_tcp_zoom_file = OUTPUT_DIR / "tcp-timeline.png"
    option2_udp_zoom_file = OUTPUT_DIR / "udp-timeline.png"
    plot_overview(data, tcp_cutoff, handover_start, handover_end, overview_file)
    plot_handover_zoom(data, tcp_cutoff, handover_start, handover_end, zoom_file)
    plot_option2_comparison(data, option2_file)
    plot_option2_zoom_single(
        data["tcp_pdr1_server"],
        "TCP Bitrate Timeline",
        "#c44e52",
        option2_tcp_zoom_file,
        (0, 25),
    )
    plot_option2_zoom_single(
        data["udp_pdr1_server"],
        "UDP Bitrate Timeline",
        "#4c72b0",
        option2_udp_zoom_file,
        (0, 25),
    )
    print_summary(data, tcp_cutoff, handover_start, handover_end)
    print()
    print(f"Saved: {os.path.relpath(overview_file, ROOT)}")
    print(f"Saved: {os.path.relpath(zoom_file, ROOT)}")
    print(f"Saved: {os.path.relpath(option2_file, ROOT)}")
    print(f"Saved: {os.path.relpath(option2_tcp_zoom_file, ROOT)}")
    print(f"Saved: {os.path.relpath(option2_udp_zoom_file, ROOT)}")


if __name__ == "__main__":
    main()
