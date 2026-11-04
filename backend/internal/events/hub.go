package events

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// MonitorUpdate is broadcast to all connected websocket clients whenever a
// check completes (and flagged when the up/down state actually changed).
type MonitorUpdate struct {
	Type      string    `json:"type"`
	MonitorID string    `json:"monitorId"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	LatencyMs *float64  `json:"latencyMs,omitempty"`
	Message   string    `json:"message,omitempty"`
	Changed   bool      `json:"changed"`
	At        time.Time `json:"at"`
}

// IncidentUpdate is broadcast when an incident is created/updated.
type IncidentUpdate struct {
	Type       string `json:"type"`
	IncidentID string `json:"incidentId"`
	MonitorID  string `json:"monitorId"`
	Status     string `json:"status"`
	Headline   string `json:"headline"`
}

// CheckStarted is broadcast when a monitor's check begins running, so the
// matching UI node can show a per-node "checking" indicator immediately —
// before the result arrives. Only that one card updates; the rest stay put.
type CheckStarted struct {
	Type      string `json:"type"`
	MonitorID string `json:"monitorId"`
}

type client struct {
	send chan []byte
}

// Hub fans out events to all connected websocket clients without requiring
// the frontend to poll or reload.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]bool
}

func NewHub() *Hub {
	return &Hub{clients: map[*client]bool{}}
}

func (h *Hub) Register() chan []byte {
	c := &client{send: make(chan []byte, 32)}
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	return c.send
}

func (h *Hub) Unregister(ch chan []byte) {
	h.mu.Lock()
	for c := range h.clients {
		if c.send == ch {
			delete(h.clients, c)
			close(c.send)
			break
		}
	}
	h.mu.Unlock()
}

func (h *Hub) broadcast(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[hub] marshal error: %v", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// drop message for slow consumers instead of blocking the whole fan-out
		}
	}
}

func (h *Hub) Broadcast(u MonitorUpdate)          { h.broadcast(u) }
func (h *Hub) BroadcastIncident(u IncidentUpdate) { h.broadcast(u) }
func (h *Hub) BroadcastCheckStarted(monitorID string) {
	h.broadcast(CheckStarted{Type: "monitor.check_started", MonitorID: monitorID})
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
