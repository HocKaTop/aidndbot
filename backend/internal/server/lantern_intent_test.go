package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"github.com/google/uuid"
	"testing"
)

func lanternIntentState() game.State {
	s := game.NewState(game.Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень."})
	hero := game.NewCharacter(1, "Олег", "Человек", "Воин")
	hero.Stats.Charisma = 60
	s.Characters = []game.Character{hero}
	bridge := game.Scene{ID: uuid.NewString(), Title: "Каменный мост Тихого Брода", Location: "Каменный мост Тихого Брода"}
	mill := game.Scene{ID: uuid.NewString(), Title: "Старая мельница", Location: "Старая мельница"}
	s.Scene = &mill
	s.Locations = []game.Scene{bridge, mill}
	s.NPCs = []game.NPC{{ID: uuid.NewString(), Name: "Похититель", Alive: true, HP: 12, MaxHP: 12, Disposition: "neutral", LocationID: mill.ID}}
	s.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Вернуть свет", Status: "ACTIVE"}}
	return s
}

func TestLanternRequestGivesStoneOnlyAfterSuccessfulNegotiation(t *testing.T) {
	for _, success := range []bool{true, false} {
		state := lanternIntentState()
		if !success {
			state.Characters[0].Stats.Charisma = -40
		}
		results, handled, err := applyLanternIntent(&state, 1, "Объясняю, что без камня деревня погибнет. Прошу вернуть его; мы поможем мирно.")
		if err != nil || !handled || len(results) != 3 || results[0].Success == nil || *results[0].Success != success {
			t.Fatal("negotiation did not resolve", handled, err, results)
		}
		stones := 0
		for _, item := range state.Characters[0].Inventory {
			if item.Name == "Огненный камень" {
				stones += item.Quantity
			}
		}
		if success && (stones != 1 || state.NPCs[0].Disposition != "friendly") || !success && (stones != 0 || state.NPCs[0].Disposition != "neutral") {
			t.Fatal("stone transfer ignored the skill result", state)
		}
		if state.Quests[0].Status != "ACTIVE" {
			t.Fatal("negotiation ended the quest")
		}
	}
}

func TestLanternIntentLeavesAmbiguousRequestsToGM(t *testing.T) {
	for _, text := range []string{
		"Если он согласится, прошу передать камень.",
		"Прошу не отдавать камень.",
		"Прошу передать отчёт по кампании.",
		"Не возвращаюсь к мосту и иду в лес.",
		"Устанавливаю камень не в фонарь, а на стол.",
		"Камень у друга. Устанавливаю ключ в фонарь.",
		"Куда идти за камнем?",
	} {
		if kind := explicitLanternKind(text); kind != "" {
			t.Errorf("%q: unexpected tutorial action %s", text, kind)
		}
	}
	state := lanternIntentState()
	state.Settings.Name = "Своя история"
	if _, handled, err := applyLanternIntent(&state, 1, "Прошу передать огненный камень."); handled || err != nil {
		t.Fatal("tutorial rules applied to a custom setting", handled, err)
	}
}

func TestLanternTextRoutePersistsOnceAndReturnsToKnownBridge(t *testing.T) {
	ctx, server, _ := reviewServer(t)
	room := multiplayerRoom(t, ctx, server)
	state := lanternIntentState()
	bridgeID := state.Locations[0].ID
	state.Characters[0].ID = room.State.Hero(1).ID
	if _, err := server.mutate(ctx, room.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		if in.Results == nil {
			t.Fatal("an explicit tutorial operation was replanned by the model")
		}
		return ai.Output{Narrative: "Последствия действия записаны."}, nil
	})
	for _, text := range []string{
		"Прошу передать огненный камень.",
		"Вместе с Анной возвращаюсь к мосту с огненным камнем.",
		"Устанавливаю камень обратно в фонарь.",
	} {
		cmd := Command{ID: uuid.NewString(), Type: "player_action"}
		cmd.Data.Text = text
		if err := server.command(ctx, room.ID, 1, cmd); err != nil {
			t.Fatal(text, err)
		}
		before := calls
		if err := server.command(ctx, room.ID, 1, cmd); err != nil || calls != before {
			t.Fatal("retry repeated a tutorial operation", text, err)
		}
	}
	saved, err := server.load(ctx, room.ID, 1)
	if err != nil || saved.Status != "FINISHED" || saved.State.Quests[0].Status != "COMPLETED" || saved.State.Scene.ID != bridgeID || len(saved.State.Locations) != 2 || saved.State.Turn != 3 {
		t.Fatal("tutorial did not persist its original bridge and ending", saved, err)
	}
	for _, item := range saved.State.Hero(1).Inventory {
		if item.Name == "Огненный камень" {
			t.Fatal("installed stone remains in inventory")
		}
	}
}
