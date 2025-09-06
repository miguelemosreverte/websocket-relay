package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

var (
	startTime   = time.Now()
	commitHash  = os.Getenv("COMMIT_HASH")
	buildTime   = os.Getenv("BUILD_TIME")
	version     = "2.0.0"
)

type HealthResponse struct {
	Status      string    `json:"status"`
	Version     string    `json:"version"`
	Uptime      string    `json:"uptime"`
	CommitHash  string    `json:"commit_hash,omitempty"`
	BuildTime   string    `json:"build_time,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	ServiceName string    `json:"service_name"`
}

type TransportInfo struct {
	Transport  string `json:"transport"`
	Protocol   string `json:"protocol"`
	Endpoint   string `json:"endpoint"`
	Port       int    `json:"port"`
	Available  bool   `json:"available"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(startTime).Round(time.Second)
	
	response := HealthResponse{
		Status:      "healthy",
		Version:     version,
		Uptime:      uptime.String(),
		CommitHash:  commitHash,
		BuildTime:   buildTime,
		Timestamp:   time.Now(),
		ServiceName: "websocket-relay",
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Service-Version", version)
	w.Header().Set("X-Service-Uptime", uptime.String())
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	banner := `
╔══════════════════════════════════════════════════════════╗
║                                                          ║
║  ╦ ╦┌─┐┌┐ ╔═╗┌─┐┌─┐┬┌─┌─┐┌┬┐  ╦═╗┌─┐┬  ┌─┐┬ ┬          ║
║  ║║║├┤ ├┴┐╚═╗│ ││  ├┴┐├┤  │   ╠╦╝├┤ │  ├─┤└┬┘          ║
║  ╚╩╝└─┘└─┘╚═╝└─┘└─┘┴ ┴└─┘ ┴   ╩╚═└─┘┴─┘┴ ┴ ┴           ║
║                                                          ║
╚══════════════════════════════════════════════════════════╝
`
	fmt.Fprint(w, banner)
	fmt.Fprintf(w, "\n🚀 WebSocket Relay Server v%s\n", version)
	fmt.Fprintf(w, "⏱️  Uptime: %s\n", time.Since(startTime).Round(time.Second))
	if commitHash != "" {
		fmt.Fprintf(w, "📦 Commit: %s\n", commitHash)
	}
	if buildTime != "" {
		fmt.Fprintf(w, "🔨 Built: %s\n", buildTime)
	}
	fmt.Fprintf(w, "\n📍 Available Endpoints:\n")
	fmt.Fprintf(w, "  GET /health     - Health check endpoint (JSON)\n")
	fmt.Fprintf(w, "  GET /stats      - Combined relay statistics (JSON)\n")
	fmt.Fprintf(w, "  GET /transports - Available transport protocols (JSON)\n")
	fmt.Fprintf(w, "  WS  /ws         - WebSocket endpoint (?name=NAME&room=ROOM)\n")
	fmt.Fprintf(w, "  UDP port 8081   - UDP relay endpoint\n")
	fmt.Fprintf(w, "  GET /           - This page\n")
	fmt.Fprintf(w, "\n✨ Server Status: RUNNING\n")
	fmt.Fprintf(w, "🌐 Host: %s\n", r.Host)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	udpPort := os.Getenv("UDP_PORT")
	if udpPort == "" {
		udpPort = "8081"
	}
	udpPortInt, _ := strconv.Atoi(udpPort)
	
	// Create and start the WebSocket hub
	hub := NewHub()
	go hub.Run()
	
	// Create and start the UDP relay
	udpRelay, err := NewUDPRelay(udpPortInt)
	if err != nil {
		log.Printf("Warning: Failed to start UDP relay: %v", err)
		udpRelay = nil
	} else {
		udpRelay.Start()
		log.Printf("UDP Relay started on port %s", udpPort)
	}
	
	// HTTP handlers
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/ws", HandleWebSocket(hub))
	
	// Transports endpoint - provides info about available transport protocols
	http.HandleFunc("/transports", func(w http.ResponseWriter, r *http.Request) {
		transports := []TransportInfo{
			{
				Transport: "websocket",
				Protocol:  "tcp",
				Endpoint:  fmt.Sprintf("ws://%s/ws", r.Host),
				Port:      8080,
				Available: true,
			},
			{
				Transport: "udp",
				Protocol:  "udp",
				Endpoint:  fmt.Sprintf("udp://%s:%s", r.Host[:len(r.Host)-5], udpPort), // Remove :8080 from host
				Port:      udpPortInt,
				Available: udpRelay != nil,
			},
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"transports": transports,
			"default":    "websocket",
		})
	})
	
	// Combined stats endpoint for both WebSocket and UDP relay
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := map[string]interface{}{
			"websocket": hub.GetStats(),
		}
		
		if udpRelay != nil {
			stats["udp"] = udpRelay.GetStats()
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})
	
	log.Printf("WebSocket Relay Server starting on port %s", port)
	log.Printf("Version: %s", version)
	log.Printf("WebSocket endpoint: ws://localhost:%s/ws", port)
	if udpRelay != nil {
		log.Printf("UDP endpoint: udp://localhost:%s", udpPort)
	}
	if commitHash != "" {
		log.Printf("Commit: %s", commitHash)
	}
	
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}