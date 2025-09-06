package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type TransportType string

const (
	WebSocketTransport TransportType = "websocket"
	UDPTransport       TransportType = "udp"
)

type BenchmarkClient interface {
	Connect() error
	SendMessage(msg interface{}) error
	ReadMessage() ([]byte, error)
	Close() error
	GetID() int
}

type WebSocketClient struct {
	ID   int
	Name string
	Room string
	URL  string
	Conn *websocket.Conn
	mu   sync.Mutex
}

func (c *WebSocketClient) GetID() int {
	return c.ID
}

func (c *WebSocketClient) Connect() error {
	u, err := url.Parse(c.URL)
	if err != nil {
		return err
	}

	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/ws"

	q := u.Query()
	q.Set("name", c.Name)
	q.Set("room", c.Room)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}

	c.Conn = conn
	return nil
}

func (c *WebSocketClient) SendMessage(msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteJSON(msg)
}

func (c *WebSocketClient) ReadMessage() ([]byte, error) {
	_, message, err := c.Conn.ReadMessage()
	return message, err
}

func (c *WebSocketClient) Close() error {
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

type UDPClient struct {
	ID         int
	Name       string
	Room       string
	ServerAddr *net.UDPAddr
	Conn       *net.UDPConn
	mu         sync.Mutex
	joined     bool
}

func (c *UDPClient) GetID() int {
	return c.ID
}

func (c *UDPClient) Connect() error {
	conn, err := net.DialUDP("udp", nil, c.ServerAddr)
	if err != nil {
		return err
	}
	c.Conn = conn

	// Send join message
	joinMsg := map[string]interface{}{
		"type": "join",
		"id":   fmt.Sprintf("bench-udp-%d", c.ID),
		"name": c.Name,
		"room": c.Room,
	}

	data, err := json.Marshal(joinMsg)
	if err != nil {
		return err
	}

	_, err = c.Conn.Write(data)
	if err != nil {
		return err
	}

	c.joined = true
	return nil
}

func (c *UDPClient) SendMessage(msg interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Add required fields for UDP messages
	if msgMap, ok := msg.(map[string]interface{}); ok {
		msgMap["id"] = fmt.Sprintf("bench-udp-%d", c.ID)
		msgMap["name"] = c.Name
		msgMap["room"] = c.Room
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = c.Conn.Write(data)
	return err
}

func (c *UDPClient) ReadMessage() ([]byte, error) {
	buffer := make([]byte, 65535)
	c.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := c.Conn.Read(buffer)
	if err != nil {
		return nil, err
	}
	return buffer[:n], nil
}

func (c *UDPClient) Close() error {
	if c.Conn != nil && c.joined {
		// Send leave message
		leaveMsg := map[string]interface{}{
			"type": "leave",
			"id":   fmt.Sprintf("bench-udp-%d", c.ID),
			"name": c.Name,
			"room": c.Room,
		}
		data, _ := json.Marshal(leaveMsg)
		c.Conn.Write(data)
		
		return c.Conn.Close()
	}
	return nil
}

type BenchmarkMetrics struct {
	Transport          string  `json:"transport"`
	MessagesPerSecond  float64 `json:"messages_per_second"`
	BytesPerSecond     float64 `json:"bytes_per_second"`
	AverageLatency     float64 `json:"average_latency_ms"`
	P50Latency         float64 `json:"p50_latency_ms"`
	P95Latency         float64 `json:"p95_latency_ms"`
	P99Latency         float64 `json:"p99_latency_ms"`
	TotalMessages      int64   `json:"total_messages"`
	TotalBytes         int64   `json:"total_bytes"`
	FailedConnections  int     `json:"failed_connections"`
	DroppedMessages    int64   `json:"dropped_messages"`
	TestDuration       float64 `json:"test_duration_seconds"`
}

func runTransportBenchmark(transport TransportType, serverURL string, numClients int, duration time.Duration) (*BenchmarkMetrics, error) {
	var clients []BenchmarkClient
	var failedConnections int
	var totalMessages int64
	var totalBytes int64
	var droppedMessages int64
	var latencies []float64
	var latencyMu sync.Mutex
	sentMessages := &sync.Map{}

	// Parse server URL to get UDP address if needed
	var udpAddr *net.UDPAddr
	if transport == UDPTransport {
		// Get transport info from server
		resp, err := http.Get(serverURL + "/transports")
		if err != nil {
			return nil, fmt.Errorf("failed to get transport info: %v", err)
		}
		defer resp.Body.Close()

		var transportInfo struct {
			Transports []struct {
				Transport string `json:"transport"`
				Port      int    `json:"port"`
			} `json:"transports"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&transportInfo); err != nil {
			return nil, fmt.Errorf("failed to decode transport info: %v", err)
		}

		// Find UDP port
		udpPort := 8081
		for _, t := range transportInfo.Transports {
			if t.Transport == "udp" {
				udpPort = t.Port
				break
			}
		}

		// Parse server host
		u, _ := url.Parse(serverURL)
		host := u.Hostname()
		
		udpAddr, err = net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, udpPort))
		if err != nil {
			return nil, fmt.Errorf("failed to resolve UDP address: %v", err)
		}
	}

	// Create clients
	for i := 0; i < numClients; i++ {
		var client BenchmarkClient
		
		switch transport {
		case WebSocketTransport:
			client = &WebSocketClient{
				ID:   i,
				Name: fmt.Sprintf("bench-%d", i),
				Room: "benchmark",
				URL:  serverURL,
			}
		case UDPTransport:
			client = &UDPClient{
				ID:         i,
				Name:       fmt.Sprintf("bench-udp-%d", i),
				Room:       "benchmark",
				ServerAddr: udpAddr,
			}
		}

		if err := client.Connect(); err != nil {
			log.Printf("Client %d failed to connect: %v", i, err)
			failedConnections++
			continue
		}

		clients = append(clients, client)

		// Start reader goroutine
		go func(c BenchmarkClient) {
			for {
				data, err := c.ReadMessage()
				if err != nil {
					if !strings.Contains(err.Error(), "use of closed network connection") {
						atomic.AddInt64(&droppedMessages, 1)
					}
					return
				}

				var msg map[string]interface{}
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}

				// Track received messages for latency calculation
				if msgType, ok := msg["type"].(string); ok && msgType == "message" {
					if content, ok := msg["content"].(string); ok {
						clientName := fmt.Sprintf("bench-%d", c.GetID())
						if transport == UDPTransport {
							clientName = fmt.Sprintf("bench-udp-%d", c.GetID())
						}

						if strings.HasPrefix(content, "Message from "+clientName) {
							msgKey := fmt.Sprintf("%s:%s", clientName, content)
							if sendTime, ok := sentMessages.LoadAndDelete(msgKey); ok {
								latency := time.Since(sendTime.(time.Time)).Seconds() * 1000
								latencyMu.Lock()
								latencies = append(latencies, latency)
								latencyMu.Unlock()
							}
						}
					}
				}

				atomic.AddInt64(&totalBytes, int64(len(data)))
			}
		}(client)
	}

	log.Printf("[%s] Connected %d/%d clients", transport, len(clients), numClients)

	// Start sending messages
	startTime := time.Now()
	stopChan := make(chan bool)
	var wg sync.WaitGroup

	// Message sender goroutines
	for _, client := range clients {
		wg.Add(1)
		go func(c BenchmarkClient) {
			defer wg.Done()
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			messageCount := 0
			for {
				select {
				case <-stopChan:
					return
				case <-ticker.C:
					clientName := fmt.Sprintf("bench-%d", c.GetID())
					if transport == UDPTransport {
						clientName = fmt.Sprintf("bench-udp-%d", c.GetID())
					}
					msgContent := fmt.Sprintf("Message from %s #%d", clientName, messageCount)
					
					msg := map[string]interface{}{
						"type":    "message",
						"content": msgContent,
					}

					sendTime := time.Now()
					if err := c.SendMessage(msg); err != nil {
						atomic.AddInt64(&droppedMessages, 1)
						continue
					}

					// Store send time for latency calculation
					msgKey := fmt.Sprintf("%s:%s", clientName, msgContent)
					sentMessages.Store(msgKey, sendTime)

					atomic.AddInt64(&totalMessages, 1)
					messageCount++
				}
			}
		}(client)
	}

	// Wait for test duration
	time.Sleep(duration)
	close(stopChan)
	wg.Wait()

	// Clean up
	for _, client := range clients {
		client.Close()
	}

	testDuration := time.Since(startTime).Seconds()

	// Calculate metrics
	metrics := &BenchmarkMetrics{
		Transport:         string(transport),
		TotalMessages:     totalMessages,
		TotalBytes:        totalBytes,
		MessagesPerSecond: float64(totalMessages) / testDuration,
		BytesPerSecond:    float64(totalBytes) / testDuration,
		FailedConnections: failedConnections,
		DroppedMessages:   droppedMessages,
		TestDuration:      testDuration,
	}

	// Calculate latency percentiles
	if len(latencies) > 0 {
		latencyMu.Lock()
		defer latencyMu.Unlock()

		// Sort latencies for percentile calculation
		sortedLatencies := make([]float64, len(latencies))
		copy(sortedLatencies, latencies)
		
		// Simple bubble sort for percentiles
		for i := 0; i < len(sortedLatencies); i++ {
			for j := i + 1; j < len(sortedLatencies); j++ {
				if sortedLatencies[i] > sortedLatencies[j] {
					sortedLatencies[i], sortedLatencies[j] = sortedLatencies[j], sortedLatencies[i]
				}
			}
		}

		// Calculate average
		sum := 0.0
		for _, l := range sortedLatencies {
			sum += l
		}
		metrics.AverageLatency = sum / float64(len(sortedLatencies))

		// Calculate percentiles
		p50Index := len(sortedLatencies) * 50 / 100
		p95Index := len(sortedLatencies) * 95 / 100
		p99Index := len(sortedLatencies) * 99 / 100

		if p50Index < len(sortedLatencies) {
			metrics.P50Latency = sortedLatencies[p50Index]
		}
		if p95Index < len(sortedLatencies) {
			metrics.P95Latency = sortedLatencies[p95Index]
		}
		if p99Index < len(sortedLatencies) {
			metrics.P99Latency = sortedLatencies[p99Index]
		}
	}

	return metrics, nil
}

func main() {
	var serverURL string
	var numClients int
	var duration time.Duration
	var outputFile string
	var transport string

	flag.StringVar(&serverURL, "server", os.Getenv("SERVER_URL"), "Server URL")
	flag.IntVar(&numClients, "clients", 50, "Number of clients")
	flag.DurationVar(&duration, "duration", 5*time.Second, "Test duration")
	flag.StringVar(&outputFile, "output", "benchmark-transport-results.json", "Output file")
	flag.StringVar(&transport, "transport", "both", "Transport to test: websocket, udp, or both")
	flag.Parse()

	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	log.Printf("=== Transport Benchmark Configuration ===")
	log.Printf("Server: %s", serverURL)
	log.Printf("Clients: %d", numClients)
	log.Printf("Duration: %s", duration)
	log.Printf("Transport: %s", transport)
	log.Println()

	results := make(map[string]*BenchmarkMetrics)

	// Run WebSocket benchmark
	if transport == "websocket" || transport == "both" {
		log.Println("Starting WebSocket benchmark...")
		wsMetrics, err := runTransportBenchmark(WebSocketTransport, serverURL, numClients, duration)
		if err != nil {
			log.Printf("WebSocket benchmark failed: %v", err)
		} else {
			results["websocket"] = wsMetrics
			
			log.Println("\n=== WebSocket Results ===")
			log.Printf("Messages/sec: %.2f", wsMetrics.MessagesPerSecond)
			log.Printf("Bandwidth: %.2f MB/s", wsMetrics.BytesPerSecond/1024/1024)
			log.Printf("Avg Latency: %.2f ms", wsMetrics.AverageLatency)
			log.Printf("P50 Latency: %.2f ms", wsMetrics.P50Latency)
			log.Printf("P95 Latency: %.2f ms", wsMetrics.P95Latency)
			log.Printf("P99 Latency: %.2f ms", wsMetrics.P99Latency)
		}
	}

	// Run UDP benchmark
	if transport == "udp" || transport == "both" {
		// Wait a bit between tests
		if transport == "both" {
			log.Println("\nWaiting 2 seconds before UDP test...")
			time.Sleep(2 * time.Second)
		}

		log.Println("Starting UDP benchmark...")
		udpMetrics, err := runTransportBenchmark(UDPTransport, serverURL, numClients, duration)
		if err != nil {
			log.Printf("UDP benchmark failed: %v", err)
		} else {
			results["udp"] = udpMetrics
			
			log.Println("\n=== UDP Results ===")
			log.Printf("Messages/sec: %.2f", udpMetrics.MessagesPerSecond)
			log.Printf("Bandwidth: %.2f MB/s", udpMetrics.BytesPerSecond/1024/1024)
			log.Printf("Avg Latency: %.2f ms", udpMetrics.AverageLatency)
			log.Printf("P50 Latency: %.2f ms", udpMetrics.P50Latency)
			log.Printf("P95 Latency: %.2f ms", udpMetrics.P95Latency)
			log.Printf("P99 Latency: %.2f ms", udpMetrics.P99Latency)
		}
	}

	// Compare results if both were run
	if len(results) == 2 {
		log.Println("\n=== Comparison ===")
		wsMetrics := results["websocket"]
		udpMetrics := results["udp"]

		if wsMetrics != nil && udpMetrics != nil {
			latencyImprovement := ((wsMetrics.AverageLatency - udpMetrics.AverageLatency) / wsMetrics.AverageLatency) * 100
			throughputImprovement := ((udpMetrics.MessagesPerSecond - wsMetrics.MessagesPerSecond) / wsMetrics.MessagesPerSecond) * 100

			log.Printf("Latency improvement (UDP vs WebSocket): %.1f%%", latencyImprovement)
			log.Printf("Throughput improvement (UDP vs WebSocket): %.1f%%", throughputImprovement)

			if udpMetrics.AverageLatency < wsMetrics.AverageLatency {
				log.Println("🏆 UDP wins on latency!")
			} else {
				log.Println("🏆 WebSocket wins on latency!")
			}

			if udpMetrics.MessagesPerSecond > wsMetrics.MessagesPerSecond {
				log.Println("🏆 UDP wins on throughput!")
			} else {
				log.Println("🏆 WebSocket wins on throughput!")
			}
		}
	}

	// Save results to file
	file, err := os.Create(outputFile)
	if err != nil {
		log.Fatalf("Failed to create output file: %v", err)
	}
	defer file.Close()

	output := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339),
		"config": map[string]interface{}{
			"server":   serverURL,
			"clients":  numClients,
			"duration": duration.String(),
		},
		"results": results,
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		log.Fatalf("Failed to write results: %v", err)
	}

	log.Printf("\nResults saved to %s", outputFile)
}