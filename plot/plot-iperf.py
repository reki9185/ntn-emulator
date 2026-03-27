#!/usr/bin/env python3
"""
iPerf Data Timeline Plot
Plots TCP and UDP bitrate over time from iperf3 output files.
Creates comprehensive comparison with 4 subplots.
"""

import matplotlib.pyplot as plt
import re
import os


def parse_iperf_file(filename):
    """
    Parse iperf output file and extract time intervals and bitrates.
    Returns: List of (time_start, time_end, bitrate_mbps, jitter_ms, lost, total, retr) tuples
    """
    data = []
    
    with open(filename, 'r') as f:
        for line in f:
            # Match lines with interval data (format: [ID] start-end sec Transfer Bitrate ...)
            # Example: [  5]   0.00-1.00   sec   365 KBytes  2.99 Mbits/sec
            # Also match: [  5]  14.00-15.00  sec  0.00 Bytes  0.00 bits/sec (for zero bitrate)
            # UDP: [  5]   0.00-1.00   sec   581 KBytes  4.75 Mbits/sec  0.912 ms  0/421 (0%)
            # TCP: [  5]   0.00-0.10   sec   109 KBytes  8.91 Mbits/sec    0   57.9 KBytes (with Retr)
            
            # First try to match UDP format (with jitter and loss)
            udp_match = re.search(
                r'\[\s*\d+\]\s+(\d+\.\d+)-(\d+\.\d+)\s+sec.*?(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec\s+(\d+\.?\d*)\s*ms\s+(\d+)/(\d+)',
                line
            )
            
            if udp_match and '[SUM]' not in line:
                time_start = float(udp_match.group(1))
                time_end = float(udp_match.group(2))
                bitrate = float(udp_match.group(3))
                unit = udp_match.group(4)
                jitter = float(udp_match.group(5))
                lost = int(udp_match.group(6))
                total = int(udp_match.group(7))
                
                # Convert to Mbits/sec
                if unit == 'Gbits':
                    bitrate *= 1000
                elif unit == 'Kbits':
                    bitrate /= 1000
                elif unit == 'bits':
                    bitrate /= 1000000
                
                data.append((time_start, time_end, bitrate, jitter, lost, total, None))
                continue
            
            # Try TCP format with Retr (no jitter, no loss, but has retransmission)
            tcp_retr_match = re.search(
                r'\[\s*\d+\]\s+(\d+\.\d+)-(\d+\.\d+)\s+sec.*?(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec\s+(\d+)\s+',
                line
            )
            
            if tcp_retr_match and '[SUM]' not in line and 'Retr' not in line:
                time_start = float(tcp_retr_match.group(1))
                time_end = float(tcp_retr_match.group(2))
                bitrate = float(tcp_retr_match.group(3))
                unit = tcp_retr_match.group(4)
                retr = int(tcp_retr_match.group(5))
                
                # Convert to Mbits/sec
                if unit == 'Gbits':
                    bitrate *= 1000
                elif unit == 'Kbits':
                    bitrate /= 1000
                elif unit == 'bits':
                    bitrate /= 1000000
                
                data.append((time_start, time_end, bitrate, None, None, None, retr))
                continue
            
            # Try simple TCP format (no jitter, no loss, no retr)
            tcp_match = re.search(
                r'\[\s*\d+\]\s+(\d+\.\d+)-(\d+\.\d+)\s+sec.*?(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec',
                line
            )
            
            if tcp_match and '[SUM]' not in line:
                time_start = float(tcp_match.group(1))
                time_end = float(tcp_match.group(2))
                bitrate = float(tcp_match.group(3))
                unit = tcp_match.group(4)
                
                # Convert to Mbits/sec
                if unit == 'Gbits':
                    bitrate *= 1000
                elif unit == 'Kbits':
                    bitrate /= 1000
                elif unit == 'bits':
                    bitrate /= 1000000
                
                data.append((time_start, time_end, bitrate, None, None, None, None))
    
    return data


def parse_iperf_summary(filename):
    """
    Parse iperf summary line to get overall statistics.
    Returns: (bitrate, jitter, lost, total, is_sender) or None
    """
    with open(filename, 'r') as f:
        content = f.read()
        
        # Look for summary lines (sender/receiver)
        # Example: [  5]   0.00-30.00  sec  17.9 MBytes  5.00 Mbits/sec  0.000 ms  0/13279 (0%)  sender
        summary_match = re.search(
            r'\[\s*\d+\]\s+\d+\.\d+-\d+\.\d+\s+sec.*?(\d+\.?\d*)\s+(Gbits|Mbits|Kbits|bits)/sec\s+(\d+\.?\d*)\s*ms\s+(\d+)/(\d+).*?(sender|receiver)',
            content
        )
        
        if summary_match:
            bitrate = float(summary_match.group(1))
            unit = summary_match.group(2)
            jitter = float(summary_match.group(3))
            lost = int(summary_match.group(4))
            total = int(summary_match.group(5))
            role = summary_match.group(6)
            
            # Convert to Mbits/sec
            if unit == 'Gbits':
                bitrate *= 1000
            elif unit == 'Kbits':
                bitrate /= 1000
            elif unit == 'bits':
                bitrate /= 1000000
            
            return (bitrate, jitter, lost, total, role == 'sender')
        
    return None


def calculate_interruption_time(data):
    """Calculate total interruption time where bitrate is 0."""
    interruption = 0
    for item in data:
        time_start, time_end, bitrate = item[0], item[1], item[2]
        if bitrate == 0:
            interruption += (time_end - time_start)
    return interruption


def plot_timeline(data, title, output_file, color='#4c72b0'):
    """Plot bitrate timeline for a single protocol."""
    if not data:
        print(f"  ⚠ No data to plot for {title}")
        return
    
    times = [item[1] for item in data]  # Use end time
    bitrates = [item[2] for item in data]
    
    fig, ax = plt.subplots(figsize=(14, 7))
    
    # Simple line plot with markers - show all data points including zeros
    ax.plot(times, bitrates, linewidth=1.5, color=color, marker='o', markersize=4)
    
    # Set y-axis to start from 0
    ax.set_ylim(bottom=0)
    
    ax.set_xlabel('Time (sec)', fontsize=13, fontweight='bold')
    ax.set_ylabel('Bitrate (Mbits/sec)', fontsize=13, fontweight='bold')
    ax.set_title(title, fontsize=15, fontweight='bold')
    ax.grid(True, alpha=0.3)
    
    # Add statistics
    # avg_bitrate = sum(bitrates) / len(bitrates)
    # max_bitrate = max(bitrates)
    # min_bitrate = min(bitrates)
    
    # stats_text = f'Avg: {avg_bitrate:.2f} Mbits/sec\nMax: {max_bitrate:.2f} Mbits/sec\nMin: {min_bitrate:.2f} Mbits/sec'
    # ax.text(0.02, 0.98, stats_text, transform=ax.transAxes, 
    #         fontsize=10, verticalalignment='top',
    #         bbox=dict(boxstyle='round', facecolor='white', alpha=0.8))
    
    plt.tight_layout()
    os.makedirs(os.path.dirname(output_file), exist_ok=True)
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"  ✓ Saved: {output_file}")
    plt.close()


def plot_comprehensive_comparison(tcp_data_s, tcp_data_c, udp_data_s, udp_data_c, 
                                  udp_summary_s, udp_summary_c, output_file):
    """
    Plot comprehensive TCP vs UDP comparison with 4 subplots:
    1. Bitrate timeline comparison
    2. Jitter comparison (bar chart)
    3. Packet loss ratio (bar chart)
    4. Interruption time (bar chart)
    """
    fig = plt.figure(figsize=(16, 12))
    gs = fig.add_gridspec(2, 2, hspace=0.3, wspace=0.3)
    
    # ===== Subplot 1: Bitrate Timeline Comparison =====
    ax1 = fig.add_subplot(gs[0, :])  # Span full width
    
    # Plot TCP
    if tcp_data_s:
        tcp_times = [item[1] for item in tcp_data_s]
        tcp_bitrates = [item[2] for item in tcp_data_s]
        ax1.plot(tcp_times, tcp_bitrates, linewidth=1.5, color='#c44e52', 
                marker='o', markersize=4, label='TCP', alpha=0.8)
    
    # Plot UDP
    if udp_data_s:
        udp_times = [item[1] for item in udp_data_s]
        udp_bitrates = [item[2] for item in udp_data_s]
        ax1.plot(udp_times, udp_bitrates, linewidth=1.5, color='#4c72b0', 
                marker='s', markersize=4, label='UDP', alpha=0.8)
    
    ax1.set_ylim(bottom=0)
    ax1.set_xlabel('Time (sec)', fontsize=12, fontweight='bold')
    ax1.set_ylabel('Bitrate (Mbits/sec)', fontsize=12, fontweight='bold')
    ax1.set_title('Bitrate Comparison Over Time', fontsize=14, fontweight='bold')
    ax1.legend(loc='best', fontsize=11, framealpha=0.95)
    ax1.grid(True, alpha=0.3)
    
    # ===== Subplot 2: Jitter Comparison =====
    ax2 = fig.add_subplot(gs[1, 0])
    
    jitter_labels = []
    jitter_values = []
    jitter_colors = []
    
    if udp_summary_s:
        jitter_labels.append('UDP\nReceiver')
        jitter_values.append(udp_summary_s[1])  # jitter
        jitter_colors.append('#4c72b0')
    
    if udp_summary_c:
        # Find sender summary from client file
        jitter_labels.append('UDP\nSender')
        jitter_values.append(udp_summary_c[1])  # jitter
        jitter_colors.append('#5da5da')
    
    if jitter_values:
        bars = ax2.bar(jitter_labels, jitter_values, color=jitter_colors, alpha=0.7, edgecolor='black')
        ax2.set_ylabel('Jitter (ms)', fontsize=12, fontweight='bold')
        ax2.set_title('Jitter Comparison', fontsize=13, fontweight='bold')
        ax2.grid(True, alpha=0.3, axis='y')
        
        # Add value labels on bars
        for bar in bars:
            height = bar.get_height()
            ax2.text(bar.get_x() + bar.get_width()/2., height,
                    f'{height:.3f} ms',
                    ha='center', va='bottom', fontsize=10)
    else:
        ax2.text(0.5, 0.5, 'No Jitter Data\n(TCP only)', 
                ha='center', va='center', fontsize=12, transform=ax2.transAxes)
        ax2.set_title('Jitter Comparison', fontsize=13)
    
    # ===== Subplot 3: Packet Loss Ratio =====
    ax3 = fig.add_subplot(gs[1, 1])
    
    loss_labels = []
    loss_values = []
    loss_colors = []
    
    if udp_summary_s:
        lost, total = udp_summary_s[2], udp_summary_s[3]
        loss_ratio = (lost / total * 100) if total > 0 else 0
        loss_labels.append('UDP\nReceiver')
        loss_values.append(loss_ratio)
        loss_colors.append('#c44e52')
    
    if udp_summary_c:
        lost, total = udp_summary_c[2], udp_summary_c[3]
        loss_ratio = (lost / total * 100) if total > 0 else 0
        loss_labels.append('UDP\nSender')
        loss_values.append(loss_ratio)
        loss_colors.append('#f59127')
    
    if loss_values:
        bars = ax3.bar(loss_labels, loss_values, color=loss_colors, alpha=0.7, edgecolor='black')
        ax3.set_ylabel('Packet Loss (%)', fontsize=12, fontweight='bold')
        ax3.set_title('Packet Loss Ratio', fontsize=13, fontweight='bold')
        ax3.grid(True, alpha=0.3, axis='y')
        
        # Add value labels on bars
        for bar in bars:
            height = bar.get_height()
            ax3.text(bar.get_x() + bar.get_width()/2., height,
                    f'{height:.2f}%',
                    ha='center', va='bottom', fontsize=10)
    else:
        ax3.text(0.5, 0.5, 'No Loss Data\n(TCP only)', 
                ha='center', va='center', fontsize=12, transform=ax3.transAxes)
        ax3.set_title('Packet Loss Ratio', fontsize=13, fontweight='bold')
    
    plt.tight_layout()
    os.makedirs(os.path.dirname(output_file), exist_ok=True)
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"  ✓ Saved: {output_file}")
    plt.close()


def plot_comprehensive_comparison_4panel(tcp_data_s, udp_data_s, tcp_data_c, output_file):
    """
    Plot comprehensive TCP vs UDP comparison with 4 subplots in a 2x2 grid:
    1. Bitrate timeline comparison (top, spans both columns)
    2. Packet loss ratio - TCP (0%) and UDP (bottom left)
    3. Retransmissions - TCP from client, UDP (0) (bottom middle)
    4. Interruption time (bottom right)
    """
    fig = plt.figure(figsize=(18, 10))
    gs = fig.add_gridspec(2, 3, hspace=0.35, wspace=0.3, height_ratios=[1.2, 1])
    
    # ===== Subplot 1: Bitrate Timeline Comparison (spans all columns) =====
    ax1 = fig.add_subplot(gs[0, :])
    
    # Plot TCP
    if tcp_data_s:
        tcp_times = [item[1] for item in tcp_data_s]
        tcp_bitrates = [item[2] for item in tcp_data_s]
        ax1.plot(tcp_times, tcp_bitrates, linewidth=1.5, color='#c44e52', 
                marker='o', markersize=4, label='TCP', alpha=0.8)
    
    # Plot UDP
    if udp_data_s:
        udp_times = [item[1] for item in udp_data_s]
        udp_bitrates = [item[2] for item in udp_data_s]
        ax1.plot(udp_times, udp_bitrates, linewidth=1.5, color='#4c72b0', 
                marker='s', markersize=4, label='UDP', alpha=0.8)
    
    ax1.set_ylim(bottom=0)
    ax1.set_xlabel('Time (sec)', fontsize=13, fontweight='bold')
    ax1.set_ylabel('Bitrate (Mbits/sec)', fontsize=13, fontweight='bold')
    ax1.set_title('Bitrate Comparison Over Time', fontsize=15, fontweight='bold', pad=15)
    ax1.legend(loc='best', fontsize=12, framealpha=0.95)
    ax1.grid(True, alpha=0.3)
    
    # ===== Subplot 2: Packet Loss Ratio - Both TCP (0) and UDP =====
    ax2 = fig.add_subplot(gs[1, 0])
    
    loss_labels = []
    loss_values = []
    loss_colors = []
    
    # TCP always has 0% packet loss (reliable protocol)
    loss_labels.append('TCP')
    loss_values.append(0.0)
    loss_colors.append('#c44e52')
    
    if udp_data_s:
        # Calculate total loss from interval data
        total_lost = sum([item[4] for item in udp_data_s if item[4] is not None])
        total_packets = sum([item[5] for item in udp_data_s if item[5] is not None])
        if total_packets > 0:
            loss_ratio = (total_lost / total_packets * 100)
            loss_labels.append('UDP')
            loss_values.append(loss_ratio)
            loss_colors.append('#4c72b0')
    
    if loss_labels:
        bars = ax2.bar(loss_labels, loss_values, color=loss_colors, alpha=0.7, edgecolor='black', width=0.5)
        ax2.set_ylabel('Packet Loss (%)', fontsize=12, fontweight='bold')
        ax2.set_title('Packet Loss Ratio', fontsize=13, fontweight='bold')
        ax2.grid(True, alpha=0.3, axis='y')
        ax2.set_ylim(bottom=0)
        
        # Add value labels on bars
        for bar in bars:
            height = bar.get_height()
            ax2.text(bar.get_x() + bar.get_width()/2., height,
                    f'{height:.2f}%',
                    ha='center', va='bottom', fontsize=11)
    
    # ===== Subplot 3: Retransmissions - TCP from client, UDP (0) =====
    ax3 = fig.add_subplot(gs[1, 1])
    
    retr_labels = []
    retr_values = []
    retr_colors = []
    
    # TCP retransmissions from client file
    if tcp_data_c:
        total_retr = sum([item[6] for item in tcp_data_c if item[6] is not None])
        retr_labels.append('TCP')
        retr_values.append(total_retr)
        retr_colors.append('#c44e52')
    
    # UDP has no retransmissions
    retr_labels.append('UDP')
    retr_values.append(0)
    retr_colors.append('#4c72b0')
    
    if retr_labels:
        bars = ax3.bar(retr_labels, retr_values, color=retr_colors, alpha=0.7, edgecolor='black', width=0.5)
        ax3.set_ylabel('Retransmissions', fontsize=12, fontweight='bold')
        ax3.set_title('Retransmitted Packets', fontsize=13, fontweight='bold')
        ax3.grid(True, alpha=0.3, axis='y')
        ax3.set_ylim(bottom=0)
        
        # Add value labels on bars
        for bar in bars:
            height = bar.get_height()
            ax3.text(bar.get_x() + bar.get_width()/2., height,
                    f'{int(height)}',
                    ha='center', va='bottom', fontsize=11)
    
    # ===== Subplot 4: Interruption Time =====
    ax4 = fig.add_subplot(gs[1, 2])
    
    # Calculate interruption time (periods with 0 bitrate)
    interruption_labels = []
    interruption_values = []
    interruption_colors = []
    
    if tcp_data_s:
        tcp_interruption = calculate_interruption_time(tcp_data_s)
        interruption_labels.append('TCP')
        interruption_values.append(tcp_interruption)
        interruption_colors.append('#c44e52')
    
    if udp_data_s:
        udp_interruption = calculate_interruption_time(udp_data_s)
        interruption_labels.append('UDP')
        interruption_values.append(udp_interruption)
        interruption_colors.append('#4c72b0')
    
    if interruption_labels:
        bars = ax4.bar(interruption_labels, interruption_values, color=interruption_colors, alpha=0.7, edgecolor='black', width=0.5)
        ax4.set_ylabel('Interruption Time (sec)', fontsize=12, fontweight='bold')
        ax4.set_title('Interruption Time', fontsize=13, fontweight='bold')
        ax4.grid(True, alpha=0.3, axis='y')
        ax4.set_ylim(bottom=0)
        
        # Add value labels on bars
        for bar in bars:
            height = bar.get_height()
            ax4.text(bar.get_x() + bar.get_width()/2., height,
                    f'{height:.1f}s',
                    ha='center', va='bottom', fontsize=11)
    
    plt.tight_layout()
    os.makedirs(os.path.dirname(output_file), exist_ok=True)
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"  ✓ Saved: {output_file}")
    plt.close()


def plot_comparison(tcp_data, udp_data, output_file):
    """Plot TCP vs UDP comparison - simple version for backward compatibility."""
    if not tcp_data and not udp_data:
        print(f"  ⚠ No data to plot for comparison")
        return
    
    fig, ax = plt.subplots(figsize=(14, 7))
    
    # Plot TCP - simple line with markers
    if tcp_data:
        tcp_times = [item[1] for item in tcp_data]
        tcp_bitrates = [item[2] for item in tcp_data]
        ax.plot(tcp_times, tcp_bitrates, linewidth=1.5, color='#c44e52', 
                marker='o', markersize=4, label='TCP')
    
    # Plot UDP - simple line with markers
    if udp_data:
        udp_times = [item[1] for item in udp_data]
        udp_bitrates = [item[2] for item in udp_data]
        ax.plot(udp_times, udp_bitrates, linewidth=1.5, color='#4c72b0', 
                marker='s', markersize=4, label='UDP')
    
    # Set y-axis to start from 0
    ax.set_ylim(bottom=0)
    
    ax.set_xlabel('Time (sec)', fontsize=13, fontweight='bold')
    ax.set_ylabel('Bitrate (Mbits/sec)', fontsize=13, fontweight='bold')
    ax.set_title('TCP and UDP Bitrate Comparison', fontsize=15, fontweight='bold')
    ax.legend(loc='best', fontsize=12, framealpha=0.95)
    ax.grid(True, alpha=0.3)
    
    # Add statistics
    stats_lines = []
    if tcp_data:
        tcp_bitrates = [item[2] for item in tcp_data]
        tcp_avg = sum(tcp_bitrates) / len(tcp_bitrates)
        stats_lines.append(f'TCP Avg: {tcp_avg:.2f} Mbits/sec')
    if udp_data:
        udp_bitrates = [item[2] for item in udp_data]
        udp_avg = sum(udp_bitrates) / len(udp_bitrates)
        stats_lines.append(f'UDP Avg: {udp_avg:.2f} Mbits/sec')
    
    if stats_lines:
        stats_text = '\n'.join(stats_lines)
        ax.text(0.02, 0.98, stats_text, transform=ax.transAxes, 
                fontsize=10, verticalalignment='top',
                bbox=dict(boxstyle='round', facecolor='white', alpha=0.8))
    
    plt.tight_layout()
    os.makedirs(os.path.dirname(output_file), exist_ok=True)
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"  ✓ Saved: {output_file}")
    plt.close()


def main():
    """Main function to generate all plots."""
    print("Generating iPerf Timeline Plots...\n")
    
    # Define file paths
    data_dir = 'iperf-data'
    output_dir = 'figure'
    
    tcp_server_file = os.path.join(data_dir, 'tcp-s.txt')
    udp_server_file = os.path.join(data_dir, 'udp-s.txt')
    tcp_client_file = os.path.join(data_dir, 'tcp-c.txt')
    udp_client_file = os.path.join(data_dir, 'udp-c.txt')
    
    # Parse data files
    print("Parsing data files...")
    
    # Load TCP server data
    if os.path.exists(tcp_server_file):
        tcp_data_s = parse_iperf_file(tcp_server_file)
        print(f"  ✓ Loaded {len(tcp_data_s)} TCP records from server file")
    else:
        tcp_data_s = []
        print(f"  ⚠ No TCP server file found")
    
    # Load TCP client data (for retransmissions)
    if os.path.exists(tcp_client_file):
        tcp_data_c = parse_iperf_file(tcp_client_file)
        print(f"  ✓ Loaded {len(tcp_data_c)} TCP records from client file")
    else:
        tcp_data_c = []
        print(f"  ⚠ No TCP client file found")
    
    # Use whichever TCP data we have for timeline
    tcp_data = tcp_data_s if tcp_data_s else tcp_data_c
    
    # Load UDP server data
    if os.path.exists(udp_server_file):
        udp_data = parse_iperf_file(udp_server_file)
        print(f"  ✓ Loaded {len(udp_data)} UDP records from server file")
    elif os.path.exists(udp_client_file):
        udp_data = parse_iperf_file(udp_client_file)
        print(f"  ✓ Loaded {len(udp_data)} UDP records from client file")
    else:
        udp_data = []
        print(f"  ⚠ No UDP data file found")
    
    print("\nGenerating plots...")
    
    # 1. UDP timeline
    if udp_data:
        plot_timeline(udp_data, 'UDP Bitrate Timeline', 
                     os.path.join(output_dir, 'udp-timeline.png'), 
                     color='#4c72b0')
    
    # 2. TCP timeline
    if tcp_data:
        plot_timeline(tcp_data, 'TCP Bitrate Timeline', 
                     os.path.join(output_dir, 'tcp-timeline.png'), 
                     color='#c44e52')
    
    # 3. TCP vs UDP comparison - comprehensive 4-panel version
    if tcp_data or udp_data:
        plot_comprehensive_comparison_4panel(tcp_data_s, udp_data, tcp_data_c, 
                       os.path.join(output_dir, 'tcp-udp-comparison.png'))
    
    print("\n✓ All plots generated successfully!")


if __name__ == '__main__':
    main()
