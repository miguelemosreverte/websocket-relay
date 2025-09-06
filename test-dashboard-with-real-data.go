package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
)

func main() {
	// Fetch real data from GitHub Pages
	fmt.Println("Fetching real data from GitHub Pages...")
	resp, err := http.Get("https://miguelemosreverte.github.io/websocket-relay/")
	if err != nil {
		fmt.Printf("Error fetching page: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response: %v\n", err)
		return
	}
	
	htmlContent := string(body)
	
	// Extract deploymentData
	deployStart := strings.Index(htmlContent, "const deploymentData = {")
	if deployStart == -1 {
		fmt.Println("Could not find deploymentData")
		return
	}
	deployStart += len("const deploymentData = ")
	deployEnd := strings.Index(htmlContent[deployStart:], "};") + deployStart + 1
	deploymentJSON := htmlContent[deployStart:deployEnd]
	
	// Extract historyData
	histStart := strings.Index(htmlContent, "const historyData = [")
	if histStart == -1 {
		fmt.Println("Could not find historyData")
		return
	}
	histStart += len("const historyData = ")
	histEnd := strings.Index(htmlContent[histStart:], "];") + histStart + 1
	historyJSON := htmlContent[histStart:histEnd]
	
	// Read dashboard template
	dashboardTemplate, err := ioutil.ReadFile(".github/workflows/comprehensive-dashboard.html")
	if err != nil {
		fmt.Printf("Error reading dashboard template: %v\n", err)
		return
	}
	
	// Create new dashboard with fixed extractMetrics function
	dashboard := string(dashboardTemplate)
	
	// Find the extractMetrics function and replace it with fixed version
	funcStart := strings.Index(dashboard, "function extractMetrics(commit) {")
	if funcStart != -1 {
		// Find the end of the function (next function or script end)
		funcEnd := strings.Index(dashboard[funcStart:], "\n        function ")
		if funcEnd == -1 {
			funcEnd = strings.Index(dashboard[funcStart:], "\n        // Initialize")
		}
		if funcEnd != -1 {
			funcEnd += funcStart
			
			fixedFunction := `function extractMetrics(commit) {
            const bench = commit.benchmark_results;
            if (!bench) return null;
            
            // Try new transport format first (results.websocket)
            if (bench.results && bench.results.websocket) {
                console.log('Using new format for', commit.commit_short, bench.results.websocket);
                return bench.results.websocket;
            }
            
            // Fall back to old direct format
            if (bench.messages_per_second !== undefined || bench.avg_latency_ms !== undefined) {
                // Check if this is a broken benchmark (no messages received)
                if (bench.total_messages_received === 0) {
                    console.warn('Broken benchmark for', commit.commit_short, '- no messages received');
                    // Skip broken benchmarks entirely - return null
                    return null;
                }
                
                console.log('Using old format for', commit.commit_short, {
                    avg_latency: bench.avg_latency_ms,
                    messages: bench.messages_per_second
                });
                
                // Map old format to new format keys
                let bytesPerSec = bench.bytes_per_second || 0;
                if (!bytesPerSec && bench.avg_bandwidth_mbps) {
                    bytesPerSec = bench.avg_bandwidth_mbps * 125000; // Mbps to bytes/sec
                } else if (!bytesPerSec && bench.bandwidth_mbps) {
                    bytesPerSec = bench.bandwidth_mbps * 125000;
                }
                
                return {
                    average_latency_ms: bench.avg_latency_ms || bench.average_latency_ms || 0,
                    p50_latency_ms: bench.p50_latency_ms || 0,
                    p95_latency_ms: bench.p95_latency_ms || 0,
                    p99_latency_ms: bench.p99_latency_ms || 0,
                    messages_per_second: bench.messages_per_second || 0,
                    bytes_per_second: bytesPerSec,
                    bandwidth_mbps: bench.bandwidth_mbps || bench.avg_bandwidth_mbps || 0
                };
            }
            
            console.log('No metrics found for', commit.commit_short);
            return null;
        }
`
			dashboard = dashboard[:funcStart] + fixedFunction + dashboard[funcEnd:]
		}
	}
	
	// Replace the data placeholders with real data
	deploymentMarker := "const deploymentData = {"
	deploymentEndMarker := "};"
	if idx := strings.Index(dashboard, deploymentMarker); idx != -1 {
		endIdx := strings.Index(dashboard[idx:], deploymentEndMarker)
		if endIdx != -1 {
			endIdx += idx + len(deploymentEndMarker)
			dashboard = dashboard[:idx] + "const deploymentData = " + deploymentJSON + ";" + dashboard[endIdx:]
		}
	}
	
	historyMarker := "const historyData = ["
	historyEndMarker := "];"
	if idx := strings.Index(dashboard, historyMarker); idx != -1 {
		endIdx := strings.Index(dashboard[idx:], historyEndMarker)
		if endIdx != -1 {
			endIdx += idx + len(historyEndMarker)
			dashboard = dashboard[:idx] + "const historyData = " + historyJSON + ";" + dashboard[endIdx:]
		}
	}
	
	// Add console logging to debug
	debugScript := `
    <script>
        // Debug: Log what we're working with
        console.log('Deployment data:', deploymentData);
        console.log('History entries:', historyData.length);
        
        // Log extraction for each commit
        const allCommits = [deploymentData, ...historyData].slice(0, 10);
        console.log('Testing extraction for first 10 commits:');
        allCommits.forEach(commit => {
            const metrics = extractMetrics(commit);
            console.log(commit.commit_short + ':', metrics);
        });
    </script>
</body>`
	
	dashboard = strings.Replace(dashboard, "</body>", debugScript, 1)
	
	// Write the generated dashboard
	outputFile := "test-dashboard-real-data.html"
	if err := ioutil.WriteFile(outputFile, []byte(dashboard), 0644); err != nil {
		fmt.Printf("Error writing dashboard: %v\n", err)
		return
	}
	
	fmt.Printf("Dashboard generated with REAL data!\n")
	fmt.Printf("Open %s in your browser\n", outputFile)
	fmt.Printf("Check the browser console (F12) for debug output\n")
	
	// Parse and show summary
	var deployData map[string]interface{}
	json.Unmarshal([]byte(deploymentJSON), &deployData)
	
	var histData []map[string]interface{}
	json.Unmarshal([]byte(historyJSON), &histData)
	
	fmt.Printf("\nData summary:\n")
	fmt.Printf("  Current deployment: %v\n", deployData["commit_short"])
	fmt.Printf("  History entries: %d\n", len(histData))
	
	// Check for broken entries
	brokenCount := 0
	for _, entry := range histData {
		if bench, ok := entry["benchmark_results"].(map[string]interface{}); ok {
			if received, ok := bench["total_messages_received"].(float64); ok && received == 0 {
				brokenCount++
				fmt.Printf("  Broken: %v (0 messages received)\n", entry["commit_short"])
			}
		}
	}
	fmt.Printf("  Broken benchmarks: %d\n", brokenCount)
}