#!/usr/bin/env python3
"""
Clean up broken benchmark data from history.json
Removes or fixes entries with zero latency and zero messages received
"""
import json
import sys

def is_broken_benchmark(bench_results):
    """Check if benchmark results indicate a broken test"""
    if not bench_results:
        return False
    
    # New format with transport comparison is always valid
    if 'results' in bench_results:
        return False
    
    # Old format - check for broken indicators
    if bench_results.get('total_messages_received', 1) == 0:
        if bench_results.get('avg_latency_ms', 1) == 0:
            return True
    
    return False

def clean_history():
    """Clean history.json by removing or marking broken benchmarks"""
    
    try:
        with open('benchmarks/history.json', 'r') as f:
            history = json.load(f)
    except FileNotFoundError:
        print("No history.json found")
        return
    
    print(f"Processing {len(history)} historical entries...")
    
    cleaned_history = []
    removed_count = 0
    
    for entry in history:
        bench = entry.get('benchmark_results')
        
        if bench and is_broken_benchmark(bench):
            print(f"Found broken benchmark: {entry.get('commit_short', 'unknown')}")
            # Option 1: Skip broken entries entirely
            removed_count += 1
            continue
            
            # Option 2: Keep but mark as broken (uncomment if preferred)
            # entry['benchmark_results']['data_quality'] = 'broken'
            # entry['benchmark_results']['note'] = 'Zero latency due to measurement error'
        
        cleaned_history.append(entry)
    
    print(f"\nRemoved {removed_count} broken entries")
    print(f"Keeping {len(cleaned_history)} valid entries")
    
    # Write cleaned history
    with open('benchmarks/history.json', 'w') as f:
        json.dump(cleaned_history, f, indent=2)
    
    print("\nHistory cleaned successfully!")
    
    # Show summary of remaining data
    print("\nRemaining commits:")
    for entry in cleaned_history[:5]:
        commit = entry.get('commit_short', 'unknown')
        bench = entry.get('benchmark_results', {})
        
        if 'results' in bench:
            # New format
            ws = bench['results'].get('websocket', {})
            latency = ws.get('average_latency_ms', 0)
            throughput = ws.get('messages_per_second', 0)
        else:
            # Old format
            latency = bench.get('avg_latency_ms', 0)
            throughput = bench.get('messages_per_second', 0)
        
        print(f"  {commit}: {latency:.1f}ms latency, {throughput:.0f} msg/s")

if __name__ == "__main__":
    clean_history()