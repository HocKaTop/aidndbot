package server

import (
	"context"
	"dnd-bot/backend/internal/auth"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Frame struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}
type Command struct {
	ID           string `json:"id,omitempty"`
	ExpectedTurn *int   `json:"expectedTurn,omitempty"`
	Type         string `json:"type"`
	Data         struct {
		Token      string `json:"token,omitempty"`
		Text       string `json:"text,omitempty"`
		Notation   string `json:"notation,omitempty"`
		ItemID     string `json:"itemId,omitempty"`
		Ability    string `json:"ability,omitempty"`
		Target     string `json:"target,omitempty"`
		ProposalID string `json:"proposalId,omitempty"`
		Ready      bool   `json:"ready,omitempty"`
		Turn       int    `json:"turn,omitempty"`
		Code       string `json:"code,omitempty"`
	} `json:"data"`
}
type client struct {
	conn *websocket.Conn
	user int64
	out  chan Frame
}
type job struct {
	user    int64
	command Command
	client  *client
}
type runtime struct {
	pending    map[pendingKey]*pendingCommand
	activity   Activity
	activityID uint64
	clients    map[*client]bool
	jobs       chan job
	last       time.Time
	ctx        context.Context
	cancel     context.CancelFunc
}
type Hub struct {
	mu     sync.Mutex
	rooms  map[string]*runtime
	server *Server
	ctx    context.Context
}

func NewHub(ctx context.Context, s *Server) *Hub {
	h := &Hub{rooms: map[string]*runtime{}, server: s, ctx: ctx}
	go h.cleanup()
	return h
}
func (h *Hub) cleanup() {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-h.ctx.Done():
			h.mu.Lock()
			for _, r := range h.rooms {
				for c := range r.clients {
					c.conn.CloseNow()
				}
			}
			h.mu.Unlock()
			return
		case <-tick.C:
			h.mu.Lock()
			for id, r := range h.rooms {
				if !r.activity.Processing && len(r.clients) == 0 && len(r.jobs) == 0 && time.Since(r.last) > 10*time.Minute {
					r.cancel()
					delete(h.rooms, id)
				}
			}
			h.mu.Unlock()
		}
	}
}
func (h *Hub) add(id string, c *client) *runtime {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.runtimeLocked(id)
	r.clients[c] = true
	r.last = time.Now()
	return r
}
func (h *Hub) runtimeLocked(id string) *runtime {
	r := h.rooms[id]
	if r == nil {
		roomCtx, cancel := context.WithCancel(h.ctx)
		r = &runtime{clients: map[*client]bool{}, pending: map[pendingKey]*pendingCommand{}, jobs: make(chan job, 16), ctx: roomCtx, cancel: cancel}
		h.rooms[id] = r
		go h.run(id, r)
	}
	return r
}
func (h *Hub) remove(id string, c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		delete(r.clients, c)
		r.last = time.Now()
	}
}
func (h *Hub) run(id string, r *runtime) {
	for {
		select {
		case <-r.ctx.Done():
			return
		case j, ok := <-r.jobs:
			if !ok {
				return
			}
			h.mu.Lock()
			r.last = time.Now()
			h.mu.Unlock()
			ctx, cancel := context.WithTimeout(r.ctx, 150*time.Second)
			h.commandProgress(r, j.user, j.command, "processing", "")
			e := h.server.command(ctx, id, j.user, j.command)
			cancel()
			status, message := "completed", ""
			if e != nil {
				status, message = "unknown", "Не удалось подтвердить результат. Проверь его или повтори отправку."
				var a *apiError
				if errors.As(e, &a) {
					message = a.Message
					if a.Code != "ROOM_BUSY" {
						status = "failed"
					}
				}
				if j.command.ID == "" {
					h.replyCommand(j.client, j.command, status, message)
				}
			}
			h.commandProgress(r, j.user, j.command, status, message)
			h.Publish(id, "room_state")
			h.mu.Lock()
			delete(r.pending, pendingKey{j.user, j.command.ID})
			r.last = time.Now()
			h.mu.Unlock()
		}
	}
}
func (h *Hub) send(c *client, f Frame) {
	select {
	case c.out <- f:
	default:
		c.conn.CloseNow()
	}
}
func (h *Hub) broadcast(id string, f Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		for c := range r.clients {
			h.send(c, f)
		}
	}
}
func (h *Hub) Publish(id, kind string) {
	h.broadcast(id, Frame{kind, map[string]bool{"refresh": true}})
}
func (s *Server) socket(w http.ResponseWriter, r *http.Request) {
	origin, _ := url.Parse(s.Config.AppURL)
	conn, e := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{origin.Host}})
	if e != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32768)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	var first Command
	e = wsjson.Read(ctx, conn, &first)
	cancel()
	if e != nil || first.Type != "auth" {
		conn.Close(websocket.StatusPolicyViolation, "authentication required")
		return
	}
	u, e := auth.Verify(first.Data.Token, s.Config.SessionSecret, time.Now())
	if e != nil {
		conn.Close(websocket.StatusPolicyViolation, "invalid session")
		return
	}
	rid := chi.URLParam(r, "id")
	if _, e = s.load(r.Context(), rid, u.ID); e != nil {
		conn.Close(websocket.StatusPolicyViolation, "membership required")
		return
	}
	ctx, cancel = context.WithCancel(r.Context())
	defer cancel()
	c := &client{conn: conn, user: u.ID, out: make(chan Frame, 32)}
	runtime := s.Hub.add(rid, c)
	slog.Info("websocket connected", "room", rid, "user", u.ID)
	defer slog.Info("websocket disconnected", "room", rid, "user", u.ID)
	defer s.Hub.remove(rid, c)
	go func() {
		tick := time.NewTicker(25 * time.Second)
		defer tick.Stop()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case f := <-c.out:
				writeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				e := wsjson.Write(writeCtx, conn, f)
				stop()
				if e != nil {
					return
				}
				if f.Type == "room_deleted" || f.Type == "room_left" {
					conn.Close(websocket.StatusNormalClosure, f.Type)
					return
				}
			case <-tick.C:
				pingCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				e := conn.Ping(pingCtx)
				stop()
				if e != nil {
					return
				}
			}
		}
	}()
	// Deletion or departure may race the first membership check and registration.
	if _, err := s.load(ctx, rid, u.ID); err != nil {
		s.Hub.send(c, Frame{"room_left", map[string]string{"message": "Комната недоступна"}})
	} else {
		s.Hub.sendInitialState(rid, c)
	}
	last := time.Time{}
	for {
		var cmd Command
		if e = wsjson.Read(ctx, conn, &cmd); e != nil {
			return
		}
		if _, e = auth.Verify(first.Data.Token, s.Config.SessionSecret, time.Now()); e != nil {
			conn.Close(websocket.StatusPolicyViolation, "session expired")
			return
		}
		if cmd.Type == "command_status" {
			s.Hub.send(c, Frame{"command_status", s.queryCommand(ctx, rid, u.ID, cmd.ID)})
			continue
		}
		if time.Since(last) < time.Second {
			s.Hub.replyCommand(c, cmd, "unknown", "Подожди секунду перед повторной отправкой.")
			continue
		}
		last = time.Now()
		switch cmd.Type {
		case "player_action", "attack_npc", "roll_dice", "use_item", "aid_ally", "short_rest", "class_ability", "ready", "start_game", "pass_turn", "defend_turn", "retreat", "skip_turn", "finish_game", "claim_stone", "return_to_bridge", "install_stone", "confirm_quest", "continue_quest", "reopen_quest":
		default:
			s.Hub.replyCommand(c, cmd, "failed", "Неизвестное событие")
			continue
		}
		s.Hub.submit(runtime, c, cmd)
	}
}

// Cancel the runtime without closing jobs: a concurrent socket may still submit.
// Readers receive the terminal event before the writer closes their connection.
func (h *Hub) DeleteRoom(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		delete(h.rooms, id)
		r.cancel()
		for c := range r.clients {
			h.send(c, Frame{"room_deleted", map[string]string{"message": "Владелец удалил комнату"}})
		}
	}
}
func (h *Hub) LeaveRoom(id string, user int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.rooms[id]; r != nil {
		for c := range r.clients {
			if c.user == user {
				delete(r.clients, c)
				h.send(c, Frame{"room_left", map[string]string{"message": "Ты покинул комнату"}})
			}
		}
	}
}
