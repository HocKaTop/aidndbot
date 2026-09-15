package server

import "time"

type Activity struct {
	Processing bool   `json:"processing"`
	UserID     int64  `json:"userId"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
}
type operationKey struct{}
type receiptKey struct{}
type commandReceipt struct {
	Author int64
	Events []Event
}

func (h *Hub) sendActivity(id string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		h.send(c, Frame{"room_activity", r.activity})
	}
}

func (h *Hub) sendInitialState(id string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		h.send(c, Frame{"room_state", struct {
			Activity
			Refresh bool `json:"refresh"`
		}{r.activity, true}})
	}
}

func (h *Hub) Activity(id string) Activity {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		return r.activity
	}
	return Activity{}
}

func (h *Hub) beginActivity(id string, user int64, name, kind string) func() {
	h.mu.Lock()
	r := h.runtimeLocked(id)
	r.activityID++
	version := r.activityID
	r.activity = Activity{true, user, name, kind}
	r.last = time.Now()
	for c := range r.clients {
		h.send(c, Frame{"room_activity", r.activity})
	}
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if r.activityID != version {
			return
		}
		r.activity = Activity{}
		r.last = time.Now()
		for c := range r.clients {
			h.send(c, Frame{"room_activity", r.activity})
		}
	}
}
