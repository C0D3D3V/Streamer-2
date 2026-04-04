// Package websocket provides the real-time communication hub used to push
// stream status updates to viewers and the streamer without polling.
package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	gorillaws "github.com/gorilla/websocket"
	"github.com/gin-gonic/gin"
)

const (
	// writeWait is the maximum time to wait for a write to the client.
	writeWait = 10 * time.Second
	// pongWait is how long to wait for the next pong before closing the connection.
	// Proxies and NAT devices often drop idle connections after 30-60 seconds;
	// keeping pongWait below that prevents silent drops.
	pongWait = 60 * time.Second
	// pingPeriod is how often the server sends a ping to the client.
	// Must be less than pongWait.
	pingPeriod = 30 * time.Second
)

// Message is the envelope for all WebSocket messages.
type Message struct {
	// Type identifies the event, e.g. "stream.started", "stream.ended".
	Type string `json:"type"`
	// Payload contains event-specific data.
	Payload interface{} `json:"payload"`
}

// client represents a single WebSocket connection subscribed to a stream.
type client struct {
	streamID string
	conn     *gorillaws.Conn
	send     chan []byte
}

// hub maintains the set of active clients and broadcasts messages to them.
type hub struct {
	mu sync.RWMutex
	// clients maps streamID → set of connected clients.
	clients map[string]map[*client]struct{}
}

// Hub is the application-wide WebSocket hub singleton.
var Hub = &hub{
	clients: make(map[string]map[*client]struct{}),
}

// Broadcast sends a message to all clients watching a given stream.
// It is safe to call from any goroutine.
func (h *hub) Broadcast(streamID string, msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("ws hub: marshal error: %v", err)
		return
	}

	h.mu.RLock()
	clients := h.clients[streamID]
	h.mu.RUnlock()

	for c := range clients {
		select {
		case c.send <- data:
		default:
		}
	}
}

// register adds a client to the hub.
func (h *hub) register(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.streamID] == nil {
		h.clients[c.streamID] = make(map[*client]struct{})
	}
	h.clients[c.streamID][c] = struct{}{}
}

// unregister removes a client from the hub and closes its send channel.
func (h *hub) unregister(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c.streamID][c]; ok {
		delete(h.clients[c.streamID], c)
		close(c.send)
	}
}

var upgrader = gorillaws.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// HandleWS upgrades the HTTP connection to WebSocket and registers the client.
// GET /ws/stream/:streamId
func HandleWS(c *gin.Context) {
	streamID := c.Param("streamId")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws: upgrade error for stream %s: %v", streamID, err)
		return
	}

	cl := &client{
		streamID: streamID,
		conn:     conn,
		send:     make(chan []byte, 32),
	}
	Hub.register(cl)

	// writePump sends queued messages and periodic pings to keep the connection
	// alive through proxies and NAT devices that drop idle TCP connections.
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer func() {
			ticker.Stop()
			Hub.unregister(cl)
			conn.Close()
		}()
		for {
			select {
			case data, ok := <-cl.send:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if !ok {
					conn.WriteMessage(gorillaws.CloseMessage, []byte{})
					return
				}
				if err := conn.WriteMessage(gorillaws.TextMessage, data); err != nil {
					return
				}
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteMessage(gorillaws.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// readPump keeps the read loop alive so we detect client disconnects.
	// Pong responses from the client reset the read deadline.
	defer func() {
		Hub.unregister(cl)
		conn.Close()
	}()
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
