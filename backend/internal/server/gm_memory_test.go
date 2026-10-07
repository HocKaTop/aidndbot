package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrivateGMMemorySurvivesOpeningAndTurn(t *testing.T) {
	ctx, server, _ := reviewServer(t)
	room := testCampaign(t, ctx, server, 1)
	ready := Command{Type: "ready"}
	ready.Data.Ready = true
	if err := server.command(ctx, room.ID, 1, ready); err != nil {
		t.Fatal(err)
	}
	const secret = "Смотритель станции скрывает источник сигнала."
	server.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Opening {
			return ai.Output{
				Narrative: "Вы прибыли на станцию. Что вы делаете?",
				Actions: []game.Action{
					{Type: "MOVE_SCENE", Name: "Станция", Description: "Пустой командный отсек."},
					{Type: "CREATE_QUEST", Name: "Найти сигнал", Description: "Выясните источник странного сигнала."},
				},
				Memory: []string{secret},
			}, nil
		}
		if len(in.State.GMNotes) != 1 || in.State.GMNotes[0] != secret {
			t.Fatalf("private opening memory missing from next AI turn: %v", in.State.GMNotes)
		}
		return ai.Output{Narrative: "Из-за панели слышен сигнал.", Actions: []game.Action{}, Memory: []string{"Сигнал усиливается у реактора."}}, nil
	})
	if err := server.startGame(ctx, room.ID, 1); err != nil {
		t.Fatal(err)
	}
	saved, err := server.load(ctx, room.ID, 1)
	if err != nil || len(saved.State.GMNotes) != 1 || saved.State.GMNotes[0] != secret {
		t.Fatal("opening memory was not persisted", err, saved.State.GMNotes)
	}
	public, err := json.Marshal(saved)
	if err != nil || strings.Contains(string(public), "gmNotes") || strings.Contains(string(public), secret) {
		t.Fatal("private notes leaked in room response", err)
	}
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Прислушиваюсь к сигналу"
	if err := server.command(ctx, room.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	saved, err = server.load(ctx, room.ID, 1)
	if err != nil || len(saved.State.GMNotes) != 2 || saved.State.GMNotes[1] != "Сигнал усиливается у реактора." {
		t.Fatal("turn memory was not persisted", err, saved.State.GMNotes)
	}
	if len(saved.State.PlayerHistory) != 1 || !strings.Contains(saved.State.PlayerHistory[0], "Прислушиваюсь к сигналу") {
		t.Fatal("player decision without engine action was forgotten", saved.State.PlayerHistory)
	}
}

func TestGMMemoryIsBoundedAndDoesNotAliasCopies(t *testing.T) {
	state := game.NewState(game.Settings{})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	rememberGMNotes(&state, []string{
		"В кармане Олега лежит загадочная пластина.",
		"Капитан станции скрывает источник сигнала.",
	})
	if len(state.GMNotes) != 1 || state.GMNotes[0] != "Капитан станции скрывает источник сигнала." {
		t.Fatal("invented player possession entered private memory", state.GMNotes)
	}
	for i := 0; i < 50; i++ {
		rememberGMNotes(&state, []string{strings.Repeat("a", 100) + string(rune('A'+i))})
	}
	if len(state.GMNotes) > maxGMNotes {
		t.Fatal("private memory grew without bound", len(state.GMNotes))
	}
	bytes := 0
	for _, note := range state.GMNotes {
		bytes += len(note)
	}
	if bytes > maxGMNotesBytes {
		t.Fatal("private memory exceeded byte budget", bytes)
	}
	clone := state.Clone()
	clone.GMNotes[0] = "changed"
	if state.GMNotes[0] == "changed" {
		t.Fatal("clone shares private memory storage")
	}
	last := state.GMNotes[len(state.GMNotes)-1]
	rememberGMNotes(&state, []string{last})
	if len(state.GMNotes) != len(clone.GMNotes) || state.GMNotes[len(state.GMNotes)-1] != last {
		t.Fatal("repeated fact was duplicated")
	}
}

func TestPlanningMemorySurvivesNarration(t *testing.T) {
	ctx, server, _ := reviewServer(t)
	room := testCampaign(t, ctx, server, 1)
	if _, err := server.mutate(ctx, room.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	const clue = "В запасном шлюзе спрятана панель B7."
	server.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Results == nil {
			return ai.Output{Narrative: "Слышен тихий сигнал.", Actions: []game.Action{{Type: "SKILL_CHECK", Skill: "wisdom", DC: 12}}, Memory: []string{clue}}, nil
		}
		if len(in.State.GMNotes) != 1 || in.State.GMNotes[0] != clue {
			t.Fatal("narrator did not receive planning clue", in.State.GMNotes)
		}
		return ai.Output{Narrative: "Сигнал слышен у панели.", Memory: []string{"Передатчик питается от запасного генератора."}}, nil
	})
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = "Прислушиваюсь к сигналу."
	if err := server.command(ctx, room.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	saved, err := server.load(ctx, room.ID, 1)
	if err != nil || len(saved.State.GMNotes) != 2 || saved.State.GMNotes[0] != clue {
		t.Fatal("narration erased the planning clue", saved.State.GMNotes, err)
	}
}
