package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"dnd-bot/backend/migrations"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type liveSessionRequest struct {
	Text string `json:"text"`
	Stop bool   `json:"stop,omitempty"`
}

type liveSessionCall struct {
	Phase      string        `json:"phase"`
	Correction string        `json:"correction,omitempty"`
	Actions    []game.Action `json:"actions,omitempty"`
	Error      string        `json:"error,omitempty"`
}

type liveSessionAI struct {
	provider ai.Provider
	calls    []liveSessionCall
}

func (p *liveSessionAI) GenerateTurn(ctx context.Context, model string, in ai.Input) (ai.Output, error) {
	call := liveSessionCall{Phase: "plan", Correction: in.ResponseCorrection}
	if in.Opening {
		call.Phase = "opening"
	} else if in.Results != nil {
		call.Phase = "narration"
	}
	if in.Correction != nil {
		call.Correction = in.Correction.Reason
	}
	out, err := p.provider.GenerateTurn(ctx, model, in)
	call.Actions = out.Actions
	if err != nil {
		call.Error = err.Error()
	}
	p.calls = append(p.calls, call)
	return out, err
}

// Opt-in interactive playtest. Each numbered input JSON is a bot message;
// outputs contain the exact response and persisted snapshot. A fresh schema
// isolates the session, and no Telegram transport or sender is started.
func TestLiveCustomSettingSession(t *testing.T) {
	dir, database := os.Getenv("LIVE_SESSION_DIR"), os.Getenv("TEST_DATABASE_URL")
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if dir == "" || database == "" || endpoint == "" || model == "" {
		t.Skip("set LIVE_SESSION_DIR, TEST_DATABASE_URL and OLLAMA_SMOKE_URL/MODEL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "live_custom_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("remove test schema", err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(database)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrations.Run(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var settings game.Settings
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	settings.OllamaModel = model
	provider := &liveSessionAI{provider: ai.New(endpoint)}
	s := &Server{Config: Config{BotToken: "test", SessionSecret: strings.Repeat("s", 32), Model: model, AppURL: "http://localhost"}, Pool: pool, AI: provider}
	s.Hub = NewHub(ctx, s)
	user := auth.User{ID: 991001, FirstName: "Лея"}
	q := store.New(pool)
	if err := q.UpsertUser(ctx, store.UpsertUserParams{ID: user.ID, FirstName: user.FirstName}); err != nil {
		t.Fatal(err)
	}
	hero, err := game.NewClassCharacter(user.ID, user.FirstName, "Человек", "Плут")
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.createCampaign(ctx, user.ID, settings, &hero)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path+".tmp", data, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			t.Fatal(err)
		}
	}
	write("created.json", room)
	for step := 1; ; step++ {
		path := filepath.Join(dir, fmt.Sprintf("input-%03d.json", step))
		var request liveSessionRequest
		for {
			data, err := os.ReadFile(path)
			if err == nil {
				if err := json.Unmarshal(data, &request); err != nil {
					t.Fatal(err)
				}
				break
			}
			if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("session timed out")
			case <-time.After(250 * time.Millisecond):
			}
		}
		if request.Stop {
			write("finished.json", map[string]any{"status": room.Status, "turn": room.State.Turn, "ending": room.State.Ending})
			return
		}
		before, err := s.eventList(ctx, room.ID)
		if err != nil {
			t.Fatal(err)
		}
		known := make(map[string]bool, len(before))
		for _, event := range before {
			known[event.ID] = true
		}
		provider.calls = nil
		started := time.Now()
		commandCtx, stop := context.WithTimeout(ctx, 150*time.Second)
		response, err := s.TextMessage(commandCtx, user, request.Text)
		stop()
		if err != nil {
			t.Fatal(err)
		}
		room, err = s.load(ctx, room.ID, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.eventList(ctx, room.ID)
		if err != nil {
			t.Fatal(err)
		}
		fresh := []Event{}
		for _, event := range after {
			if !known[event.ID] {
				fresh = append(fresh, event)
			}
		}
		write(fmt.Sprintf("output-%03d.json", step), map[string]any{
			"input": request.Text, "response": response, "room": room,
			"events": fresh, "aiCalls": provider.calls,
			"durationSeconds": time.Since(started).Seconds(),
		})
		t.Logf("message %d: %.1fs, turn=%d, status=%s", step, time.Since(started).Seconds(), room.State.Turn, room.Status)
	}
}
