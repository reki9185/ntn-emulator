#!/usr/bin/env python3
"""
Comprehensive NTN Experiment Results Plotter

This script creates a multi-panel visualization of:
1. PDR from timeline (ue_log.txt)
2. Bitrate from iperf (iperf-s.txt)
3. PDR calculated from iperf (1 - lost/total)
4. SNR from channel model (ntn_channel_model.json)
5. Distance from channel model (ntn_channel_model.json)
6. Elevation from channel model (ntn_channel_model.json)

Time alignment: 
- iperf-s.txt: 9s in file = 0s in plot (offset -9s)
- experiment.json: 20s in file = 0s in plot (offset -20s)
- Time range: 0s to 600s on x-axis
- iperf actual file time: 9s to 609s
- experiment actual file time: 20s to 620s
"""

import json
import re
import matplotlib.pyplot as plt
import matplotlib.gridspec as gridspec
import numpy as np
from pathlib import Path


def parse_ue_log(filename):
    """
    Parse ue_log.txt and extract timeline PDR values.
    Returns: List of (time_s, pdr) tuples
    """
    data = []
    
    with open(filename, 'r') as f:
        for line in f:
            # Match timeline state update lines
            # Example: 📡 [Timeline] t=20.000s (abs: 22:03:11.623) state update — satellite=STARLINK-31077 UE-RAN=4.3ms RAN-5G=4.3ms PDR=0.636
            match = re.search(r't=(\d+\.?\d*)s.*PDR=(\d+\.?\d*)', line)
            if match:
                time_s = float(match.group(1))
                pdr = float(match.group(2))
                data.append((time_s, pdr))
    
    return data


def parse_iperf_file(filename):
    """
    Parse iperf output file and extract bitrate and packet loss data.
    Returns: List of (time_start, time_end, bitrate_mbps, lost, total) tuples
    """
    data = []
    
    with open(filename, 'r') as f:
        for line in f:
            # Match UDP format with packet loss
            # Example: [  5]   0.00-1.03   sec   565 KBytes  4.50 Mbits/sec  1.341 ms  0/410 (0%)
            match = re.search(
                r'\[\s*\d+\]\s+(\d+\.?\d*)-(\d+\.?\d*)\s+sec.*?(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec.*?(\d+)/(\d+)',
                line
            )
            
            if match and '[SUM]' not in line:
                time_start = float(match.group(1))
                time_end = float(match.group(2))
                bitrate = float(match.group(3))
                unit = match.group(4)
                lost = int(match.group(5))
                total = int(match.group(6))
                
                # Convert to Mbits/sec
                if unit == 'Gbits':
                    bitrate *= 1000
                elif unit == 'Kbits':
                    bitrate /= 1000
                elif unit == 'bits':
                    bitrate /= 1000000
                
                data.append((time_start, time_end, bitrate, lost, total))
    
    return data


def parse_channel_model(filename):
    """
    Parse ntn_channel_model.json and extract SNR, distance, elevation.
    Returns: Dict with 'time_points', 'snr', 'distance', 'elevation' arrays
    """
    with open(filename, 'r') as f:
        channel_data = json.load(f)
    
    data_points = channel_data['data']
    
    # Extract arrays
    time_points = []
    snr = []
    distance = []
    elevation = []
    
    for point in data_points:
        # Time point index (0-based), will be converted to actual time
        time_points.append(point['time_point'])
        snr.append(point['snr'])
        distance.append(point['distance'])
        elevation.append(point['elevation'])
    
    return {
        'time_points': np.array(time_points),
        'snr': np.array(snr),
        'distance': np.array(distance),
        'elevation': np.array(elevation),
        'window_size_s': channel_data.get('window_size_s', 10)
    }


def parse_experiment_json(filename):
    """
    Parse experiment.json to understand the timeline.
    Returns: List of (time_s, pdr) tuples
    """
    with open(filename, 'r') as f:
        exp_data = json.load(f)
    
    data = []
    for point in exp_data:
        time_s = float(point['time'])
        pdr = float(point['pdr'])
        data.append((time_s, pdr))
    
    return data


def plot_results(data_dir):
    """
    Create comprehensive multi-panel plot of all experiment results.
    """
    data_dir = Path(data_dir)
    
    # Parse all data files
    print("Loading data files...")
    ue_log_data = parse_ue_log(data_dir / 'ue_log.txt')
    iperf_data = parse_iperf_file(data_dir / 'iperf-s.txt')
    channel_data = parse_channel_model(data_dir / 'ntn_channel_model.json')
    
    # Also load experiment.json for reference (optional)
    try:
        exp_data = parse_experiment_json(data_dir / 'experiment.json')
        print(f"✓ Loaded experiment.json: {len(exp_data)} time points")
    except FileNotFoundError:
        exp_data = None
        print("⚠️  experiment.json not found, skipping")
    
    print(f"✓ Loaded ue_log.txt: {len(ue_log_data)} PDR updates")
    print(f"✓ Loaded iperf-s.txt: {len(iperf_data)} intervals")
    print(f"✓ Loaded ntn_channel_model.json: {len(channel_data['time_points'])} data points")
    
    # Time alignment settings:
    # - iperf-s.txt: 9s in file = 0s in plot (offset = -9s)
    # - experiment.json: 20s in file = 0s in plot (offset = -20s)
    # - Max plot time: 600s
    # - So iperf goes from 9s to 609s (file time), experiment from 20s to 620s (file time)
    IPERF_OFFSET = 9.0
    EXPERIMENT_OFFSET = 20.0
    MAX_TIME = 600.0
    
    # Filter and transform iperf data: keep data from 9s to 609s, then subtract 9s offset
    iperf_filtered = []
    for ts, te, br, l, tot in iperf_data:
        if IPERF_OFFSET <= ts <= (IPERF_OFFSET + MAX_TIME):
            # Apply time offset: subtract IPERF_OFFSET to align 9s -> 0s
            ts_aligned = ts - IPERF_OFFSET
            te_aligned = te - IPERF_OFFSET
            iperf_filtered.append((ts_aligned, te_aligned, br, l, tot))
    
    # Filter and transform UE log data: keep data from 20s to 620s, then subtract 20s offset
    ue_log_filtered = []
    for t, pdr in ue_log_data:
        if EXPERIMENT_OFFSET <= t <= (EXPERIMENT_OFFSET + MAX_TIME):
            # Apply time offset: subtract EXPERIMENT_OFFSET to align 20s -> 0s
            t_aligned = t - EXPERIMENT_OFFSET
            ue_log_filtered.append((t_aligned, pdr))
    
    # For channel model: time_point 0 = 20s in experiment file
    # After alignment: time_point 0 should map to 0s in plot (since exp 20s = 0s)
    window_size = channel_data['window_size_s']
    channel_times_all = channel_data['time_points'] * window_size
    
    # Filter channel model data to match time range (0 to 600s after alignment)
    channel_mask = channel_times_all <= MAX_TIME
    channel_times = channel_times_all[channel_mask]
    channel_snr = channel_data['snr'][channel_mask]
    channel_distance = channel_data['distance'][channel_mask]
    channel_elevation = channel_data['elevation'][channel_mask]
    
    print(f"\nAfter time alignment and filtering:")
    print(f"  iPerf: file time {IPERF_OFFSET}s-{IPERF_OFFSET + MAX_TIME}s -> plot time 0s-{MAX_TIME}s ({len(iperf_filtered)} intervals)")
    print(f"  Experiment: file time {EXPERIMENT_OFFSET}s-{EXPERIMENT_OFFSET + MAX_TIME}s -> plot time 0s-{MAX_TIME}s ({len(ue_log_filtered)} points)")
    print(f"  Channel model: {len(channel_times)} points")
    
    # Colors for consistency
    color_pdr = '#c44e52'
    color_bitrate = '#4c72b0'
    color_iperf_pdr_fig1 = '#55a868'  # Green for figure 1
    color_iperf_pdr_fig2 = '#e7b800'  # Yellow/Gold for figure 2
    color_snr = '#c44e52'
    color_distance = '#4c72b0'
    color_elevation = '#55a868'
    
    # ===========================================================================
    # FIGURE 1: Plots 1, 2, 3 (Timeline PDR, Bitrate, iPerf PDR)
    # ===========================================================================
    fig1 = plt.figure(figsize=(15, 10))
    gs1 = gridspec.GridSpec(3, 1, hspace=0.4)
    
    # ============================================================
    # Figure 1 - Plot 1: PDR from UE Log (Timeline)
    # ============================================================
    ax1_1 = fig1.add_subplot(gs1[0])
    
    if ue_log_filtered:
        times = [t for t, _ in ue_log_filtered]
        pdrs = [pdr * 100 for _, pdr in ue_log_filtered]  # Convert to percentage
        
        ax1_1.plot(times, pdrs, linestyle='-', linewidth=2, 
                color=color_pdr, label='Timeline PDR')
        ax1_1.set_ylabel('PDR (%)', fontsize=12, fontweight='bold')
        ax1_1.set_xlabel('Time (s)', fontsize=11)
        ax1_1.set_title('Packet Delivery Ratio from ns3', 
                     fontsize=13, fontweight='bold', pad=10)
        ax1_1.grid(True, alpha=0.3, linestyle='--')
        ax1_1.set_ylim(-5, 105)
        ax1_1.legend(loc='upper right')
    
    # ============================================================
    # Figure 1 - Plot 2: Bitrate from iPerf
    # ============================================================
    ax1_2 = fig1.add_subplot(gs1[1])
    
    if iperf_filtered:
        times_mid = [(ts + te) / 2 for ts, te, _, _, _ in iperf_filtered]
        bitrates = [br for _, _, br, _, _ in iperf_filtered]
        
        ax1_2.plot(times_mid, bitrates, linestyle='-', linewidth=2,
                color=color_bitrate, label='Bitrate')
        ax1_2.fill_between(times_mid, bitrates, alpha=0.3, color=color_bitrate)
        ax1_2.set_ylabel('Bitrate (Mbps)', fontsize=12, fontweight='bold')
        ax1_2.set_xlabel('Time (s)', fontsize=11)
        ax1_2.set_title('E2E throughput', 
                     fontsize=13, fontweight='bold', pad=10)
        ax1_2.grid(True, alpha=0.3, linestyle='--')
        ax1_2.set_ylim(0, max(bitrates) * 1.1)
        ax1_2.legend(loc='upper right')
    
    # ============================================================
    # Figure 1 - Plot 3: PDR calculated from iPerf (1 - lost/total)
    # ============================================================
    ax1_3 = fig1.add_subplot(gs1[2])
    
    if iperf_filtered:
        times_mid = [(ts + te) / 2 for ts, te, _, _, _ in iperf_filtered]
        iperf_pdrs = []
        
        for _, _, _, lost, total in iperf_filtered:
            if total > 0:
                pdr = (1 - lost / total) * 100  # As percentage
            else:
                pdr = 100.0
            iperf_pdrs.append(pdr)
        
        ax1_3.plot(times_mid, iperf_pdrs, linestyle='-', linewidth=2,
                color=color_iperf_pdr_fig1, label='iPerf PDR (1-lost/total)')
        ax1_3.set_ylabel('PDR (%)', fontsize=12, fontweight='bold')
        ax1_3.set_xlabel('Time (s)', fontsize=11)
        ax1_3.set_title('Packet Delivery Ratio from iPerf', 
                     fontsize=13, fontweight='bold', pad=10)
        ax1_3.grid(True, alpha=0.3, linestyle='--')
        ax1_3.set_ylim(-5, 105)
        ax1_3.legend(loc='upper right')
    
    # Adjust layout and save Figure 1
    plt.tight_layout(rect=[0, 0, 1, 0.99])
    
    output_file1 = data_dir / 'experiment_results_fig1.png'
    fig1.savefig(output_file1, dpi=300, bbox_inches='tight')
    print(f"\n✓ Figure 1 saved to: {output_file1}")
    
    # ===========================================================================
    # FIGURE 2: Plots 4, 5 (SNR, Distance)
    # ===========================================================================
    fig2 = plt.figure(figsize=(15, 8))
    gs2 = gridspec.GridSpec(2, 1, hspace=0.35)
    
    # ============================================================
    # Figure 2 - Plot 1: SNR from Channel Model
    # ============================================================
    ax2_4 = fig2.add_subplot(gs2[0])
    
    ax2_4.plot(channel_times, channel_snr, linestyle='-',
            linewidth=2, color=color_snr, label='SNR')
    ax2_4.set_ylabel('SNR (dB)', fontsize=12, fontweight='bold')
    ax2_4.set_xlabel('Time (s)', fontsize=11)
    ax2_4.set_title('Signal-to-Noise Ratio', 
                 fontsize=13, fontweight='bold', pad=10)
    ax2_4.grid(True, alpha=0.3, linestyle='--')
    ax2_4.axhline(y=0, color='red', linestyle='--', alpha=0.5, linewidth=1)
    ax2_4.legend(loc='upper right')
    
    # ============================================================
    # Figure 2 - Plot 2: Distance from Channel Model
    # ============================================================
    ax2_5 = fig2.add_subplot(gs2[1])
    
    ax2_5.plot(channel_times, channel_distance, linestyle='-',
            linewidth=2, color=color_distance, label='Distance')
    ax2_5.set_ylabel('Distance (km)', fontsize=12, fontweight='bold')
    ax2_5.set_xlabel('Time (s)', fontsize=11)
    ax2_5.set_title('Satellite-UE Distance', 
                 fontsize=13, fontweight='bold', pad=10)
    ax2_5.grid(True, alpha=0.3, linestyle='--')
    ax2_5.legend(loc='lower right')
    
    # Adjust layout and save Figure 2
    plt.tight_layout(rect=[0, 0, 1, 0.99])
    
    output_file2 = data_dir / 'experiment_results_fig2.png'
    fig2.savefig(output_file2, dpi=300, bbox_inches='tight')
    print(f"\n✓ Figure 2 saved to: {output_file2}")
    
    # ===========================================================================
    # FIGURE 3: Combined PDR plot (ns3 vs iPerf)
    # ===========================================================================
    fig3 = plt.figure(figsize=(15, 6))
    ax3 = fig3.add_subplot(111)
    
    # Plot ns3 PDR (ideal) with dashed line
    if ue_log_filtered:
        times = [t for t, _ in ue_log_filtered]
        pdrs = [pdr * 100 for _, pdr in ue_log_filtered]
        
        ax3.plot(times, pdrs, linestyle='--', linewidth=2.5, 
                color='#c44e52', label='ideal', alpha=0.8)
    
    # Plot iPerf PDR (real) with solid line
    if iperf_filtered:
        times_mid = [(ts + te) / 2 for ts, te, _, _, _ in iperf_filtered]
        iperf_pdrs = []
        
        for _, _, _, lost, total in iperf_filtered:
            if total > 0:
                pdr = (1 - lost / total) * 100
            else:
                pdr = 100.0
            iperf_pdrs.append(pdr)
        
        ax3.plot(times_mid, iperf_pdrs, linestyle='-', linewidth=2.5,
                color='#4c72b0', label='real', alpha=0.8)
    
    ax3.set_ylabel('PDR (%)', fontsize=12, fontweight='bold')
    ax3.set_xlabel('Time (s)', fontsize=11)
    ax3.set_title('Packet Delivery Ratio Comparison (ns3 vs iPerf)', 
                 fontsize=13, fontweight='bold', pad=10)
    ax3.grid(True, alpha=0.3, linestyle='--')
    ax3.set_ylim(-5, 105)
    ax3.legend(loc='upper right', fontsize=11)
    
    plt.tight_layout()
    
    output_file3 = data_dir / 'experiment_results_fig3.png'
    fig3.savefig(output_file3, dpi=300, bbox_inches='tight')
    print(f"\n✓ Figure 3 saved to: {output_file3}")
    
    plt.show()


def main():
    import argparse
    
    parser = argparse.ArgumentParser(
        description='Plot comprehensive NTN experiment results',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Plot results from specific directory
  ./plot_ntn_results.py plot/iperf-20260330
  
  # Plot results from current directory
  ./plot_ntn_results.py .

Required files in data directory:
  - ue_log.txt (timeline PDR updates)
  - iperf-s.txt (iperf server output)
  - ntn_channel_model.json (channel characteristics)
  - experiment.json (optional, for reference)

Time alignment:
  - iperf-s.txt: 9s in file = 0s in plot (offset -9s)
  - experiment.json: 20s in file = 0s in plot (offset -20s)
  - X-axis displays: 0s to 600s
  - iperf file range: 9s to 609s
  - experiment file range: 20s to 620s
        """
    )
    parser.add_argument('data_dir', nargs='?', default='iperf-20260330',
                       help='Directory containing experiment data files')
    
    args = parser.parse_args()
    
    data_path = Path(args.data_dir)
    if not data_path.exists():
        print(f"❌ Error: Directory not found: {data_path}")
        return 1
    
    # Check required files
    required_files = ['ue_log.txt', 'iperf-s.txt', 'ntn_channel_model.json']
    missing_files = [f for f in required_files if not (data_path / f).exists()]
    
    if missing_files:
        print(f"❌ Error: Missing required files in {data_path}:")
        for f in missing_files:
            print(f"   - {f}")
        return 1
    
    print(f"📊 Plotting results from: {data_path}\n")
    plot_results(data_path)
    
    return 0


if __name__ == '__main__':
    exit(main())
