package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// UDPRelay handles UDP-based message relay
type UDPRelay struct {
	conn       *net.UDPConn
	clients    map[string]*UDPClient // key: "ip:port:room"
	rooms      map[string]map[string]*UDPClient
	mu         sync.RWMutex
	stats      *UDPStats
	msgChan    chan *UDPMessage
	stopChan   chan bool
}

// UDPClient represents a UDP client
type UDPClient struct {
	Addr       *net.UDPAddr
	Name       string
	Room       string
	LastActive time.Time
	ID         string
}

// UDPMessage represents a message in the UDP relay
type UDPMessage struct {
	Type      string    `json:"type"`      // "join", "leave", "message", "ping"
	ID        string    `json:"id"`        // Client ID
	Name      string    `json:"name"`      // Client name
	Room      string    `json:"room"`      // Room name
	Content   string    `json:"content"`   // Message content
	Timestamp time.Time `json:"timestamp"` // Message timestamp
	Sequence  int64     `json:"sequence"`  // For ordering
	ClientID  int       `json:"client_id"` // For benchmark tracking
}

// UDPStats tracks UDP relay statistics
type UDPStats struct {
	TotalPackets   int64
	TotalBytes     int64
	ActiveClients  int64
	DroppedPackets int64
	mu             sync.RWMutex
}

// NewUDPRelay creates a new UDP relay server
func NewUDPRelay(port int) (*UDPRelay, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	// Set buffer sizes for better performance
	conn.SetReadBuffer(1024 * 1024)  // 1MB read buffer
	conn.SetWriteBuffer(1024 * 1024) // 1MB write buffer

	relay := &UDPRelay{
		conn:     conn,
		clients:  make(map[string]*UDPClient),
		rooms:    make(map[string]map[string]*UDPClient),
		stats:    &UDPStats{},
		msgChan:  make(chan *UDPMessage, 1000),
		stopChan: make(chan bool),
	}

	return relay, nil
}

// Start begins the UDP relay server
func (r *UDPRelay) Start() {
	go r.handleMessages()
	go r.cleanupInactive()
	go r.readLoop()
}

// Stop stops the UDP relay server
func (r *UDPRelay) Stop() {
	close(r.stopChan)
	r.conn.Close()
}

// readLoop reads incoming UDP packets
func (r *UDPRelay) readLoop() {
	buffer := make([]byte, 65535) // Max UDP packet size

	for {
		select {
		case <-r.stopChan:
			return
		default:
			// Set read deadline to avoid blocking forever
			r.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			
			n, addr, err := r.conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue // Timeout is expected
				}
				if err != net.ErrClosed {
					log.Printf("UDP read error: %v", err)
				}
				continue
			}

			atomic.AddInt64(&r.stats.TotalPackets, 1)
			atomic.AddInt64(&r.stats.TotalBytes, int64(n))

			// Parse the message
			var msg UDPMessage
			if err := json.Unmarshal(buffer[:n], &msg); err != nil {
				log.Printf("Failed to unmarshal UDP message: %v", err)
				atomic.AddInt64(&r.stats.DroppedPackets, 1)
				continue
			}

			// Handle the message
			r.handlePacket(addr, &msg)
		}
	}
}

// handlePacket processes an incoming UDP packet
func (r *UDPRelay) handlePacket(addr *net.UDPAddr, msg *UDPMessage) {
	clientKey := fmt.Sprintf("%s:%s", addr.String(), msg.Room)
	
	r.mu.Lock()
	client, exists := r.clients[clientKey]
	
	if !exists || msg.Type == "join" {
		// New client or explicit join
		client = &UDPClient{
			Addr:       addr,
			Name:       msg.Name,
			Room:       msg.Room,
			LastActive: time.Now(),
			ID:         msg.ID,
		}
		
		r.clients[clientKey] = client
		
		// Add to room
		if r.rooms[msg.Room] == nil {
			r.rooms[msg.Room] = make(map[string]*UDPClient)
		}
		r.rooms[msg.Room][clientKey] = client
		
		atomic.AddInt64(&r.stats.ActiveClients, 1)
		
		// Broadcast join message
		joinMsg := &UDPMessage{
			Type:      "join",
			ID:        client.ID,
			Name:      client.Name,
			Room:      msg.Room,
			Content:   fmt.Sprintf("%s joined the room", client.Name),
			Timestamp: time.Now(),
		}
		r.mu.Unlock()
		r.broadcast(msg.Room, joinMsg, nil)
		
		log.Printf("UDP client %s (%s) joined room %s", client.Name, addr.String(), msg.Room)
	} else {
		// Existing client
		client.LastActive = time.Now()
		r.mu.Unlock()
		
		if msg.Type == "leave" {
			r.removeClient(clientKey, client)
		} else if msg.Type == "message" {
			// Set server timestamp but preserve other fields for latency tracking
			msg.Timestamp = time.Now()
			msg.ID = client.ID
			msg.Name = client.Name
			r.broadcast(msg.Room, msg, nil)
		} else if msg.Type == "ping" {
			// Send pong back to client
			pong := &UDPMessage{
				Type:      "pong",
				Timestamp: time.Now(),
			}
			r.sendToClient(addr, pong)
		}
	}
}

// broadcast sends a message to all clients in a room
func (r *UDPRelay) broadcast(room string, msg *UDPMessage, except *net.UDPAddr) {
	r.mu.RLock()
	clients := r.rooms[room]
	r.mu.RUnlock()

	if clients == nil {
		return
	}

	for _, client := range clients {
		if except != nil && client.Addr.String() == except.String() {
			continue // Skip the sender if specified
		}
		
		if err := r.sendToClient(client.Addr, msg); err != nil {
			log.Printf("Failed to send to client %s: %v", client.Addr.String(), err)
			atomic.AddInt64(&r.stats.DroppedPackets, 1)
		}
	}
}

// sendToClient sends a message to a specific client
func (r *UDPRelay) sendToClient(addr *net.UDPAddr, msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	_, err = r.conn.WriteToUDP(data, addr)
	if err != nil {
		return err
	}

	atomic.AddInt64(&r.stats.TotalPackets, 1)
	atomic.AddInt64(&r.stats.TotalBytes, int64(len(data)))
	return nil
}

// removeClient removes a client from the relay
func (r *UDPRelay) removeClient(clientKey string, client *UDPClient) {
	r.mu.Lock()
	delete(r.clients, clientKey)
	if room, ok := r.rooms[client.Room]; ok {
		delete(room, clientKey)
		if len(room) == 0 {
			delete(r.rooms, client.Room)
		}
	}
	atomic.AddInt64(&r.stats.ActiveClients, -1)
	r.mu.Unlock()

	// Broadcast leave message
	leaveMsg := &UDPMessage{
		Type:      "leave",
		ID:        client.ID,
		Name:      client.Name,
		Room:      client.Room,
		Content:   fmt.Sprintf("%s left the room", client.Name),
		Timestamp: time.Now(),
	}
	r.broadcast(client.Room, leaveMsg, nil)
	
	log.Printf("UDP client %s left room %s", client.Name, client.Room)
}

// cleanupInactive removes inactive clients
func (r *UDPRelay) cleanupInactive() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopChan:
			return
		case <-ticker.C:
			r.mu.Lock()
			now := time.Now()
			toRemove := []string{}
			
			for key, client := range r.clients {
				if now.Sub(client.LastActive) > 60*time.Second {
					toRemove = append(toRemove, key)
				}
			}
			r.mu.Unlock()

			for _, key := range toRemove {
				r.mu.RLock()
				client := r.clients[key]
				r.mu.RUnlock()
				if client != nil {
					r.removeClient(key, client)
				}
			}
		}
	}
}

// handleMessages processes messages from the channel
func (r *UDPRelay) handleMessages() {
	for {
		select {
		case <-r.stopChan:
			return
		case msg := <-r.msgChan:
			// Process any queued messages if needed
			_ = msg
		}
	}
}

// GetStats returns current statistics
func (r *UDPRelay) GetStats() map[string]interface{} {
	r.stats.mu.RLock()
	defer r.stats.mu.RUnlock()

	return map[string]interface{}{
		"total_packets":   atomic.LoadInt64(&r.stats.TotalPackets),
		"total_bytes":     atomic.LoadInt64(&r.stats.TotalBytes),
		"active_clients":  atomic.LoadInt64(&r.stats.ActiveClients),
		"dropped_packets": atomic.LoadInt64(&r.stats.DroppedPackets),
		"rooms":           len(r.rooms),
	}
}