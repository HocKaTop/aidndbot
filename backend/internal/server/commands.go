package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type commandKey struct{}
type commandIdentity struct {
	ID   pgtype.UUID
	Hash string
}
type commandStatus struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type pendingKey struct {
	User int64
	ID   string
}
type pendingCommand struct{ Hash, Status string }

func identifyCommand(cmd Command) (*commandIdentity, error) {
	if cmd.ID == "" {
		return nil, nil
	} // Older clients and direct text commands.
	u, err := id(cmd.ID)
	if err != nil {
		return nil, bad("Некорректный идентификатор действия")
	}
	cmd.ID = ""
	cmd.Data.Token = ""
	return &commandIdentity{u, fmt.Sprintf("%x", sha256.Sum256(blob(cmd)))}, nil
}

func readCommandReceipt(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, room pgtype.UUID, user int64, identity *commandIdentity) ([]Event, bool, error) {
	var hash string
	var data []byte
	err := db.QueryRow(ctx, "SELECT request_hash,events FROM command_receipts WHERE room_id=$1 AND user_id=$2 AND command_id=$3", room, user, identity.ID).Scan(&hash, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if identity.Hash != "" && hash != identity.Hash {
		return nil, true, &apiError{409, "COMMAND_ID_REUSED", "Этот идентификатор уже использован для другого действия."}
	}
	var events []Event
	err = json.Unmarshal(data, &events)
	return events, true, err
}

func (h *Hub) replyCommand(c *client, cmd Command, status, message string) {
	if cmd.ID != "" {
		h.send(c, Frame{"command_status", commandStatus{cmd.ID, status, message}})
	} else if message != "" {
		h.send(c, Frame{"error", map[string]string{"message": message}})
	}
}

func (h *Hub) commandProgress(r *runtime, user int64, cmd Command, status, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cmd.ID == "" {
		return
	}
	if pending := r.pending[pendingKey{user, cmd.ID}]; pending != nil {
		pending.Status = status
	}
	for c := range r.clients {
		if c.user == user {
			h.replyCommand(c, cmd, status, message)
		}
	}
}

func (h *Hub) submit(r *runtime, c *client, cmd Command) {
	identity, err := identifyCommand(cmd)
	if err != nil {
		h.replyCommand(c, cmd, "failed", err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	k := pendingKey{c.user, cmd.ID}
	if identity != nil {
		if pending := r.pending[k]; pending != nil {
			if pending.Hash != identity.Hash {
				h.replyCommand(c, cmd, "unknown", "Выполняется другое действие с этим идентификатором.")
			} else {
				h.replyCommand(c, cmd, pending.Status, "")
			}
			return
		}
	}
	select {
	case <-r.ctx.Done():
		h.replyCommand(c, cmd, "unknown", "Комната отключилась. Проверь результат после подключения.")
	case r.jobs <- job{c.user, cmd, c}:
		if identity != nil {
			r.pending[k] = &pendingCommand{identity.Hash, "queued"}
			h.replyCommand(c, cmd, "queued", "")
		}
	default:
		h.replyCommand(c, cmd, "unknown", "Очередь комнаты заполнена. Повтори отправку позже с тем же действием.")
	}
}

func (s *Server) queryCommand(ctx context.Context, rid string, user int64, commandID string) commandStatus {
	out := commandStatus{ID: commandID, Status: "unknown", Message: "Подтверждение пока не найдено. Можно безопасно повторить отправку."}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	identity, err := identifyCommand(Command{ID: commandID})
	if err != nil || identity == nil {
		return out
	}
	if _, err = s.load(ctx, rid, user); err != nil {
		return out
	}
	u, _ := id(rid)
	identity.Hash = ""
	_, found, err := readCommandReceipt(ctx, s.Pool, u, user, identity)
	if err != nil {
		return out
	}
	if found {
		out.Status, out.Message = "completed", ""
		return out
	}
	if s.Hub == nil {
		return out
	}
	s.Hub.mu.Lock()
	defer s.Hub.mu.Unlock()
	if r := s.Hub.rooms[rid]; r != nil {
		if pending := r.pending[pendingKey{user, commandID}]; pending != nil {
			out.Status, out.Message = pending.Status, ""
		}
	}
	return out
}
