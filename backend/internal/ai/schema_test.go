package ai

import (
	"context"
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestResponseSchemaOmitsUnavailableMechanicalBranches(t *testing.T) {
	state := game.NewState(game.Settings{
		Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень.",
	})
	state.Scene = &game.Scene{Title: "Деревня", Location: "Деревня"}
	for _, tc := range []struct {
		name string
		text string
		want int
	}{
		{"ordinary action without NPC", "Прислушиваюсь к шёпоту", 5},
		{"explicit dice request", "Брось d20", 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(responseSchema(Input{State: state, Text: tc.text}), &document); err != nil {
				t.Fatal(err)
			}
			var check func(any)
			check = func(v any) {
				switch value := v.(type) {
				case map[string]any:
					if options, present := value["anyOf"]; present && len(options.([]any)) == 0 {
						t.Fatal("empty anyOf produces an invalid Ollama grammar")
					}
					for _, child := range value {
						check(child)
					}
				case []any:
					for _, child := range value {
						check(child)
					}
				}
			}
			check(document)
			actions := document["properties"].(map[string]any)["actions"].(map[string]any)
			if got := len(actions["anyOf"].([]any)); got != tc.want {
				t.Fatalf("got %d action alternatives, want %d", got, tc.want)
			}
		})
	}
}

func TestQuestProposalOnlyAvailableInSafeCustomStory(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     Input
		available bool
	}{
		{"custom quest", Input{State: game.State{Settings: game.Settings{Name: "Экспедиция"}, Quests: []game.Quest{{ID: "goal", Status: "ACTIVE"}}}}, true},
		{"before opening", Input{Opening: true, State: game.State{Settings: game.Settings{Name: "Экспедиция"}, Quests: []game.Quest{{ID: "goal", Status: "ACTIVE"}}}}, false},
		{"no goals", Input{State: game.State{Settings: game.Settings{Name: "Экспедиция"}}}, false},
		{"failed goal", Input{State: game.State{Settings: game.Settings{Name: "Экспедиция"}, Quests: []game.Quest{{ID: "goal", Status: "FAILED"}}}}, false},
		{"combat", Input{State: game.State{Settings: game.Settings{Name: "Экспедиция"}, Combat: true, Quests: []game.Quest{{ID: "goal", Status: "COMPLETED"}}}}, false},
		{"already proposed", Input{State: game.State{Settings: game.Settings{Name: "Экспедиция"}, Quests: []game.Quest{{ID: "goal", Status: "ACTIVE"}}, PendingQuestCompletion: &game.QuestCompletionProposal{QuestID: "goal", Reason: "Done"}}}, false},
		{"tutorial", Input{State: game.State{Settings: game.Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень."}, Quests: []game.Quest{{ID: "goal", Status: "ACTIVE"}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var schema struct {
				Defs map[string]json.RawMessage `json:"$defs"`
			}
			if err := json.Unmarshal(responseSchema(tc.input), &schema); err != nil {
				t.Fatal(err)
			}
			_, present := schema.Defs["PROPOSE_QUEST_COMPLETION"]
			if present != tc.available {
				t.Fatal("unexpected proposal action availability", present)
			}
			if _, direct := schema.Defs["FINISH_CAMPAIGN"]; direct {
				t.Fatal("model can still finish campaign directly")
			}
		})
	}
}

func TestPhysicalItemSchemaRequiresTheEffectOrNoTransfer(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Архив"})
	state.Characters = []game.Character{game.NewCharacter(1, "Лея", "", "")}
	for _, tc := range []struct {
		intent PlayerIntent
		kind   string
	}{
		{PlayerIntent{ItemRequest: "цилиндр"}, "ADD_ITEM"},
		{PlayerIntent{GiveItemID: state.Characters[0].Inventory[0].ID}, "REMOVE_ITEM"},
	} {
		var schema struct {
			Properties struct {
				Actions struct {
					AnyOf []struct {
						MaxItems int             `json:"maxItems"`
						Items    json.RawMessage `json:"items"`
					} `json:"anyOf"`
				} `json:"actions"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(responseSchema(Input{State: state, PlayerID: 1, Intent: &tc.intent}), &schema); err != nil {
			t.Fatal(err)
		}
		for _, branch := range schema.Properties.Actions.AnyOf {
			if branch.MaxItems == 0 {
				continue // Refusal/dialogue cannot mechanically transfer anything.
			}
			var items []map[string]json.RawMessage
			if err := json.Unmarshal(branch.Items, &items); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, item := range items {
				if string(item["$ref"]) == `"#/$defs/`+tc.kind+`"` {
					count++
				}
			}
			if count != 1 {
				t.Fatal("Ollama can select a check without its inventory consequence", tc.kind, string(branch.Items))
			}
		}
	}
}

// Opt-in smoke test for the installed Ollama grammar. Uses a synthetic room.
func TestLiveLastLanternWithoutAttackTarget(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	state := game.NewState(game.Settings{
		Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень.", OllamaModel: model,
	})
	state.Scene = &game.Scene{Title: "Деревня", Location: "Деревня", Description: "Туман у моста."}
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "quest", Title: "Вернуть свет", Status: "ACTIVE"}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	out, err := New(endpoint).GenerateTurn(ctx, model, Input{State: state, PlayerID: 1, Text: "Я прислушиваюсь к шёпоту у моста."})
	if err != nil {
		t.Fatal(err)
	}
	if out.Narrative == "" {
		t.Fatal("empty response")
	}
}
