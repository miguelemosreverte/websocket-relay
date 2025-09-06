package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for testing
	},
}

type Client struct {
	ID       string
	Name     string
	Room     string
	Conn     *websocket.Conn
	Hub      *Hub
	Send     chan []byte
	JoinedAt time.Time
}

type Message struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Room      string    `json:"room"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type Hub struct {
	rooms      map[string]map[*Client]bool
	broadcast  chan *Message
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
	stats      *Stats
}

type Stats struct {
	TotalConnections int64     `json:"total_connections"`
	ActiveUsers      int       `json:"active_users"`
	TotalMessages    int64     `json:"total_messages"`
	BytesRelayed     int64     `json:"bytes_relayed"`
	StartTime        time.Time `json:"start_time"`
	mu               sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		rooms:      make(map[string]map[*Client]bool),
		broadcast:  make(chan *Message, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		stats: &Stats{
			StartTime: time.Now(),
		},
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if h.rooms[client.Room] == nil {
				h.rooms[client.Room] = make(map[*Client]bool)
			}
			h.rooms[client.Room][client] = true
			h.stats.TotalConnections++
			h.stats.ActiveUsers = h.countActiveUsers()
			h.mu.Unlock()
			
			// Broadcast join message
			joinMsg := &Message{
				Type:      "join",
				ID:        client.ID,
				Name:      client.Name,
				Room:      client.Room,
				Content:   fmt.Sprintf("%s joined the room", client.Name),
				Timestamp: time.Now(),
			}
			h.broadcast <- joinMsg
			log.Printf("Client %s (%s) joined room %s", client.Name, client.ID, client.Room)

		case client := <-h.unregister:
			h.mu.Lock()
			if room, ok := h.rooms[client.Room]; ok {
				if _, ok := room[client]; ok {
					delete(room, client)
					close(client.Send)
					h.stats.ActiveUsers = h.countActiveUsers()
					
					// Broadcast leave message
					leaveMsg := &Message{
						Type:      "leave",
						ID:        client.ID,
						Name:      client.Name,
						Room:      client.Room,
						Content:   fmt.Sprintf("%s left the room", client.Name),
						Timestamp: time.Now(),
					}
					h.mu.Unlock()
					h.broadcast <- leaveMsg
					log.Printf("Client %s (%s) left room %s", client.Name, client.ID, client.Room)
				} else {
					h.mu.Unlock()
				}
			} else {
				h.mu.Unlock()
			}

		case message := <-h.broadcast:
			h.mu.RLock()
			room := h.rooms[message.Room]
			h.mu.RUnlock()
			
			if room != nil {
				data, err := json.Marshal(message)
				if err != nil {
					log.Printf("Error marshaling message: %v", err)
					continue
				}
				
				h.stats.mu.Lock()
				h.stats.TotalMessages++
				h.stats.BytesRelayed += int64(len(data))
				h.stats.mu.Unlock()
				
				for client := range room {
					select {
					case client.Send <- data:
					default:
						// Client's send channel is full, close it
						h.mu.Lock()
						delete(room, client)
						close(client.Send)
						h.mu.Unlock()
					}
				}
			}
		}
	}
}

func (h *Hub) countActiveUsers() int {
	count := 0
	for _, room := range h.rooms {
		count += len(room)
	}
	return count
}

func (h *Hub) GetStats() map[string]interface{} {
	h.stats.mu.RLock()
	defer h.stats.mu.RUnlock()
	
	uptime := time.Since(h.stats.StartTime).Seconds()
	
	return map[string]interface{}{
		"total_connections": h.stats.TotalConnections,
		"active_users":      h.stats.ActiveUsers,
		"total_messages":    h.stats.TotalMessages,
		"bytes_relayed":     h.stats.BytesRelayed,
		"uptime_seconds":    uptime,
		"messages_per_second": float64(h.stats.TotalMessages) / uptime,
		"bandwidth_mbps":     (float64(h.stats.BytesRelayed) * 8) / (uptime * 1000000),
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()
	
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	
	for {
		_, messageBytes, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}
		
		var msg Message
		if err := json.Unmarshal(messageBytes, &msg); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			continue
		}
		
		// Set metadata
		msg.ID = c.ID
		msg.Name = c.Name
		msg.Room = c.Room
		msg.Timestamp = time.Now()
		if msg.Type == "" {
			msg.Type = "message"
		}
		
		c.Hub.broadcast <- &msg
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()
	
	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			
			c.Conn.WriteMessage(websocket.TextMessage, message)
			
		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func HandleWebSocket(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse query parameters
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "Anonymous"
		}
		
		room := r.URL.Query().Get("room")
		if room == "" {
			room = "global"
		}
		
		// Upgrade connection
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket upgrade error: %v", err)
			return
		}
		
		// Create client
		client := &Client{
			ID:       fmt.Sprintf("%d", time.Now().UnixNano()),
			Name:     name,
			Room:     room,
			Conn:     conn,
			Hub:      hub,
			Send:     make(chan []byte, 256),
			JoinedAt: time.Now(),
		}
		
		// Register client
		hub.register <- client
		
		// Start goroutines
		go client.WritePump()
		go client.ReadPump()
	}
}