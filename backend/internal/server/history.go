package server

import (
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type historyEvent struct {
	Event
	Sequence string `json:"sequence"`
}
type historyPage struct {
	Events     []historyEvent `json:"events"`
	NextCursor *string        `json:"nextCursor"`
}

// Cursor pagination stays stable when new turns arrive while reading old ones.
// Sequence values travel as strings so JS cannot round PostgreSQL bigint IDs.
func (s *Server) eventPage(w http.ResponseWriter, r *http.Request) {
	rid := chi.URLParam(r, "id")
	if _, err := s.load(r.Context(), rid, uid(r)); err != nil {
		fail(w, err)
		return
	}
	query := r.URL.Query()
	before, after := query.Has("before"), query.Has("after")
	if before && after {
		fail(w, bad("Укажи только одно направление истории"))
		return
	}
	limit := 50
	if query.Has("limit") {
		var err error
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			fail(w, bad("Размер страницы должен быть от 1 до 100"))
			return
		}
	}
	var cursor int64
	if before || after {
		name := "before"
		if after {
			name = "after"
		}
		var err error
		cursor, err = strconv.ParseInt(query.Get(name), 10, 64)
		if err != nil || cursor < 0 || (before && cursor == 0) {
			fail(w, bad("Некорректный курсор истории"))
			return
		}
	}
	u, _ := id(rid)
	q := store.New(s.Pool)
	var rows []store.GameEvent
	var err error
	if after {
		rows, err = q.LaterEvents(r.Context(), store.LaterEventsParams{RoomID: u, Sequence: cursor, Limit: int32(limit + 1)})
	} else {
		rows, err = q.EarlierEvents(r.Context(), store.EarlierEventsParams{RoomID: u, BeforeSequence: pgtype.Int8{Int64: cursor, Valid: before}, Limit: int32(limit + 1)})
	}
	if err != nil {
		fail(w, err)
		return
	}
	page := historyPage{Events: []historyEvent{}}
	if len(rows) > limit {
		rows = rows[:limit]
		next := strconv.FormatInt(rows[len(rows)-1].Sequence, 10)
		page.NextCursor = &next
	}
	if !after {
		slices.Reverse(rows)
	}
	for _, v := range rows {
		page.Events = append(page.Events, historyEvent{Event{key(v.ID), v.Type, json.RawMessage(v.Payload), v.CreatedAt.Time.Format("2006-01-02T15:04:05.999999Z07:00")}, strconv.FormatInt(v.Sequence, 10)})
	}
	respond(w, 200, page)
}
