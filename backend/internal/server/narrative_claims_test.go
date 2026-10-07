package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"strings"
	"testing"
)

func claimTestState() game.State {
	state := game.NewState(game.Settings{Name: "Экспедиция"})
	state.Scene = &game.Scene{ID: "hall", Title: "Коридор", Location: "Коридор"}
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.NPCs = []game.NPC{{ID: "guard", Name: "Стражник", Alive: true, HP: 12, Location: "Коридор"}}
	return state
}

func TestNarrationRepairsInventedWorldChange(t *testing.T) {
	state := claimTestState()
	calls := 0
	s := &Server{AI: openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		if calls == 1 {
			return ai.Output{Narrative: "Ты уже в подвале, стражник мёртв, ключ лежит в твоём инвентаре.", Actions: []game.Action{}}, nil
		}
		if !strings.Contains(in.ResponseCorrection, "перехода") {
			t.Fatal("missing correction for invented location", in.ResponseCorrection)
		}
		return ai.Output{Narrative: "Стражник остаётся в коридоре. Подвал пока впереди.", Actions: []game.Action{}}, nil
	})}
	out, err := s.narrateTurn(context.Background(), "room", ai.Input{State: state, PlayerID: 1, Results: []game.Result{{Type: "SKILL_CHECK", Text: "Проверка завершена"}}})
	if err != nil || calls != 2 || !strings.Contains(out.Narrative, "остаётся") {
		t.Fatal("narration was not repaired", out, calls, err)
	}
}

func TestNarrativeClaimsRequireRealWorldEffects(t *testing.T) {
	state := claimTestState()
	for _, text := range []string{
		"Ты уже в подвале.",
		"Ты вошёл в подвал.",
		"Вы добираетесь до старой мельницы.",
		"Вы вошли в старую заброшенную мельницу.",
		"Стражник мёртв.",
		"Ключ лежит в твоём инвентаре.",
		"Бой начался.",
		"Бой начинается.",
		"Ты уже в подвале, стражник мёртв, ключ лежит в твоём инвентаре.",
	} {
		if err := validateStoryNarrative(state, 1, nil, nil, text); err == nil {
			t.Errorf("invented effect was accepted: %q", text)
		}
	}
	for _, text := range []string{"Ты уже в опасности.", "Ты вошёл в доверие стражника из подвала.", "Стражник не мёртв.", "Ключ виден под столом."} {
		if err := validateStoryNarrative(state, 1, nil, nil, text); err != nil {
			t.Errorf("ordinary narration rejected: %q: %v", text, err)
		}
	}
	state.NPCs = append(state.NPCs, game.NPC{ID: "dragon", Name: "Дракон", Alive: false, Location: "Коридор"})
	if err := validateStoryNarrative(state, 1, nil, nil, "Стражник говорит, что дракон мёртв."); err != nil {
		t.Fatal("speaker was mistaken for dead NPC", err)
	}
}

func TestLocationMentionInDescriptionDoesNotConfirmArrival(t *testing.T) {
	state := claimTestState()
	state.Scene.Description = "Из коридора видно лестницу в подвал."
	if err := validateStoryNarrative(state, 1, nil, nil, "Вы уже в подвале."); err == nil {
		t.Fatal("a distant place mentioned in the description was treated as the current place")
	}
}

func TestNarrativeClaimsAcceptConfirmedChanges(t *testing.T) {
	state := claimTestState()
	state.Scene = &game.Scene{ID: "basement", Title: "Подвал", Location: "Подвал"}
	state.NPCs[0].Alive = false
	state.Characters[0].Inventory = append(state.Characters[0].Inventory, game.Item{ID: "key", Name: "Ключ", Quantity: 1})
	state.Combat = true
	for _, text := range []string{
		"Ты уже в подвале.",
		"Стражник мёртв.",
		"Ключ лежит в твоём инвентаре.",
		"Бой начался.",
	} {
		if err := validateStoryNarrative(state, 1, nil, []game.Result{}, text); err != nil {
			t.Errorf("confirmed effect rejected: %q: %v", text, err)
		}
	}
	old := claimTestState()
	if err := validateStoryNarrative(old, 1, []game.Action{{Type: "ATTACK", Target: "guard"}}, nil, "Стражник мёртв."); err == nil {
		t.Fatal("planned attack was mistaken for confirmed death")
	}
	if err := validateStoryNarrative(old, 1, []game.Action{{Type: "SKILL_CHECK", Skill: "wisdom", DC: 10}, {Type: "ADD_ITEM", Name: "Ключ"}}, nil, "Ключ лежит в твоём инвентаре."); err == nil {
		t.Fatal("unresolved check was mistaken for acquired item")
	}
}

func TestOpeningLocationClaimMatchesCreatedScene(t *testing.T) {
	state := game.NewState(game.Settings{})
	actions := []game.Action{{Type: "MOVE_SCENE", Name: "Таверна", Description: "Горит очаг."}}
	if err := validateStoryNarrative(state, 1, actions, nil, "Ты уже в подвале."); err == nil {
		t.Fatal("opening narrative described another location")
	}
	if err := validateStoryNarrative(state, 1, actions, nil, "Ты уже в таверне."); err != nil {
		t.Fatal("opening narrative matching its scene was rejected", err)
	}
}
