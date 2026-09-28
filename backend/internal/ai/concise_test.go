package ai

import (
	"context"
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestPromptBrevityAndDiceSemantics(t *testing.T) {
	for _, in := range []Input{{}, {Opening: true}, {Results: []game.Result{}}} {
		p := Prompt(in)
		for _, rule := range []string{"1–2 коротких абзаца", "2–6 предложений", "не повторяй сцену и действие", "не решай за героя"} {
			if !strings.Contains(p, rule) {
				t.Fatalf("missing %q", rule)
			}
		}
		if strings.Contains(p, "2–4 коротких абзаца") {
			t.Fatal("old verbosity rule retained")
		}
	}
	if !strings.Contains(Prompt(Input{Opening: true}), "максимум 2–3 коротких абзаца, без длинного пролога") {
		t.Fatal("opening length missing")
	}
	if !strings.Contains(Prompt(Input{Results: []game.Result{}}), "обычно 1 короткий абзац") {
		t.Fatal("mechanics brevity missing")
	}
	p := Prompt(Input{Correction: &PlanCorrection{Actions: []game.Action{{Type: "DICE_ROLL", Name: "прикосновение к занавесе"}}}})
	for _, rule := range []string{"только самостоятельный бросок по просьбе игрока", "name — dice notation", "Для действия с характеристикой нужен SKILL_CHECK", "обычное взаимодействие — actions=[]", "ATTACK и SKILL_CHECK уже бросают кубики", "иначе удали его (actions=[]) или замени на SKILL_CHECK", "Сохранять DICE_ROLL не обязательно"} {
		if !strings.Contains(p, rule) {
			t.Fatalf("missing dice rule %q", rule)
		}
	}
}

func TestDiceSchemaUsesEngineLanguageOnlyForDice(t *testing.T) {
	var root struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(responseSchema(Input{}), &root); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(root.Properties), `{"actions":`) {
		t.Fatal("grammar must choose actions before prose")
	}
	var schema struct {
		Defs map[string]struct {
			Properties struct {
				Name struct {
					Pattern string `json:"pattern"`
				} `json:"name"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(responseSchema(Input{}), &schema); err != nil {
		t.Fatal(err)
	}
	pattern := schema.Defs["DICE_ROLL"].Properties.Name.Pattern
	if pattern != game.DiceNotationPattern {
		t.Fatal("schema drifted from engine")
	}
	re := regexp.MustCompile(pattern)
	for _, value := range []string{"d20", "2d6+3", "1d8-1", "01d0006+003", "20d1000-100", "прикосновение к занавесе", "проверка d20", "", "21d6", "d1001", "d6+101"} {
		_, err := game.ParseDice(value)
		if re.MatchString(value) != (err == nil) {
			t.Fatalf("schema/parser disagreement: %q", value)
		}
	}
	for _, kind := range []string{"MOVE_SCENE", "CREATE_NPC", "CREATE_QUEST", "ADD_ITEM"} {
		if schema.Defs[kind].Properties.Name.Pattern != "" {
			t.Fatalf("dice pattern leaked to %s", kind)
		}
	}
}

func TestNarrativeLimitsAndGenerationBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		in            Input
		chars, tokens int
	}{
		{"turn", Input{}, 900, 1024}, {"opening", Input{Opening: true}, 1400, 1280},
		{"results", Input{Results: []game.Result{{Type: "SKILL_CHECK"}}}, 900, 768},
		{"epilogue", Input{Results: []game.Result{{Type: "UPDATE_QUEST", Text: "Квест — COMPLETED"}}}, 900, 768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				narrative := strings.Repeat("я", tc.chars+extra)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						Options struct {
							NumPredict int `json:"num_predict"`
						} `json:"options"`
						Format struct {
							Properties struct {
								Narrative struct {
									MaxLength int `json:"maxLength"`
								} `json:"narrative"`
							} `json:"properties"`
						} `json:"format"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.Options.NumPredict != tc.tokens || request.Format.Properties.Narrative.MaxLength != tc.chars {
						t.Error("wrong request limits", request)
					}
					content, _ := json.Marshal(Output{Narrative: narrative, Actions: []game.Action{}, Memory: []string{}})
					json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": string(content)}})
				}))
				out, err := New(srv.URL).GenerateTurn(context.Background(), "test", tc.in)
				srv.Close()
				if extra == 0 && (err != nil || out.Narrative != narrative) {
					t.Fatal("valid Unicode narrative rejected/changed", err)
				}
				if extra == 1 && (!errors.Is(err, ErrInvalidResponse) || out.Narrative != "" || !strings.Contains(err.Error(), "narrative слишком длинный")) {
					t.Fatal("oversize narrative was sliced or accepted", err)
				}
			}
		})
	}
}
