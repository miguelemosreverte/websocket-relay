package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	startTime   = time.Now()
	commitHash  = os.Getenv("COMMIT_HASH")
	buildTime   = os.Getenv("BUILD_TIME")
	version     = "1.0.0"
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
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "WebSocket Relay Server v%s\n", version)
	fmt.Fprintf(w, "Uptime: %s\n", time.Since(startTime).Round(time.Second))
	if commitHash != "" {
		fmt.Fprintf(w, "Commit: %s\n", commitHash)
	}
	fmt.Fprintf(w, "\nEndpoints:\n")
	fmt.Fprintf(w, "  GET /health - Health check endpoint\n")
	fmt.Fprintf(w, "  GET /       - This page\n")
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	http.HandleFunc("/health", healthHandler)
	http.HandleFunc("/", rootHandler)
	
	log.Printf("WebSocket Relay Server starting on port %s", port)
	log.Printf("Version: %s", version)
	if commitHash != "" {
		log.Printf("Commit: %s", commitHash)
	}
	
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal("Server failed to start:", err)
	}
}