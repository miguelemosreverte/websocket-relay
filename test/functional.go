package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type TestMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Room      string    `json:"room"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type TestClient struct {
	Name     string
	Room     string
	Conn     *websocket.Conn
	Messages []TestMessage
	mu       sync.Mutex
}

func NewTestClient(serverURL, name, room string) (*TestClient, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	u.Scheme = "ws"
	u.Path = "/ws"
	q := u.Query()
	q.Set("name", name)
	q.Set("room", room)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}

	client := &TestClient{
		Name:     name,
		Room:     room,
		Conn:     conn,
		Messages: make([]TestMessage, 0),
	}

	// Start reading messages
	go client.readMessages()

	return client, nil
}

func (c *TestClient) readMessages() {
	defer c.Conn.Close()
	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			return
		}

		var msg TestMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		c.mu.Lock()
		c.Messages = append(c.Messages, msg)
		c.mu.Unlock()
	}
}

func (c *TestClient) SendMessage(content string) error {
	msg := TestMessage{
		Type:    "message",
		Content: content,
	}
	return c.Conn.WriteJSON(msg)
}

func (c *TestClient) GetMessages() []TestMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	msgs := make([]TestMessage, len(c.Messages))
	copy(msgs, c.Messages)
	return msgs
}

func (c *TestClient) Close() {
	c.Conn.Close()
}

// Test Functions

func testBasicBroadcast(serverURL string) bool {
	fmt.Println("Testing basic broadcast...")
	
	// Create 3 clients in same room
	client1, err := NewTestClient(serverURL, "Alice", "test-room")
	if err != nil {
		log.Printf("Failed to create client1: %v", err)
		return false
	}
	defer client1.Close()

	client2, err := NewTestClient(serverURL, "Bob", "test-room")
	if err != nil {
		log.Printf("Failed to create client2: %v", err)
		return false
	}
	defer client2.Close()

	client3, err := NewTestClient(serverURL, "Charlie", "test-room")
	if err != nil {
		log.Printf("Failed to create client3: %v", err)
		return false
	}
	defer client3.Close()

	// Wait for connections to establish
	time.Sleep(500 * time.Millisecond)

	// Alice sends a message
	testMessage := "Hello from Alice!"
	if err := client1.SendMessage(testMessage); err != nil {
		log.Printf("Failed to send message: %v", err)
		return false
	}

	// Wait for message propagation
	time.Sleep(500 * time.Millisecond)

	// Check if Bob and Charlie received the message
	bobMessages := client2.GetMessages()
	charlieMessages := client3.GetMessages()

	bobReceived := false
	for _, msg := range bobMessages {
		if msg.Content == testMessage && msg.Name == "Alice" {
			bobReceived = true
			break
		}
	}

	charlieReceived := false
	for _, msg := range charlieMessages {
		if msg.Content == testMessage && msg.Name == "Alice" {
			charlieReceived = true
			break
		}
	}

	if !bobReceived {
		log.Println("Bob did not receive Alice's message")
		return false
	}
	if !charlieReceived {
		log.Println("Charlie did not receive Alice's message")
		return false
	}

	fmt.Println("✅ Basic broadcast test passed")
	return true
}

func testRoomIsolation(serverURL string) bool {
	fmt.Println("Testing room isolation...")

	// Create 2 clients in room1
	room1Client1, err := NewTestClient(serverURL, "User1", "room1")
	if err != nil {
		log.Printf("Failed to create room1Client1: %v", err)
		return false
	}
	defer room1Client1.Close()

	room1Client2, err := NewTestClient(serverURL, "User2", "room1")
	if err != nil {
		log.Printf("Failed to create room1Client2: %v", err)
		return false
	}
	defer room1Client2.Close()

	// Create 2 clients in room2
	room2Client1, err := NewTestClient(serverURL, "User3", "room2")
	if err != nil {
		log.Printf("Failed to create room2Client1: %v", err)
		return false
	}
	defer room2Client1.Close()

	room2Client2, err := NewTestClient(serverURL, "User4", "room2")
	if err != nil {
		log.Printf("Failed to create room2Client2: %v", err)
		return false
	}
	defer room2Client2.Close()

	// Wait for connections
	time.Sleep(500 * time.Millisecond)

	// Send message in room1
	room1Message := "Message for room1 only"
	if err := room1Client1.SendMessage(room1Message); err != nil {
		log.Printf("Failed to send message in room1: %v", err)
		return false
	}

	// Send message in room2
	room2Message := "Message for room2 only"
	if err := room2Client1.SendMessage(room2Message); err != nil {
		log.Printf("Failed to send message in room2: %v", err)
		return false
	}

	// Wait for propagation
	time.Sleep(500 * time.Millisecond)

	// Check room1 clients only got room1 message
	room1Client2Messages := room1Client2.GetMessages()
	for _, msg := range room1Client2Messages {
		if msg.Type == "message" && msg.Content == room2Message {
			log.Println("Room isolation failed: room1 client received room2 message")
			return false
		}
	}

	// Check room2 clients only got room2 message
	room2Client2Messages := room2Client2.GetMessages()
	for _, msg := range room2Client2Messages {
		if msg.Type == "message" && msg.Content == room1Message {
			log.Println("Room isolation failed: room2 client received room1 message")
			return false
		}
	}

	fmt.Println("✅ Room isolation test passed")
	return true
}

func testConnectionEvents(serverURL string) bool {
	fmt.Println("Testing connection events...")

	// Create first client
	client1, err := NewTestClient(serverURL, "FirstUser", "event-room")
	if err != nil {
		log.Printf("Failed to create client1: %v", err)
		return false
	}
	defer client1.Close()

	time.Sleep(200 * time.Millisecond)

	// Create second client
	client2, err := NewTestClient(serverURL, "SecondUser", "event-room")
	if err != nil {
		log.Printf("Failed to create client2: %v", err)
		return false
	}

	// Wait for join message
	time.Sleep(500 * time.Millisecond)

	// Check if client1 received join message for client2
	client1Messages := client1.GetMessages()
	joinReceived := false
	for _, msg := range client1Messages {
		if msg.Type == "join" && msg.Name == "SecondUser" {
			joinReceived = true
			break
		}
	}

	if !joinReceived {
		log.Println("Join event not received")
		return false
	}

	// Close client2 to trigger leave event
	client2.Close()
	time.Sleep(500 * time.Millisecond)

	// Check if client1 received leave message
	client1Messages = client1.GetMessages()
	leaveReceived := false
	for _, msg := range client1Messages {
		if msg.Type == "leave" && msg.Name == "SecondUser" {
			leaveReceived = true
			break
		}
	}

	if !leaveReceived {
		log.Println("Leave event not received")
		return false
	}

	fmt.Println("✅ Connection events test passed")
	return true
}

func main() {
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	fmt.Printf("Running functional tests against %s\n", serverURL)
	fmt.Println("=" + string(make([]byte, 50)) + "=")

	allPassed := true
	
	// Run tests
	if !testBasicBroadcast(serverURL) {
		allPassed = false
	}

	if !testRoomIsolation(serverURL) {
		allPassed = false
	}

	if !testConnectionEvents(serverURL) {
		allPassed = false
	}

	fmt.Println("=" + string(make([]byte, 50)) + "=")
	
	if allPassed {
		fmt.Println("✅ All functional tests passed!")
		os.Exit(0)
	} else {
		fmt.Println("❌ Some tests failed")
		os.Exit(1)
	}
}