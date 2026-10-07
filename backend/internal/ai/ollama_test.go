package ai

import (
	"context"
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	if _, e := o.GenerateTurn(ctx, "test", Input{}); !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation was lost", e)
	}
}

func TestResponseFailureKinds(t *testing.T) {
	valid := `{"message":{"content":"{\"narrative\":\"Ответ\",\"actions\":[],\"memory\":[]}"}}`
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"model", 404, `{"error":"private model details"}`, ErrModelUnavailable},
		{"busy", 429, "private proxy details", ErrBusy},
		{"overload", 503, "", ErrBusy},
		{"unavailable", 500, "", ErrUnavailable},
		{"request", 400, "", ErrRejected},
		{"access", 401, "", ErrRejected},
		{"error envelope", 200, `{"error":"private server details"}`, ErrUnavailable},
		{"invalid envelope", 200, "not json", ErrInvalidResponse},
		{"trailing envelope", 200, valid + ` {}`, ErrInvalidResponse},
		{"oversized envelope", 200, valid + strings.Repeat(" ", 1<<20), ErrInvalidResponse},
		{"empty narrative", 200, `{"message":{"content":"{\"narrative\":\" \",\"actions\":[],\"memory\":[]}"}}`, ErrInvalidResponse},
		{"missing arrays", 200, `{"message":{"content":"{\"narrative\":\"Ответ\"}"}}`, ErrInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL).GenerateTurn(context.Background(), "test", Input{})
			if !errors.Is(err, tc.want) {
				t.Fatal("wrong error kind", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("server response leaked", err)
			}
		})
	}
}

func TestTimeoutWhileReadingResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	o := New(srv.URL)
	o.Client.Timeout = 30 * time.Millisecond
	_, err := o.GenerateTurn(context.Background(), "test", Input{})
	if !errors.Is(err, ErrTimeout) {
		t.Fatal("body timeout misclassified", err)
	}
}

func TestStructuredOutputConstrainsOpeningAndNarration(t *testing.T) {
	var schema struct {
		Properties struct {
			Actions struct {
				MaxItems int `json:"maxItems"`
				AnyOf    []struct {
					MinItems int `json:"minItems"`
					MaxItems int `json:"maxItems"`
					Items    []struct {
						Ref string `json:"$ref"`
					} `json:"items"`
				} `json:"anyOf"`
			} `json:"actions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(responseSchema(Input{Opening: true}), &schema); err != nil {
		t.Fatal(err)
	}
	options := schema.Properties.Actions.AnyOf
	if len(options) != 3 {
		t.Fatal("opening needs scene and quest, plus up to two NPCs")
	}
	for _, a := range options {
		if a.MinItems != len(a.Items) || a.MaxItems != len(a.Items) || a.MinItems < 2 || a.MaxItems > 4 || a.Items[0].Ref != "#/$defs/MOVE_SCENE" || a.Items[1].Ref != "#/$defs/CREATE_QUEST" {
			t.Fatal("opening must establish scene and quest in order")
		}
	}
	if err := json.Unmarshal(responseSchema(Input{Results: []game.Result{}}), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties.Actions.MaxItems != 0 {
		t.Fatal("narration must not change state")
	}
}

func TestActionSchemaSelectsTypeBeforeParameters(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(responseSchema(Input{}), &schema); err != nil {
		t.Fatal(err)
	}
	for kind, definition := range schema.Defs {
		if len(definition.Properties) != 0 && !strings.HasPrefix(string(definition.Properties), `{"type":`) {
			t.Fatalf("%s: grammar would force parameter choice before action type", kind)
		}
	}
}

func TestActionSchemaTargetsExistingObjects(t *testing.T) {
	state := game.NewState(game.Settings{})
	state.Scene = &game.Scene{Location: "mill"}
	state.Characters = []game.Character{{UserID: 1, Inventory: []game.Item{{ID: "owned"}}}, {UserID: 2, Inventory: []game.Item{{ID: "someone-elses"}}}}
	state.NPCs = []game.NPC{{ID: "local", Alive: true, Location: "mill"}, {ID: "remote", Alive: true, Location: "forest"}, {ID: "dead", Location: "mill"}}
	state.Quests = []game.Quest{{ID: "active", Status: "ACTIVE"}, {ID: "done", Status: "COMPLETED"}}
	var schema struct {
		Defs map[string]struct {
			Properties struct {
				Target struct {
					Enum []string `json:"enum"`
				} `json:"target"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(responseSchema(Input{State: state, PlayerID: 1}), &schema); err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]string{"ATTACK": "local", "SET_DISPOSITION": "local", "REMOVE_ITEM": "owned", "PROPOSE_QUEST_COMPLETION": "active", "PROPOSE_QUEST_FAILURE": "active"} {
		ids := schema.Defs[kind].Properties.Target.Enum
		if len(ids) != 1 || ids[0] != want {
			t.Fatalf("%s can target unavailable objects: %v", kind, ids)
		}
	}
	if _, direct := schema.Defs["UPDATE_QUEST"]; direct {
		t.Fatal("custom campaign can update quest without owner confirmation")
	}
	// A fresh room cannot generate references to invented objects.
	schema.Defs = nil
	if err := json.Unmarshal(responseSchema(Input{}), &schema); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ATTACK", "SET_DISPOSITION", "UPDATE_NPC", "REMOVE_ITEM", "UPDATE_QUEST"} {
		if _, exists := schema.Defs[kind]; exists {
			t.Fatalf("%s without any targets", kind)
		}
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
