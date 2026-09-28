package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func lanternState() game.State {
	state := game.NewState(game.Settings{Name: "Последний фонарь", WorldDescription: "У моста деревни Тихий Брод пропал огненный камень."})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "objective", Title: "Вернуть свет", Status: "ACTIVE"}}
	state.Scene = &game.Scene{Title: "Старая мельница", Location: "Старая мельница"}
	return state
}

func TestLastLanternFightAndFinalePersist(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := testCampaign(t, ctx, s, 1)
	r, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Settings.Name = "Последний фонарь"
		r.State.Settings.WorldDescription = "У моста деревни Тихий Брод пропал огненный камень."
		r.State.Scene = &game.Scene{ID: uuid.NewString(), Title: "Дворец мельника", Location: "Дворец мельника"}
		r.State.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Вернуть свет", Status: "ACTIVE"}}
		r.State.NPCs = []game.NPC{{ID: uuid.NewString(), Name: "Похититель", Alive: true, HP: 40, MaxHP: 40, ArmorClass: 12, Location: "Дворец мельника", Disposition: "neutral"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Results != nil {
			return ai.Output{Narrative: "Серверные результаты описаны.", Actions: []game.Action{}}, nil
		}
		return ai.Output{Narrative: "Ход описан.", Actions: []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень"}}}, nil
	})
	if err := s.command(ctx, r.ID, 1, Command{Type: "claim_stone"}); err == nil {
		t.Fatal("stone claimed before thief yielded")
	}
	if err := s.command(ctx, r.ID, 1, Command{Type: "return_to_bridge"}); err == nil {
		t.Fatal("returned to bridge without stone")
	}
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Начать драку"
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	r, err = s.load(ctx, r.ID, 1)
	if err != nil || !r.State.Combat || r.State.NPCs[0].Disposition != "hostile" || r.State.Turn != 1 {
		t.Fatal("battle was only narrated", err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Type] = true
	}
	if !seen["START_COMBAT"] || !seen["ATTACK"] || !seen["NPC_ATTACK"] {
		t.Fatal("missing battle mechanics", seen)
	}
	// Bring the party home after the encounter. The rest uses normal commands.
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.Combat = false
		r.State.NPCs[0].HP = 0
		r.State.NPCs[0].Alive = false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Type = "claim_stone"
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	r, err = s.load(ctx, r.ID, 1)
	if err != nil || r.Status != "PLAYING" {
		t.Fatal("quest finished before installation", err)
	}
	cmd.Type = "return_to_bridge"
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	cmd.Type = "install_stone"
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	r, err = s.load(ctx, r.ID, 1)
	if err != nil || r.Status != "FINISHED" || r.State.Ending == nil || r.State.Ending.Reason != "objective" || r.State.Quests[0].Status != "COMPLETED" {
		t.Fatal("verified goal did not finish adventure", err)
	}
	events, err = s.eventList(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen = map[string]bool{}
	for _, event := range events {
		seen[event.Type] = true
	}
	if !seen["REMOVE_ITEM"] || !seen["UPDATE_QUEST"] || !seen["GAME_FINISHED"] {
		t.Fatal("ending events missing", seen)
	}
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("finished adventure accepted another turn")
	}
}

func TestExplicitAttackStartsRealCombat(t *testing.T) {
	empty := lanternState()
	first, err := ensureLanternThief(&empty, 1)
	if err != nil || first == nil || len(empty.NPCs) != 1 {
		t.Fatal("entering mill did not create real thief", err)
	}
	second, err := ensureLanternThief(&empty, 1)
	if err != nil || second != nil || len(empty.NPCs) != 1 {
		t.Fatal("returning to mill duplicated thief", err)
	}
	empty.NPCs[0].Alive = false
	third, err := ensureLanternThief(&empty, 1)
	if err != nil || third != nil || len(empty.NPCs) != 1 {
		t.Fatal("dead thief respawned", err)
	}
	for _, text := range []string{"Начать драку", "Добить похитителя", "Я атакую похитителя"} {
		t.Run(text, func(t *testing.T) {
			r := Room{Status: "PLAYING", State: lanternState()}
			r.State.NPCs = []game.NPC{{ID: "thief", Name: "Похититель", Alive: true, HP: 40, MaxHP: 40, ArmorClass: 12, Disposition: "neutral", Location: "Старая мельница"}}
			results, handled, err := applyExplicitAttack(&r.State, 1, text)
			if err != nil || !handled || !r.State.Combat || r.State.NPCs[0].Disposition != "hostile" {
				t.Fatal("explicit attack did not start combat", results, err)
			}
			if len(results) != 3 || results[0].Type != "SET_DISPOSITION" || results[1].Type != "START_COMBAT" || results[2].Type != "ATTACK" {
				t.Fatal("missing server-owned attack roll", results)
			}
			results, err = settleTurn(&r, 1, false, results)
			initiative, response := false, false
			for _, result := range results {
				initiative = initiative || result.Type == "INITIATIVE_ROLL"
				response = response || result.Type == "NPC_ATTACK"
			}
			if err != nil || !initiative || !response || r.Status != "PLAYING" {
				t.Fatal("enemy did not respond", results, err)
			}
			if strings.Contains(combatNarrative(results), "повержен") {
				t.Fatal("narration reported death while enemy is still alive")
			}
		})
	}
	r := Room{Status: "PLAYING", State: lanternState()}
	r.State.Scene = &game.Scene{Title: "Вход во дворец мельника", Location: "Вход во дворец мельника"}
	results, handled, err := applyExplicitAttack(&r.State, 1, "Начать драку")
	if err != nil || !handled || len(r.State.NPCs) != 1 || r.State.NPCs[0].Name != "Похититель" || results[0].Type != "CREATE_NPC" {
		t.Fatal("missing scenario thief was not created as a real NPC", results, err)
	}
	if attacksExplicitly("Не хочу начинать драку") || attacksExplicitly("Я поговорю с похитителем") {
		t.Fatal("peaceful intent treated as attack")
	}
}

func TestLastLanternRequiresInstalledStoneAndEnds(t *testing.T) {
	r := Room{Status: "PLAYING", State: lanternState()}
	quest := r.State.Quests[0].ID
	if err := game.ValidateActions(&r.State, 1, []game.Action{{Type: "UPDATE_QUEST", Target: quest, Status: "COMPLETED"}}); err == nil {
		t.Fatal("model closed quest before obtaining stone")
	}
	if err := game.ValidateActions(&r.State, 1, []game.Action{{Type: "UPDATE_QUEST", Target: quest, Status: "FAILED"}}); err == nil {
		t.Fatal("one failed conversation closed the main quest")
	}
	if err := game.ValidateActions(&r.State, 1, []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень"}}); err == nil {
		t.Fatal("stone appeared without the thief yielding")
	}
	r.State.NPCs = []game.NPC{{ID: "thief", Name: "Похититель", HP: 12, Alive: true, Disposition: "friendly", Location: "Старая мельница"}}
	if _, err := game.ApplyActions(&r.State, 1, []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень"}}); err != nil {
		t.Fatal(err)
	}
	if err := game.ValidateActions(&r.State, 1, []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень"}}); err == nil {
		t.Fatal("model duplicated stone")
	}
	stone := r.State.Hero(1).Inventory[len(r.State.Hero(1).Inventory)-1].ID
	goal := []game.Action{{Type: "REMOVE_ITEM", Target: stone}, {Type: "UPDATE_QUEST", Target: quest, Status: "COMPLETED"}}
	if err := game.ValidateActions(&r.State, 1, goal); err == nil {
		t.Fatal("stone installed at mill instead of bridge")
	}
	r.State.Scene = &game.Scene{Title: "Старый мост", Location: "Старый мост"}
	r.State.NPCs = append(r.State.NPCs, game.NPC{ID: "bridge-enemy", Name: "Враг", HP: 12, Alive: true, Disposition: "hostile", Location: "Старый мост"})
	r.State.Combat = true
	if err := game.ValidateActions(&r.State, 1, goal); err == nil {
		t.Fatal("stone installed during combat")
	}
	r.State.Combat = false
	if err := game.ValidateActions(&r.State, 1, goal); err == nil {
		t.Fatal("stone installed while a hostile enemy remained at bridge")
	}
	r.State.NPCs[len(r.State.NPCs)-1].Alive = false
	results, err := game.ApplyActions(&r.State, 1, goal)
	if err != nil {
		t.Fatal("valid installation rejected", err)
	}
	results, err = settleTurn(&r, 1, false, results)
	if err != nil || r.Status != "FINISHED" || r.State.Ending == nil || r.State.Ending.Reason != "objective" || results[len(results)-1].Type != "GAME_FINISHED" {
		t.Fatal("completed objective did not trigger finale", results, err)
	}
	for _, item := range r.State.Hero(1).Inventory {
		if strings.EqualFold(item.Name, "Огненный камень") {
			t.Fatal("installed stone remained in inventory")
		}
	}
}
