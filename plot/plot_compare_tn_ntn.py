#!/usr/bin/env python3
"""
Compare TN (Terrestrial Network) vs NTN (Non-Terrestrial Network) iPerf Results

This script compares two iperf output files:
- tn-iperf-s.txt: Terrestrial network (normal)
- iperf-s.txt: Non-terrestrial network (satellite)

It generates three comparison plots:
1. Bitrate comparison
2. PDR (Packet Delivery Ratio) = 1 - lost/total
3. Packet loss ratio = lost/total

Time axis: 0-600s (starting from the line containing "9.01-10.04")
"""

import re
import matplotlib.pyplot as plt
import matplotlib.gridspec as gridspec
import numpy as np
from pathlib import Path


def parse_iperf_from_line(filename, start_time_min=9.0, duration=600):
    """
    Parse iperf file starting from a specific time.
    
    Args:
        filename: Path to iperf output file
        start_time_min: Minimum start time to begin parsing (default: 9.0s)
        duration: Duration in seconds to extract data
    
    Returns:
        List of (time_start, time_end, bitrate_mbps, lost, total) tuples
    """
    data = []
    started = False
    start_time_offset = None
    
    with open(filename, 'r') as f:
        for line in f:
            # Look for data lines with time intervals
            match = re.search(r'\[\s*\d+\]\s+(\d+\.?\d*)-(\d+\.?\d*)\s+sec', line)
            
            if match and not started:
                time_start = float(match.group(1))
                # Start when we reach the minimum start time
                if time_start >= start_time_min:
                    started = True
                    start_time_offset = time_start
            
            if not started:
                continue
            
            # Match UDP format with packet loss
            # Example: [  5]   9.01-10.04  sec   250 KBytes  1.98 Mbits/sec  1.634 ms  283/464 (61%)
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
                
                # Adjust time to start from 0
                adjusted_start = time_start - start_time_offset
                adjusted_end = time_end - start_time_offset
                
                # Only include data within duration
                if adjusted_start >= duration:
                    break
                
                data.append((adjusted_start, adjusted_end, bitrate, lost, total))
    
    return data


def plot_comparison(tn_file, ntn_file, output_dir, start_time_min=9.0, duration=600):
    """
    Create comparison plots for TN vs NTN.
    """
    print(f"📊 Comparing TN vs NTN iPerf Results\n")
    print(f"   TN file:  {tn_file}")
    print(f"   NTN file: {ntn_file}")
    print(f"   Start time: {start_time_min}s")
    print(f"   Duration: {duration}s\n")
    
    # Parse both files
    tn_data = parse_iperf_from_line(tn_file, start_time_min, duration)
    ntn_data = parse_iperf_from_line(ntn_file, start_time_min, duration)
    
    print(f"✓ Parsed TN data:  {len(tn_data)} intervals")
    print(f"✓ Parsed NTN data: {len(ntn_data)} intervals\n")
    
    if not tn_data or not ntn_data:
        print("❌ Error: No data found in one or both files")
        return
    
    # Extract data for plotting
    tn_times = [(ts + te) / 2 for ts, te, _, _, _ in tn_data]
    tn_bitrates = [br for _, _, br, _, _ in tn_data]
    tn_pdrs = [(1 - lost/total) * 100 if total > 0 else 100.0 
               for _, _, _, lost, total in tn_data]
    tn_loss_ratios = [(lost/total) * 100 if total > 0 else 0.0 
                      for _, _, _, lost, total in tn_data]
    
    ntn_times = [(ts + te) / 2 for ts, te, _, _, _ in ntn_data]
    ntn_bitrates = [br for _, _, br, _, _ in ntn_data]
    ntn_pdrs = [(1 - lost/total) * 100 if total > 0 else 100.0 
                for _, _, _, lost, total in ntn_data]
    ntn_loss_ratios = [(lost/total) * 100 if total > 0 else 0.0 
                       for _, _, _, lost, total in ntn_data]
    
    # Colors
    color_tn = '#4c72b0'   # Green for TN (terrestrial)
    color_ntn = '#c44e52'  # Red for NTN (satellite)
    
    # Create figure with 3 subplots
    fig = plt.figure(figsize=(15, 12))
    gs = gridspec.GridSpec(3, 1, hspace=0.3)
    
    # ============================================================
    # Plot 1: Bitrate Comparison
    # ============================================================
    ax1 = fig.add_subplot(gs[0])
    
    ax1.plot(tn_times, tn_bitrates, linestyle='-', linewidth=2,
            color=color_tn, label='TN (Terrestrial)', alpha=0.8)
    ax1.plot(ntn_times, ntn_bitrates, linestyle='-', linewidth=2,
            color=color_ntn, label='NTN (Satellite)', alpha=0.8)
    
    ax1.set_ylabel('Bitrate (Mbps)', fontsize=12, fontweight='bold')
    ax1.set_xlabel('Time (s)', fontsize=11)
    ax1.set_title('Bitrate Comparison: TN vs NTN', 
                 fontsize=14, fontweight='bold', pad=10)
    ax1.grid(True, alpha=0.3, linestyle='--')
    ax1.set_xlim(0, duration)
    ax1.legend(loc='upper right', fontsize=11)
    
    # ============================================================
    # Plot 2: PDR Comparison (1 - lost/total)
    # ============================================================
    ax2 = fig.add_subplot(gs[1])
    
    ax2.plot(tn_times, tn_pdrs, linestyle='-', linewidth=2,
            color=color_tn, label='TN (Terrestrial)', alpha=0.8)
    ax2.plot(ntn_times, ntn_pdrs, linestyle='-', linewidth=2,
            color=color_ntn, label='NTN (Satellite)', alpha=0.8)
    
    ax2.set_ylabel('PDR (%)', fontsize=12, fontweight='bold')
    ax2.set_xlabel('Time (s)', fontsize=11)
    ax2.set_title('Packet Delivery Ratio: TN vs NTN', 
                 fontsize=14, fontweight='bold', pad=10)
    ax2.grid(True, alpha=0.3, linestyle='--')
    ax2.set_xlim(0, duration)
    ax2.set_ylim(-5, 105)
    ax2.legend(loc='lower right', fontsize=11)
    
    # ============================================================
    # Plot 3: Packet Loss Ratio Comparison (lost/total)
    # ============================================================
    ax3 = fig.add_subplot(gs[2])
    
    ax3.plot(tn_times, tn_loss_ratios, linestyle='-', linewidth=2,
            color=color_tn, label='TN (Terrestrial)', alpha=0.8)
    ax3.plot(ntn_times, ntn_loss_ratios, linestyle='-', linewidth=2,
            color=color_ntn, label='NTN (Satellite)', alpha=0.8)
    
    ax3.set_ylabel('Packet Loss Ratio (%)', fontsize=12, fontweight='bold')
    ax3.set_xlabel('Time (s)', fontsize=11)
    ax3.set_title('Packet Loss Ratio: TN vs NTN', 
                 fontsize=14, fontweight='bold', pad=10)
    ax3.grid(True, alpha=0.3, linestyle='--')
    ax3.set_xlim(0, duration)
    ax3.set_ylim(-5, 105)
    ax3.legend(loc='upper right', fontsize=11)
    
    # Save figure
    plt.tight_layout()
    
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    output_file = output_dir / 'tn_vs_ntn_comparison.png'
    
    fig.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"✓ Comparison plot saved to: {output_file}")
    
    # Print statistics
    print("\n📈 Statistics Summary:")
    print(f"\nTN (Terrestrial Network):")
    print(f"  Average Bitrate:     {np.mean(tn_bitrates):.2f} Mbps")
    print(f"  Average PDR:         {np.mean(tn_pdrs):.2f}%")
    print(f"  Average Loss Ratio:  {np.mean(tn_loss_ratios):.2f}%")
    
    print(f"\nNTN (Non-Terrestrial Network):")
    print(f"  Average Bitrate:     {np.mean(ntn_bitrates):.2f} Mbps")
    print(f"  Average PDR:         {np.mean(ntn_pdrs):.2f}%")
    print(f"  Average Loss Ratio:  {np.mean(ntn_loss_ratios):.2f}%")
    
    plt.show()


def main():
    import argparse
    
    parser = argparse.ArgumentParser(
        description='Compare TN vs NTN iPerf Results',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Compare using default paths
  ./compare_tn_ntn.py
  
  # Specify custom paths
  ./compare_tn_ntn.py --tn-file path/to/tn-iperf-s.txt --ntn-file path/to/iperf-s.txt
  
  # Custom output directory
  ./compare_tn_ntn.py --output-dir custom_figures
  
  # Custom duration
  ./compare_tn_ntn.py --duration 300

Output:
  - Generates tn_vs_ntn_comparison.png with 3 comparison plots
  - Plots: Bitrate, PDR, and Packet Loss Ratio
        """
    )
    parser.add_argument('--tn-file', default='iperf-20260330/tn-iperf-s.txt',
                       help='Path to TN iperf file (default: iperf-20260330/tn-iperf-s.txt)')
    parser.add_argument('--ntn-file', default='iperf-20260330/iperf-s.txt',
                       help='Path to NTN iperf file (default: iperf-20260330/iperf-s.txt)')
    parser.add_argument('--output-dir', default='figure',
                       help='Output directory for plots (default: figure)')
    parser.add_argument('--start-time', type=float, default=9.0,
                       help='Start time in seconds (default: 9.0)')
    parser.add_argument('--duration', type=int, default=600,
                       help='Duration in seconds (default: 600)')
    
    args = parser.parse_args()
    
    tn_path = Path(args.tn_file)
    ntn_path = Path(args.ntn_file)
    
    # Check if files exist
    if not tn_path.exists():
        print(f"❌ Error: TN file not found: {tn_path}")
        return 1
    
    if not ntn_path.exists():
        print(f"❌ Error: NTN file not found: {ntn_path}")
        return 1
    
    plot_comparison(tn_path, ntn_path, args.output_dir, 
                   args.start_time, args.duration)
    
    return 0


if __name__ == '__main__':
    exit(main())
