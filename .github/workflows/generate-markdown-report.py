#!/usr/bin/env python3
import json
import sys
from datetime import datetime

def format_number(num):
    """Format large numbers for readability"""
    if num >= 1000000:
        return f"{num/1000000:.2f}M"
    elif num >= 1000:
        return f"{num/1000:.1f}K"
    else:
        return f"{num:.0f}"

def generate_markdown_report(deployment_data, history_data):
    """Generate a comprehensive Markdown report from benchmark data"""
    
    report = []
    report.append("# 📊 WebSocket Relay Performance Report")
    report.append(f"\n*Generated: {datetime.now().strftime('%Y-%m-%d %H:%M:%S UTC')}*\n")
    
    # Current deployment status
    report.append("## 🚀 Latest Deployment\n")
    report.append(f"- **Commit:** `{deployment_data.get('commit_short', 'unknown')}` - {deployment_data.get('commit_message', 'No message')[:100]}")
    report.append(f"- **Status:** {deployment_data.get('deployment_status', 'unknown')}")
    report.append(f"- **Timestamp:** {deployment_data.get('timestamp', 'unknown')}")
    report.append(f"- **Repository:** {deployment_data.get('repository', 'unknown')}\n")
    
    # Check for benchmark results
    bench = deployment_data.get('benchmark_results')
    if not bench:
        report.append("⚠️ **No benchmark data available for latest deployment**\n")
    else:
        # Check if it's transport comparison format or single benchmark
        if 'results' in bench and bench['results']:
            # Transport comparison format
            report.append("## ⚡ Transport Comparison: WebSocket vs UDP\n")
            
            ws = bench['results'].get('websocket', {})
            udp = bench['results'].get('udp', {})
            
            # Create comparison table
            report.append("| Metric | WebSocket (TCP) | UDP | Winner |")
            report.append("|--------|-----------------|-----|--------|")
            
            # Latency comparison
            ws_lat = ws.get('average_latency_ms', 0)
            udp_lat = udp.get('average_latency_ms', 0)
            lat_winner = "🏆 WebSocket" if ws_lat < udp_lat and ws_lat > 0 else "🏆 UDP" if udp_lat > 0 else "❌ Invalid"
            report.append(f"| **Avg Latency** | {ws_lat:.2f} ms | {udp_lat:.2f} ms | {lat_winner} |")
            
            # P50 Latency
            report.append(f"| **P50 Latency** | {ws.get('p50_latency_ms', 0):.2f} ms | {udp.get('p50_latency_ms', 0):.2f} ms | - |")
            
            # P95 Latency
            report.append(f"| **P95 Latency** | {ws.get('p95_latency_ms', 0):.2f} ms | {udp.get('p95_latency_ms', 0):.2f} ms | - |")
            
            # P99 Latency
            report.append(f"| **P99 Latency** | {ws.get('p99_latency_ms', 0):.2f} ms | {udp.get('p99_latency_ms', 0):.2f} ms | - |")
            
            # Throughput comparison
            ws_tput = ws.get('messages_per_second', 0)
            udp_tput = udp.get('messages_per_second', 0)
            tput_winner = "🏆 WebSocket" if ws_tput > udp_tput else "🏆 UDP" if udp_tput > 0 else "❌ Invalid"
            report.append(f"| **Messages/sec** | {ws_tput:.0f} | {udp_tput:.0f} | {tput_winner} |")
            
            # Bandwidth comparison
            ws_bw = ws.get('bytes_per_second', 0) / 1024 / 1024
            udp_bw = udp.get('bytes_per_second', 0) / 1024 / 1024
            bw_winner = "🏆 WebSocket" if ws_bw > udp_bw and ws_bw > 0 else "🏆 UDP" if udp_bw > 0 else "❌ Invalid"
            report.append(f"| **Bandwidth** | {ws_bw:.2f} MB/s | {udp_bw:.2f} MB/s | {bw_winner} |")
            
            # Connection stats
            report.append(f"| **Failed Connections** | {ws.get('failed_connections', 0)} | {udp.get('failed_connections', 0)} | - |")
            report.append(f"| **Dropped Messages** | {ws.get('dropped_messages', 0)} | {udp.get('dropped_messages', 0)} | - |")
            report.append("")
            
            # Analysis
            report.append("### 📈 Analysis\n")
            if ws_lat > 0 and udp_lat > 0:
                lat_diff = ((udp_lat - ws_lat) / ws_lat) * 100
                if lat_diff > 0:
                    report.append(f"- UDP has **{lat_diff:.1f}% higher latency** than WebSocket")
                else:
                    report.append(f"- UDP has **{abs(lat_diff):.1f}% lower latency** than WebSocket")
            
            if ws_tput > 0 and udp_tput > 0:
                tput_diff = ((udp_tput - ws_tput) / ws_tput) * 100
                if tput_diff > 0:
                    report.append(f"- UDP achieves **{tput_diff:.1f}% higher throughput**")
                else:
                    report.append(f"- WebSocket achieves **{abs(tput_diff):.1f}% higher throughput**")
            report.append("")
            
        else:
            # Old single benchmark format
            report.append("## 📊 Performance Metrics\n")
            report.append("| Metric | Value |")
            report.append("|--------|-------|")
            report.append(f"| **Total Messages** | {format_number(bench.get('total_messages_sent', 0))} |")
            report.append(f"| **Messages/sec** | {bench.get('messages_per_second', 0):.0f} |")
            report.append(f"| **Avg Latency** | {bench.get('avg_latency_ms', 0):.2f} ms |")
            report.append(f"| **P50 Latency** | {bench.get('p50_latency_ms', 0):.2f} ms |")
            report.append(f"| **P95 Latency** | {bench.get('p95_latency_ms', 0):.2f} ms |")
            report.append(f"| **P99 Latency** | {bench.get('p99_latency_ms', 0):.2f} ms |")
            report.append(f"| **Bandwidth** | {bench.get('bandwidth_mbps', 0):.2f} Mbps |")
            report.append("")
        
        # Test configuration
        config = bench.get('test_config') or bench.get('config', {})
        if config:
            report.append("## 🔧 Test Configuration\n")
            report.append(f"- **Clients:** {config.get('num_clients', config.get('clients', 'unknown'))}")
            report.append(f"- **Duration:** {config.get('duration', '5s')}")
            report.append(f"- **Messages/Client/Sec:** {config.get('messages_per_client_per_sec', 10)}")
            report.append(f"- **Message Size:** {config.get('message_size_bytes', 1024)} bytes\n")
    
    # Data quality issues
    report.append("## ⚠️ Data Quality Analysis\n")
    
    issues = []
    
    # Check current deployment for issues
    if bench:
        if 'results' in bench:
            for transport in ['websocket', 'udp']:
                if transport in bench['results']:
                    data = bench['results'][transport]
                    if data.get('average_latency_ms', 0) == 0:
                        issues.append(f"- **{transport.upper()}**: Zero latency detected (likely measurement error)")
                    if data.get('bytes_per_second', 0) == 0:
                        issues.append(f"- **{transport.upper()}**: Zero bandwidth detected")
                    if data.get('messages_per_second', 0) == 0:
                        issues.append(f"- **{transport.upper()}**: Zero throughput detected")
        else:
            if bench.get('avg_latency_ms', 0) == 0:
                issues.append("- Zero average latency in latest benchmark")
            if bench.get('bandwidth_mbps', 0) == 0:
                issues.append("- Zero bandwidth in latest benchmark")
    
    # Check historical data for issues
    zero_latency_commits = []
    zero_bandwidth_commits = []
    
    for commit in history_data[:10]:  # Check last 10 commits
        commit_hash = commit.get('commit_short', 'unknown')
        bench = commit.get('benchmark_results')
        if bench:
            # Check for zero values
            if 'results' in bench:
                ws = bench['results'].get('websocket', {})
                if ws.get('average_latency_ms', 0) == 0:
                    zero_latency_commits.append(commit_hash)
                if ws.get('bytes_per_second', 0) == 0:
                    zero_bandwidth_commits.append(commit_hash)
            else:
                if bench.get('avg_latency_ms', 0) == 0:
                    zero_latency_commits.append(commit_hash)
                if bench.get('bandwidth_mbps', 0) == 0:
                    zero_bandwidth_commits.append(commit_hash)
    
    if zero_latency_commits:
        issues.append(f"- Commits with zero latency: {', '.join(zero_latency_commits)}")
    if zero_bandwidth_commits:
        issues.append(f"- Commits with zero bandwidth: {', '.join(zero_bandwidth_commits)}")
    
    if issues:
        report.extend(issues)
        report.append("\n**Possible causes:**")
        report.append("- Benchmark client failed to receive messages")
        report.append("- Message timestamp tracking not working correctly")
        report.append("- Network connectivity issues during test")
        report.append("- Insufficient warm-up time before measurements")
    else:
        report.append("✅ No data quality issues detected")
    
    report.append("")
    
    # Historical trends
    report.append("## 📈 Historical Performance (Last 5 Commits)\n")
    report.append("| Commit | Date | Avg Latency | P99 Latency | Messages/sec | Bandwidth |")
    report.append("|--------|------|-------------|-------------|--------------|-----------|")
    
    for commit in history_data[:5]:
        commit_hash = commit.get('commit_short', 'unknown')
        timestamp = commit.get('timestamp', '')
        date = timestamp.split('T')[0] if timestamp else 'unknown'
        
        bench = commit.get('benchmark_results')
        if bench:
            # Try to extract metrics from either format
            if 'results' in bench and bench['results']:
                # Use WebSocket data for historical comparison
                metrics = bench['results'].get('websocket', {})
                avg_lat = metrics.get('average_latency_ms', 0)
                p99_lat = metrics.get('p99_latency_ms', 0)
                msg_sec = metrics.get('messages_per_second', 0)
                bandwidth = metrics.get('bytes_per_second', 0) / 1024 / 1024
            else:
                # Old format
                avg_lat = bench.get('avg_latency_ms', 0)
                p99_lat = bench.get('p99_latency_ms', 0)
                msg_sec = bench.get('messages_per_second', 0)
                bandwidth = bench.get('bandwidth_mbps', 0) / 8  # Convert Mbps to MB/s
            
            # Flag suspicious values
            avg_lat_str = f"{avg_lat:.2f} ms" if avg_lat > 0 else "❌ 0 ms"
            p99_lat_str = f"{p99_lat:.2f} ms" if p99_lat > 0 else "❌ 0 ms"
            msg_sec_str = f"{msg_sec:.0f}" if msg_sec > 0 else "❌ 0"
            bandwidth_str = f"{bandwidth:.2f} MB/s" if bandwidth > 0 else "❌ 0 MB/s"
            
            report.append(f"| {commit_hash} | {date} | {avg_lat_str} | {p99_lat_str} | {msg_sec_str} | {bandwidth_str} |")
        else:
            report.append(f"| {commit_hash} | {date} | No data | No data | No data | No data |")
    
    report.append("")
    
    # Summary
    report.append("## 📋 Summary\n")
    if deployment_data.get('deployment_status') == 'success':
        report.append("✅ **Deployment successful**")
    else:
        report.append(f"⚠️ **Deployment status:** {deployment_data.get('deployment_status', 'unknown')}")
    
    if deployment_data.get('functional_test_status') == 'passed':
        report.append("✅ **Functional tests passed**")
    else:
        report.append("❌ **Functional tests:** " + deployment_data.get('functional_test_status', 'unknown'))
    
    # Note about data issues if found
    if issues:
        report.append("\n⚠️ **Note:** Data quality issues detected. Some metrics may be unreliable.")
    
    report.append("\n---")
    report.append("*🤖 Report generated automatically by WebSocket Relay CI/CD pipeline*")
    
    return "\n".join(report)

def main():
    # Read deployment data
    try:
        with open('deployment-report.json', 'r') as f:
            deployment_data = json.load(f)
    except:
        deployment_data = {"error": "Could not load deployment report"}
    
    # Read history data
    try:
        with open('benchmarks/history.json', 'r') as f:
            history_data = json.load(f)
    except:
        history_data = []
    
    # Generate report
    report = generate_markdown_report(deployment_data, history_data)
    
    # Write to file
    with open('benchmark-report.md', 'w') as f:
        f.write(report)
    
    print("Markdown report generated: benchmark-report.md")
    
    # Also output to stdout for logging
    print("\n" + "="*60)
    print(report)
    print("="*60)

if __name__ == "__main__":
    main()