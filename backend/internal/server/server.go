package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	Config Config
	Pool   *pgxpool.Pool
	AI     ai.Provider
	Hub    *Hub
}
type userKey struct{}
type apiError struct {
	Status        int
	Code, Message string
}

func (e *apiError) Error() string { return e.Message }
func bad(message string) error    { return &apiError{400, "INVALID_ACTION", message} }
func denied() error {
	return &apiError{403, "FORBIDDEN", "Нет доступа к комнате или действию"}
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) {
	var a *apiError
	if errors.As(e, &a) {
		respond(w, a.Status, map[string]any{"error": map[string]string{"code": a.Code, "message": a.Message}})
		return
	}
	if errors.Is(e, pgx.ErrNoRows) {
		respond(w, 404, map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "Комната не найдена"}})
		return
	}
	slog.Error("request failed", "error", e)
	respond(w, 500, map[string]any{"error": map[string]string{"code": "INTERNAL_ERROR", "message": "Не удалось выполнить действие"}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return bad("Некорректный JSON")
	}
	var rest any
	if d.Decode(&rest) != io.EOF {
		return bad("Ожидался один JSON объект")
	}
	return nil
}
func uid(r *http.Request) int64 { return r.Context().Value(userKey{}).(auth.User).ID }
func id(s string) (pgtype.UUID, error) {
	u, e := uuid.Parse(s)
	return pgtype.UUID{Bytes: u, Valid: e == nil}, e
}
func key(u pgtype.UUID) string { return uuid.UUID(u.Bytes).String() }
func blob(v any) []byte        { b, _ := json.Marshal(v); return b }
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if e := s.Pool.Ping(ctx); e != nil {
			respond(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		respond(w, 200, map[string]string{"status": "ok"})
	})
	r.Post("/api/auth/telegram", s.login)
	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/api/me", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, r.Context().Value(userKey{})) })
		r.Get("/api/rooms", s.listRooms)
		r.Post("/api/rooms", s.createRoom)
		r.Post("/api/rooms/join", s.joinCode)
		r.Get("/api/rooms/{id}", s.getRoom)
		r.Post("/api/rooms/{id}/join", s.joinRoom)
		r.Post("/api/rooms/{id}/leave", s.leaveRoom)
		r.Patch("/api/rooms/{id}", s.settings)
		r.Delete("/api/rooms/{id}", s.deleteRoom)
		r.Get("/api/rooms/{id}/members", s.members)
		r.Post("/api/rooms/{id}/characters", s.character)
		r.Patch("/api/characters/{id}", s.editCharacter)
		r.Get("/api/rooms/{id}/characters/me", s.myCharacter)
		r.Get("/api/rooms/{id}/quests", s.quests)
		r.Get("/api/rooms/{id}/events", s.events)
		r.Post("/api/rooms/{id}/start", s.start)
		r.Post("/api/rooms/{id}/pause", s.pause)
	})
	r.Get("/ws/rooms/{id}", s.socket)
	return r
}
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, e := auth.Verify(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.Config.SessionSecret, time.Now())
		if e != nil {
			fail(w, &apiError{401, "UNAUTHORIZED", "Открой приложение заново через Telegram"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InitData string `json:"initData"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	u, e := auth.Validate(in.InitData, s.Config.BotToken, time.Now())
	if e != nil {
		fail(w, &apiError{401, "INVALID_TELEGRAM_AUTH", "Telegram-подпись недействительна или устарела"})
		return
	}
	if e = store.New(s.Pool).UpsertUser(r.Context(), store.UpsertUserParams{ID: u.ID, FirstName: u.FirstName, Username: u.Username}); e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, map[string]any{"user": u, "token": auth.Issue(u, s.Config.SessionSecret, time.Now())})
}
