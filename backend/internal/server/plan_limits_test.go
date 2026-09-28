package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiceCorrectionCanRemoveOrReplaceRoll(t *testing.T) {
	invalid := game.Action{Type: "DICE_ROLL", Name: "прикосновение к занавесе"}
	for _, tc := range []struct {
		name    string
		actions []game.Action
		success bool
	}{
		{"no mechanics", []game.Action{}, true},
		{"skill check", []game.Action{{Type: "SKILL_CHECK", Skill: "wisdom", DC: 12}}, true},
		{"independent roll", []game.Action{{Type: "DICE_ROLL", Name: "d20"}}, true},
		{"still invalid", []game.Action{invalid}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := game.NewState(game.Settings{OllamaModel: "test"})
			state.Characters = []game.Character{game.NewCharacter(1, "Олег", "", "")}
			in := ai.Input{State: state, PlayerID: 1, Text: "Прикоснуться к занавесе"}
			before, calls := string(blob(in.State)), 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				var got ai.Input
				if err := json.Unmarshal([]byte(request.Messages[1].Content), &got); err != nil {
					t.Error(err)
					return
				}
				actions := []game.Action{invalid}
				if calls == 2 {
					if got.Correction == nil || len(got.Correction.Actions) != 1 || got.Correction.Actions[0] != invalid || !strings.Contains(got.Correction.Reason, "DICE_ROLL") || !strings.Contains(got.Correction.Reason, "d20") {
						t.Error("original invalid roll/reason lost", got.Correction)
					}
					if !strings.Contains(request.Messages[0].Content, "Сохранять DICE_ROLL не обязательно") {
						t.Error("correction instruction missing")
					}
					actions = tc.actions
				}
				if got.Text != in.Text || string(blob(got.State)) != before || got.Results != nil {
					t.Error("correction changed state or executed mechanics")
				}
				content := blob(ai.Output{Narrative: "Ткань прохладная на ощупь.", Actions: actions, Memory: []string{}})
				json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": string(content)}})
			}))
			defer provider.Close()
			s := &Server{AI: ai.New(provider.URL)}
			out, err := s.planTurn(context.Background(), "test", in)
			if (err == nil) != tc.success || calls != 2 || string(blob(in.State)) != before {
				t.Fatal("unexpected correction result", calls, err)
			}
			if tc.success && string(blob(out.Actions)) != string(blob(tc.actions)) {
				t.Fatal("correction coerced action type", out.Actions)
			}
		})
	}
}

func TestOversizeNarrativeUsesBoundedFormatRepair(t *testing.T) {
	for _, narration := range []bool{false, true} {
		for _, repair := range []bool{false, true} {
			calls := 0
			input := ai.Input{Text: "Осматриваю следы"}
			if narration {
				input.Results = []game.Result{{Type: "SKILL_CHECK", Text: "Проверка wisdom: 15 против 12; успех: true"}}
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				var got ai.Input
				json.Unmarshal([]byte(request.Messages[1].Content), &got)
				if calls == 2 && (!strings.Contains(got.ResponseCorrection, "narrative слишком длинный") || got.Correction != nil) {
					t.Error("wrong repair channel", got)
				}
				if string(blob(got.Results)) != string(blob(input.Results)) {
					t.Error("repair changed engine results")
				}
				text := strings.Repeat("я", ai.NarrativeMaxChars+1)
				if calls == 2 && repair {
					text = "Следы ведут к мосту."
				}
				content := blob(ai.Output{Narrative: text, Actions: []game.Action{}, Memory: []string{}})
				json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": string(content)}})
			}))
			s := &Server{AI: ai.New(provider.URL)}
			var err error
			if narration {
				_, err = s.narrateTurn(context.Background(), "test", input)
			} else {
				_, err = s.planTurn(context.Background(), "test", input)
			}
			provider.Close()
			if (err == nil) != repair || calls != 2 {
				t.Fatal(narration, repair, calls, err)
			}
		}
	}
}
