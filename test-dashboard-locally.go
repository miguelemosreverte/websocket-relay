package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"time"
)

type BenchmarkResult struct {
	Config struct {
		Clients  int    `json:"clients"`
		Duration string `json:"duration"`
		Server   string `json:"server"`
	} `json:"config"`
	Results struct {
		Websocket TransportResult `json:"websocket"`
		UDP       TransportResult `json:"udp"`
	} `json:"results"`
	Timestamp string `json:"timestamp"`
}

type TransportResult struct {
	Transport           string  `json:"transport"`
	MessagesPerSecond   float64 `json:"messages_per_second"`
	BytesPerSecond      float64 `json:"bytes_per_second"`
	AverageLatencyMs    float64 `json:"average_latency_ms"`
	P50LatencyMs        float64 `json:"p50_latency_ms"`
	P95LatencyMs        float64 `json:"p95_latency_ms"`
	P99LatencyMs        float64 `json:"p99_latency_ms"`
	TotalMessages       int     `json:"total_messages"`
	TotalBytes          int     `json:"total_bytes"`
	FailedConnections   int     `json:"failed_connections"`
	DroppedMessages     int     `json:"dropped_messages"`
	TestDurationSeconds float64 `json:"test_duration_seconds"`
}

type DeploymentInfo struct {
	CommitSHA   string          `json:"commit_sha"`
	CommitShort string          `json:"commit_short"`
	Branch      string          `json:"branch"`
	Message     string          `json:"message"`
	Author      string          `json:"author"`
	Timestamp   string          `json:"timestamp"`
	BenchResult BenchmarkResult `json:"benchmark_results"`
}

func main() {
	// Read the benchmark results
	benchData, err := ioutil.ReadFile("test/benchmark-transport-results.json")
	if err != nil {
		fmt.Printf("Error reading benchmark results: %v\n", err)
		return
	}

	var benchResult BenchmarkResult
	if err := json.Unmarshal(benchData, &benchResult); err != nil {
		fmt.Printf("Error parsing benchmark results: %v\n", err)
		return
	}

	// Create mock deployment info
	deployment := DeploymentInfo{
		CommitSHA:   "abc123def456789",
		CommitShort: "abc123d",
		Branch:      "main",
		Message:     "Test commit for local dashboard",
		Author:      "Test User",
		Timestamp:   time.Now().Format(time.RFC3339),
		BenchResult: benchResult,
	}

	// Create mock history with varied data
	history := []DeploymentInfo{
		{
			CommitShort: "def456a",
			Message:     "Previous commit 1",
			Timestamp:   time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
			BenchResult: BenchmarkResult{
				Results: struct {
					Websocket TransportResult `json:"websocket"`
					UDP       TransportResult `json:"udp"`
				}{
					Websocket: TransportResult{
						AverageLatencyMs:  3.5,
						MessagesPerSecond: 180.0,
						BytesPerSecond:    650000,
					},
				},
			},
		},
		{
			CommitShort: "789012b",
			Message:     "Previous commit 2",
			Timestamp:   time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
			BenchResult: BenchmarkResult{
				Results: struct {
					Websocket TransportResult `json:"websocket"`
					UDP       TransportResult `json:"udp"`
				}{
					Websocket: TransportResult{
						AverageLatencyMs:  4.2,
						MessagesPerSecond: 175.0,
						BytesPerSecond:    620000,
					},
				},
			},
		},
		{
			CommitShort: "345678c",
			Message:     "Previous commit 3",
			Timestamp:   time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
			BenchResult: BenchmarkResult{
				Results: struct {
					Websocket TransportResult `json:"websocket"`
					UDP       TransportResult `json:"udp"`
				}{
					Websocket: TransportResult{
						AverageLatencyMs:  3.8,
						MessagesPerSecond: 190.0,
						BytesPerSecond:    680000,
					},
				},
			},
		},
	}

	// Read the dashboard template
	dashboardTemplate, err := ioutil.ReadFile(".github/workflows/comprehensive-dashboard.html")
	if err != nil {
		fmt.Printf("Error reading dashboard template: %v\n", err)
		return
	}

	// Convert to JSON strings
	deploymentJSON, _ := json.MarshalIndent(deployment, "        ", "    ")
	historyJSON, _ := json.MarshalIndent(history, "        ", "    ")

	// Replace the data placeholders
	dashboard := string(dashboardTemplate)
	
	// Find and replace the data sections
	deploymentMarker := "const deploymentData = {"
	deploymentEnd := "};"
	if idx := strings.Index(dashboard, deploymentMarker); idx != -1 {
		endIdx := strings.Index(dashboard[idx:], deploymentEnd)
		if endIdx != -1 {
			endIdx += idx + len(deploymentEnd)
			dashboard = dashboard[:idx] + "const deploymentData = " + string(deploymentJSON) + ";" + dashboard[endIdx:]
		}
	}

	historyMarker := "const historyData = ["
	historyEnd := "];"
	if idx := strings.Index(dashboard, historyMarker); idx != -1 {
		endIdx := strings.Index(dashboard[idx:], historyEnd)
		if endIdx != -1 {
			endIdx += idx + len(historyEnd)
			dashboard = dashboard[:idx] + "const historyData = " + string(historyJSON) + ";" + dashboard[endIdx:]
		}
	}

	// Write the generated dashboard
	outputFile := "test-dashboard-output.html"
	if err := ioutil.WriteFile(outputFile, []byte(dashboard), 0644); err != nil {
		fmt.Printf("Error writing dashboard: %v\n", err)
		return
	}

	fmt.Printf("Dashboard generated successfully!\n")
	fmt.Printf("Open %s in your browser to test\n", outputFile)
	fmt.Printf("\nDeployment data:\n")
	fmt.Printf("  Commit: %s\n", deployment.CommitShort)
	fmt.Printf("  WebSocket Latency: %.2f ms\n", deployment.BenchResult.Results.Websocket.AverageLatencyMs)
	fmt.Printf("  WebSocket Throughput: %.0f msg/s\n", deployment.BenchResult.Results.Websocket.MessagesPerSecond)
	fmt.Printf("\nHistory data (%d entries):\n", len(history))
	for _, h := range history {
		fmt.Printf("  %s: %.2f ms, %.0f msg/s\n", 
			h.CommitShort,
			h.BenchResult.Results.Websocket.AverageLatencyMs,
			h.BenchResult.Results.Websocket.MessagesPerSecond)
	}
}