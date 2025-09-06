package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"sort"
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

type BenchmarkResults struct {
	DurationSeconds      float64 `json:"duration_seconds"`
	TotalMessagesSent    int64   `json:"total_messages_sent"`
	TotalMessagesReceived int64   `json:"total_messages_received"`
	TotalBytesSent       int64   `json:"total_bytes_sent"`
	TotalBytesReceived   int64   `json:"total_bytes_received"`
	AvgLatencyMs         float64 `json:"avg_latency_ms"`
	P50LatencyMs         float64 `json:"p50_latency_ms"`
	P95LatencyMs         float64 `json:"p95_latency_ms"`
	P99LatencyMs         float64 `json:"p99_latency_ms"`
	MaxLatencyMs         float64 `json:"max_latency_ms"`
	MinLatencyMs         float64 `json:"min_latency_ms"`
	ConnectionCount      int     `json:"connection_count"`
	MessagesPerSecond    float64 `json:"messages_per_second"`
	BandwidthMbps        float64 `json:"bandwidth_mbps"`
	Errors               int64   `json:"errors"`
	Timestamp            string  `json:"timestamp"`
}

type BenchmarkMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ClientID  int       `json:"client_id"`
	Sequence  int64     `json:"sequence"`
	Timestamp int64     `json:"timestamp"`
	Content   string    `json:"content"`
}

var (
	totalErrors      int64
	messageSequence  int64
	pendingMessages  sync.Map // Track sent messages for latency calculation
)

func createBenchmarkClient(id int, serverURL string) (*BenchmarkClient, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	u.Scheme = "ws"
	u.Path = "/ws"
	q := u.Query()
	q.Set("name", fmt.Sprintf("bench-%d", id))
	q.Set("room", "benchmark")
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
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

			// Calculate latency if this is an echo of our message
			if msg.ClientID == c.ID && msg.Timestamp > 0 {
				latency := float64(time.Now().UnixNano()-msg.Timestamp) / 1e6 // Convert to ms
				c.Stats.mu.Lock()
				c.Stats.Latencies = append(c.Stats.Latencies, latency)
				c.Stats.mu.Unlock()
			}
		}
	}()
}

func (c *BenchmarkClient) startSending(duration time.Duration, messagesPerSecond int) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		
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
				msg := BenchmarkMessage{
					Type:      "message",
					ClientID:  c.ID,
					Sequence:  seq,
					Timestamp: time.Now().UnixNano(),
					Content:   fmt.Sprintf("Benchmark message %d from client %d", seq, c.ID),
				}
				
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

func calculatePercentile(latencies []float64, percentile float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	
	sort.Float64s(latencies)
	index := int(math.Ceil(percentile/100*float64(len(latencies)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(latencies) {
		index = len(latencies) - 1
	}
	
	return latencies[index]
}

func runBenchmark(serverURL string, numClients int, duration time.Duration) (*BenchmarkResults, error) {
	fmt.Printf("Starting benchmark with %d clients for %v\n", numClients, duration)
	
	// Create clients
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
	
	// Wait a bit for connections to stabilize
	time.Sleep(500 * time.Millisecond)
	
	// Start sending messages
	startTime := time.Now()
	messagesPerClientPerSecond := 10 // Each client sends 10 msgs/sec
	
	for _, client := range clients {
		client.startSending(duration, messagesPerClientPerSecond)
	}
	
	// Wait for benchmark duration
	time.Sleep(duration)
	
	// Stop all clients
	fmt.Println("Stopping clients...")
	for _, client := range clients {
		client.stop()
	}
	
	actualDuration := time.Since(startTime).Seconds()
	
	// Collect results
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
	
	results := &BenchmarkResults{
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
		BandwidthMbps:         float64(totalBytesSent+totalBytesReceived) * 8 / (actualDuration * 1000000),
		Errors:                atomic.LoadInt64(&totalErrors),
		Timestamp:             time.Now().UTC().Format(time.RFC3339),
	}
	
	return results, nil
}

func main() {
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}
	
	numClients := 10
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
	
	fmt.Printf("WebSocket Relay Benchmark\n")
	fmt.Printf("Server: %s\n", serverURL)
	fmt.Printf("Clients: %d\n", numClients)
	fmt.Printf("Duration: %v\n", duration)
	fmt.Println("=" + string(make([]byte, 50)) + "=")
	
	results, err := runBenchmark(serverURL, numClients, duration)
	if err != nil {
		log.Fatalf("Benchmark failed: %v", err)
	}
	
	// Print results
	fmt.Println("\nBenchmark Results:")
	fmt.Println("=" + string(make([]byte, 50)) + "=")
	fmt.Printf("Duration: %.2f seconds\n", results.DurationSeconds)
	fmt.Printf("Connections: %d\n", results.ConnectionCount)
	fmt.Printf("Messages Sent: %d\n", results.TotalMessagesSent)
	fmt.Printf("Messages Received: %d\n", results.TotalMessagesReceived)
	fmt.Printf("Messages/Second: %.2f\n", results.MessagesPerSecond)
	fmt.Printf("Bandwidth: %.2f Mbps\n", results.BandwidthMbps)
	fmt.Printf("Avg Latency: %.2f ms\n", results.AvgLatencyMs)
	fmt.Printf("P50 Latency: %.2f ms\n", results.P50LatencyMs)
	fmt.Printf("P95 Latency: %.2f ms\n", results.P95LatencyMs)
	fmt.Printf("P99 Latency: %.2f ms\n", results.P99LatencyMs)
	fmt.Printf("Max Latency: %.2f ms\n", results.MaxLatencyMs)
	fmt.Printf("Errors: %d\n", results.Errors)
	
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
	
	fmt.Printf("\nResults saved to %s\n", outputFile)
	
	// Exit with error if there were failures
	if results.Errors > 0 {
		os.Exit(1)
	}
}