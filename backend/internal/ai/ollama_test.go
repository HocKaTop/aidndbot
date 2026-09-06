package ai

import (
	"context"
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestOllamaFailures(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		status        int
	}{{"valid", `{"narrative":"hello","actions":[],"memory":[]}`, 200}, {"malformed", `not json`, 200}, {"unknown field", `{"narrative":"hello","actions":[],"memory":[],"hp":100}`, 200}, {"missing model", "", 404}, {"trailing JSON", `{"narrative":"hello","actions":[],"memory":[]} {}`, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": tc.content}})
			}))
			defer srv.Close()
			_, e := New(srv.URL).GenerateTurn(context.Background(), "test", Input{})
			if (tc.name == "valid") != (e == nil) {
				t.Fatal(e)
			}
		})
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := New("http://localhost:1")
	o.Client.Timeout = time.Second
	if _, e := o.GenerateTurn(ctx, "test", Input{}); e == nil {
		t.Fatal("cancelled request succeeded")
	}
}

// TestLiveOllama is opt-in and sends only an invented scene, never project secrets.
func TestLiveOllama(t *testing.T) {
	endpoint := os.Getenv("OLLAMA_SMOKE_URL")
	if endpoint == "" {
		t.Skip("set OLLAMA_SMOKE_URL to test a running model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	state := game.NewState(game.Settings{Name: "Проверка", Setting: "Тёмное фэнтези", Tone: "Таинственный"})
	state.Characters = append(state.Characters, game.NewCharacter(1, "Олег", "Человек", "Воин"))
	provider := New(endpoint)
	input := Input{State: state, PlayerID: 1, Text: "Начни приключение. Создай только сцену заброшенной таверны, без NPC и боя."}
	started := time.Now()
	out, err := provider.GenerateTurn(ctx, "qwen3:8b", input)
	if err != nil {
		t.Fatal(err)
	}
	results := []game.Result{}
	for _, a := range out.Actions {
		r, err := game.Apply(&state, 1, a)
		if err != nil {
			t.Fatal("model action rejected", err)
		}
		results = append(results, r)
	}
	if state.Scene == nil {
		t.Fatal("model did not create a scene")
	}
	input.State = state
	input.Results = results
	out, err = provider.GenerateTurn(ctx, "qwen3:8b", input)
	if err != nil || out.Narrative == "" {
		t.Fatal("narration failed", err)
	}
	t.Logf("Live qwen3:8b: validated actions and final narration in %s", time.Since(started).Round(time.Millisecond))
}
