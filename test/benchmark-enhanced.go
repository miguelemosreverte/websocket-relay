package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type BenchmarkClient struct {
	ID       int
	Conn     *websocket.Conn
	SendChan chan []byte
	Stats    *ClientStats
	wg       sync.WaitGroup
}

type ClientStats struct {
	MessagesSent     int64
	MessagesReceived int64
	BytesSent        int64
	BytesReceived    int64
	Latencies        []float64
	mu               sync.Mutex
}

// Time-series data point
type MetricSnapshot struct {
	Timestamp         float64 `json:"timestamp"`         // Seconds since start
	MessagesSent      int64   `json:"messages_sent"`
	MessagesReceived  int64   `json:"messages_received"`
	BytesSent         int64   `json:"bytes_sent"`
	BytesReceived     int64   `json:"bytes_received"`
	ActiveConnections int     `json:"active_connections"`
	ErrorCount        int64   `json:"error_count"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	P95LatencyMs      float64 `json:"p95_latency_ms"`
	P99LatencyMs      float64 `json:"p99_latency_ms"`
	ThroughputMbps    float64 `json:"throughput_mbps"`
}

type EnhancedBenchmarkResults struct {
	// Summary stats
	DurationSeconds       float64  `json:"duration_seconds"`
	TotalMessagesSent     int64    `json:"total_messages_sent"`
	TotalMessagesReceived int64    `json:"total_messages_received"`
	TotalBytesSent        int64    `json:"total_bytes_sent"`
	TotalBytesReceived    int64    `json:"total_bytes_received"`
	AvgLatencyMs          float64  `json:"avg_latency_ms"`
	P50LatencyMs          float64  `json:"p50_latency_ms"`
	P95LatencyMs          float64  `json:"p95_latency_ms"`
	P99LatencyMs          float64  `json:"p99_latency_ms"`
	MaxLatencyMs          float64  `json:"max_latency_ms"`
	MinLatencyMs          float64  `json:"min_latency_ms"`
	ConnectionCount       int      `json:"connection_count"`
	MessagesPerSecond     float64  `json:"messages_per_second"`
	PeakBandwidthMbps     float64  `json:"peak_bandwidth_mbps"`
	AvgBandwidthMbps      float64  `json:"avg_bandwidth_mbps"`
	Errors                int64    `json:"errors"`
	Timestamp             string   `json:"timestamp"`
	
	// Time-series data (every 100ms)
	TimeSeries []MetricSnapshot `json:"time_series"`
	
	// Test configuration
	TestConfig struct {
		NumClients              int    `json:"num_clients"`
		MessagesPerClientPerSec int    `json:"messages_per_client_per_sec"`
		MessageSizeBytes        int    `json:"message_size_bytes"`
		TestDurationSeconds     int    `json:"test_duration_seconds"`
		ServerURL               string `json:"server_url"`
	} `json:"test_config"`
}

type BenchmarkMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ClientID  int       `json:"client_id"`
	Sequence  int64     `json:"sequence"`
	Timestamp int64     `json:"timestamp"`
	Content   string    `json:"content"`
	Payload   string    `json:"payload"` // For bandwidth testing
}

var (
	totalErrors      int64
	messageSequence  int64
	
	// Metrics collection
	metricsMutex     sync.RWMutex
	latencyWindow    []float64
	lastSnapshot     MetricSnapshot
	
	// Track sent messages for latency calculation
	sentMessages     sync.Map // map[string]int64 - message identifier -> send timestamp
)

func createBenchmarkClient(id int, serverURL string) (*BenchmarkClient, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	
	// Handle HTTPS -> WSS conversion
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/ws"
	q := u.Query()
	q.Set("name", fmt.Sprintf("bench-%d", id))
	q.Set("room", "benchmark")
	u.RawQuery = q.Encode()

	// Create dialer with TLS config if needed
	dialer := websocket.DefaultDialer
	if os.Getenv("SKIP_TLS_VERIFY") == "true" && u.Scheme == "wss" {
		dialer = &websocket.Dialer{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}

	client := &BenchmarkClient{
		ID:       id,
		Conn:     conn,
		SendChan: make(chan []byte, 100),
		Stats: &ClientStats{
			Latencies: make([]float64, 0, 10000),
		},
	}

	return client, nil
}

func (c *BenchmarkClient) startReading() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			_, message, err := c.Conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					atomic.AddInt64(&totalErrors, 1)
				}
				return
			}

			var msg BenchmarkMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			// Skip non-message types (join/leave events)
			if msg.Type != "message" && msg.Type != "" {
				continue
			}

			atomic.AddInt64(&c.Stats.MessagesReceived, 1)
			atomic.AddInt64(&c.Stats.BytesReceived, int64(len(message)))

			// Calculate latency by matching received messages to sent ones
			// The relay broadcasts messages with the sender's name
			if msg.Name == fmt.Sprintf("bench-%d", c.ID) {
				// This is one of our messages echoed back
				msgKey := fmt.Sprintf("%s:%s", msg.Name, msg.Content)
				if sendTimeVal, ok := sentMessages.LoadAndDelete(msgKey); ok {
					sendTime := sendTimeVal.(int64)
					latency := float64(time.Now().UnixNano()-sendTime) / 1e6 // Convert to ms
					
					c.Stats.mu.Lock()
					c.Stats.Latencies = append(c.Stats.Latencies, latency)
					c.Stats.mu.Unlock()
					
					// Add to global latency window
					metricsMutex.Lock()
					latencyWindow = append(latencyWindow, latency)
				// Keep only last 1000 latencies for rolling window
				if len(latencyWindow) > 1000 {
					latencyWindow = latencyWindow[1:]
				}
				metricsMutex.Unlock()
				
				c.Stats.mu.Unlock()
			}
		}
	}()
}

func (c *BenchmarkClient) startSending(duration time.Duration, messagesPerSecond int, messageSize int) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		
		// Generate payload for bandwidth testing
		payload := generatePayload(messageSize)
		
		interval := time.Second / time.Duration(messagesPerSecond)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		
		timeout := time.After(duration)
		
		for {
			select {
			case <-timeout:
				return
			case <-ticker.C:
				seq := atomic.AddInt64(&messageSequence, 1)
				msgContent := fmt.Sprintf("Msg %d from client %d", seq, c.ID)
				sendTime := time.Now().UnixNano()
				
				msg := BenchmarkMessage{
					Type:      "message",
					ClientID:  c.ID,
					Sequence:  seq,
					Timestamp: sendTime,
					Content:   msgContent,
					Payload:   payload,
				}
				
				// Store send timestamp for latency calculation
				msgKey := fmt.Sprintf("bench-%d:%s", c.ID, msgContent)
				sentMessages.Store(msgKey, sendTime)
				
				data, err := json.Marshal(msg)
				if err != nil {
					atomic.AddInt64(&totalErrors, 1)
					continue
				}
				
				err = c.Conn.WriteMessage(websocket.TextMessage, data)
				if err != nil {
					atomic.AddInt64(&totalErrors, 1)
					return
				}
				
				atomic.AddInt64(&c.Stats.MessagesSent, 1)
				atomic.AddInt64(&c.Stats.BytesSent, int64(len(data)))
			}
		}
	}()
}

func (c *BenchmarkClient) stop() {
	c.Conn.Close()
	c.wg.Wait()
}

func generatePayload(size int) string {
	if size <= 0 {
		return ""
	}
	// Generate random alphanumeric payload
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func calculatePercentile(latencies []float64, percentile float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	
	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)
	sort.Float64s(sorted)
	
	index := int(math.Ceil(percentile/100*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	
	return sorted[index]
}

func collectMetrics(clients []*BenchmarkClient, startTime time.Time) MetricSnapshot {
	var totalSent, totalReceived, totalBytesSent, totalBytesReceived int64
	activeConnections := 0
	
	for _, client := range clients {
		sent := atomic.LoadInt64(&client.Stats.MessagesSent)
		received := atomic.LoadInt64(&client.Stats.MessagesReceived)
		bytesSent := atomic.LoadInt64(&client.Stats.BytesSent)
		bytesReceived := atomic.LoadInt64(&client.Stats.BytesReceived)
		
		totalSent += sent
		totalReceived += received
		totalBytesSent += bytesSent
		totalBytesReceived += bytesReceived
		
		if sent > 0 || received > 0 {
			activeConnections++
		}
	}
	
	elapsed := time.Since(startTime).Seconds()
	
	// Calculate latency percentiles from window
	metricsMutex.RLock()
	avgLatency := 0.0
	p95Latency := 0.0
	p99Latency := 0.0
	if len(latencyWindow) > 0 {
		sum := 0.0
		for _, l := range latencyWindow {
			sum += l
		}
		avgLatency = sum / float64(len(latencyWindow))
		p95Latency = calculatePercentile(latencyWindow, 95)
		p99Latency = calculatePercentile(latencyWindow, 99)
	}
	metricsMutex.RUnlock()
	
	// Calculate throughput for this snapshot
	deltaBytes := (totalBytesSent + totalBytesReceived) - (lastSnapshot.BytesSent + lastSnapshot.BytesReceived)
	deltaTime := elapsed - lastSnapshot.Timestamp
	throughputMbps := 0.0
	if deltaTime > 0 {
		throughputMbps = float64(deltaBytes) * 8 / (deltaTime * 1000000)
	}
	
	snapshot := MetricSnapshot{
		Timestamp:         elapsed,
		MessagesSent:      totalSent,
		MessagesReceived:  totalReceived,
		BytesSent:         totalBytesSent,
		BytesReceived:     totalBytesReceived,
		ActiveConnections: activeConnections,
		ErrorCount:        atomic.LoadInt64(&totalErrors),
		AvgLatencyMs:      avgLatency,
		P95LatencyMs:      p95Latency,
		P99LatencyMs:      p99Latency,
		ThroughputMbps:    throughputMbps,
	}
	
	lastSnapshot = snapshot
	return snapshot
}

func runEnhancedBenchmark(serverURL string, numClients int, duration time.Duration) (*EnhancedBenchmarkResults, error) {
	// Test configuration
	messagesPerClientPerSecond := 50  // Increased for higher load
	messageSize := 1024                // 1KB payload for bandwidth testing
	
	fmt.Printf("=== Enhanced WebSocket Benchmark ===\n")
	fmt.Printf("Server: %s\n", serverURL)
	fmt.Printf("Clients: %d\n", numClients)
	fmt.Printf("Duration: %v\n", duration)
	fmt.Printf("Messages/client/sec: %d\n", messagesPerClientPerSecond)
	fmt.Printf("Message size: %d bytes\n", messageSize)
	fmt.Printf("Expected load: %d msg/s, ~%.1f Mbps\n", 
		numClients*messagesPerClientPerSecond,
		float64(numClients*messagesPerClientPerSecond*messageSize*8)/1000000)
	fmt.Println("=====================================")
	
	// Reset global state
	totalErrors = 0
	messageSequence = 0
	latencyWindow = make([]float64, 0, 1000)
	lastSnapshot = MetricSnapshot{}
	
	// Create clients
	fmt.Printf("Creating %d clients...\n", numClients)
	clients := make([]*BenchmarkClient, 0, numClients)
	for i := 0; i < numClients; i++ {
		client, err := createBenchmarkClient(i, serverURL)
		if err != nil {
			log.Printf("Failed to create client %d: %v", i, err)
			atomic.AddInt64(&totalErrors, 1)
			continue
		}
		clients = append(clients, client)
		client.startReading()
	}
	
	fmt.Printf("Created %d clients successfully\n", len(clients))
	
	// Wait for connections to stabilize
	time.Sleep(500 * time.Millisecond)
	
	// Start collecting metrics
	timeSeries := make([]MetricSnapshot, 0, int(duration.Seconds()*10))
	metricsTicker := time.NewTicker(100 * time.Millisecond) // Collect every 100ms
	defer metricsTicker.Stop()
	
	// Start benchmark
	startTime := time.Now()
	fmt.Println("Starting benchmark...")
	
	// Start sending messages
	for _, client := range clients {
		client.startSending(duration, messagesPerClientPerSecond, messageSize)
	}
	
	// Collect metrics during test
	done := time.After(duration)
	for {
		select {
		case <-done:
			goto finished
		case <-metricsTicker.C:
			snapshot := collectMetrics(clients, startTime)
			timeSeries = append(timeSeries, snapshot)
			
			// Print progress
			fmt.Printf("\r[%.1fs] Sent: %d, Received: %d, Throughput: %.1f Mbps, Avg Latency: %.1f ms",
				snapshot.Timestamp,
				snapshot.MessagesSent,
				snapshot.MessagesReceived,
				snapshot.ThroughputMbps,
				snapshot.AvgLatencyMs)
		}
	}
	
finished:
	fmt.Println("\n\nStopping clients...")
	
	// Stop all clients
	for _, client := range clients {
		client.stop()
	}
	
	actualDuration := time.Since(startTime).Seconds()
	
	// Collect final stats
	var totalSent, totalReceived, totalBytesSent, totalBytesReceived int64
	var allLatencies []float64
	
	for _, client := range clients {
		totalSent += atomic.LoadInt64(&client.Stats.MessagesSent)
		totalReceived += atomic.LoadInt64(&client.Stats.MessagesReceived)
		totalBytesSent += atomic.LoadInt64(&client.Stats.BytesSent)
		totalBytesReceived += atomic.LoadInt64(&client.Stats.BytesReceived)
		
		client.Stats.mu.Lock()
		allLatencies = append(allLatencies, client.Stats.Latencies...)
		client.Stats.mu.Unlock()
	}
	
	// Calculate latency statistics
	var avgLatency, minLatency, maxLatency float64
	if len(allLatencies) > 0 {
		sum := 0.0
		minLatency = allLatencies[0]
		maxLatency = allLatencies[0]
		
		for _, l := range allLatencies {
			sum += l
			if l < minLatency {
				minLatency = l
			}
			if l > maxLatency {
				maxLatency = l
			}
		}
		avgLatency = sum / float64(len(allLatencies))
	}
	
	// Find peak bandwidth
	peakBandwidth := 0.0
	totalBandwidth := 0.0
	for _, snapshot := range timeSeries {
		if snapshot.ThroughputMbps > peakBandwidth {
			peakBandwidth = snapshot.ThroughputMbps
		}
		totalBandwidth += snapshot.ThroughputMbps
	}
	avgBandwidth := 0.0
	if len(timeSeries) > 0 {
		avgBandwidth = totalBandwidth / float64(len(timeSeries))
	}
	
	results := &EnhancedBenchmarkResults{
		DurationSeconds:       actualDuration,
		TotalMessagesSent:     totalSent,
		TotalMessagesReceived: totalReceived,
		TotalBytesSent:        totalBytesSent,
		TotalBytesReceived:    totalBytesReceived,
		AvgLatencyMs:          avgLatency,
		P50LatencyMs:          calculatePercentile(allLatencies, 50),
		P95LatencyMs:          calculatePercentile(allLatencies, 95),
		P99LatencyMs:          calculatePercentile(allLatencies, 99),
		MaxLatencyMs:          maxLatency,
		MinLatencyMs:          minLatency,
		ConnectionCount:       len(clients),
		MessagesPerSecond:     float64(totalSent) / actualDuration,
		PeakBandwidthMbps:     peakBandwidth,
		AvgBandwidthMbps:      avgBandwidth,
		Errors:                atomic.LoadInt64(&totalErrors),
		Timestamp:             time.Now().UTC().Format(time.RFC3339),
		TimeSeries:            timeSeries,
	}
	
	// Add test configuration
	results.TestConfig.NumClients = numClients
	results.TestConfig.MessagesPerClientPerSec = messagesPerClientPerSecond
	results.TestConfig.MessageSizeBytes = messageSize
	results.TestConfig.TestDurationSeconds = int(duration.Seconds())
	results.TestConfig.ServerURL = serverURL
	
	return results, nil
}

func main() {
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}
	
	numClients := 50  // Increased default
	if os.Getenv("NUM_CLIENTS") != "" {
		fmt.Sscanf(os.Getenv("NUM_CLIENTS"), "%d", &numClients)
	}
	
	duration := 5 * time.Second
	if os.Getenv("DURATION") != "" {
		d, _ := time.ParseDuration(os.Getenv("DURATION"))
		if d > 0 {
			duration = d
		}
	}
	
	results, err := runEnhancedBenchmark(serverURL, numClients, duration)
	if err != nil {
		log.Fatalf("Benchmark failed: %v", err)
	}
	
	// Print results summary
	fmt.Println("\n=== Benchmark Results Summary ===")
	fmt.Printf("Duration: %.2f seconds\n", results.DurationSeconds)
	fmt.Printf("Connections: %d\n", results.ConnectionCount)
	fmt.Printf("Messages Sent: %d\n", results.TotalMessagesSent)
	fmt.Printf("Messages Received: %d\n", results.TotalMessagesReceived)
	fmt.Printf("Messages/Second: %.2f\n", results.MessagesPerSecond)
	fmt.Printf("Peak Bandwidth: %.2f Mbps\n", results.PeakBandwidthMbps)
	fmt.Printf("Avg Bandwidth: %.2f Mbps\n", results.AvgBandwidthMbps)
	fmt.Printf("Avg Latency: %.2f ms\n", results.AvgLatencyMs)
	fmt.Printf("P50 Latency: %.2f ms\n", results.P50LatencyMs)
	fmt.Printf("P95 Latency: %.2f ms\n", results.P95LatencyMs)
	fmt.Printf("P99 Latency: %.2f ms\n", results.P99LatencyMs)
	fmt.Printf("Max Latency: %.2f ms\n", results.MaxLatencyMs)
	fmt.Printf("Errors: %d\n", results.Errors)
	fmt.Printf("Time-series data points: %d\n", len(results.TimeSeries))
	
	// Save results to JSON file
	outputFile := "benchmark-results.json"
	if os.Getenv("OUTPUT_FILE") != "" {
		outputFile = os.Getenv("OUTPUT_FILE")
	}
	
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal results: %v", err)
	}
	
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		log.Fatalf("Failed to write results file: %v", err)
	}
	
	fmt.Printf("\nDetailed results saved to %s\n", outputFile)
	
	// Exit with error only if critical failures
	errorRate := float64(results.Errors) / float64(results.TotalMessagesSent) * 100
	if results.TotalMessagesSent == 0 {
		fmt.Println("❌ CRITICAL: No messages were sent")
		os.Exit(1)
	} else if errorRate > 10 {
		fmt.Printf("❌ CRITICAL: Error rate too high (%.1f%%)\n", errorRate)
		os.Exit(1)
	} else if results.Errors > 0 {
		fmt.Printf("⚠️  Warning: %d errors occurred (%.1f%% error rate)\n", results.Errors, errorRate)
	}
	
	fmt.Println("✅ Benchmark completed successfully!")
}