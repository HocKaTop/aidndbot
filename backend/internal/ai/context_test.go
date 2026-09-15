package ai

import (
	"dnd-bot/backend/internal/game"
	"errors"
	"strings"
	"testing"
)

func TestContextBudget(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Test"})
	state.Characters = append(state.Characters, game.NewCharacter(1, "Hero", "Human", "Warrior"))
	state.Summary = strings.Repeat("old memory", 1000)
	in := Input{State: state, Recent: []string{strings.Repeat("old", 8000), "most recent"}, Text: "current action", PlayerID: 1}
	out, e := prepareInput(in, DefaultContextWindow)
	if e != nil {
		t.Fatal(e)
	}
	if out.Text != in.Text || len(out.State.Characters) != 1 || out.State.Characters[0].HP != 20 {
		t.Fatal("critical state discarded")
	}
	if in.State.Summary == "" || len(in.Recent) != 2 {
		t.Fatal("input mutated")
	}
	if len(out.Recent) != 1 || out.Recent[0] != "most recent" || out.State.Summary == "" {
		t.Fatal("oversized memory discarded all useful history")
	}
	in.State.Settings.WorldDescription = strings.Repeat("huge", 10000)
	if _, e = prepareInput(in, DefaultContextWindow); !errors.Is(e, ErrContextTooLarge) {
		t.Fatal("oversized critical context accepted", e)
	}
}

func TestDefaultContextFitsPartyAndNarration(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Приключение", Setting: "Тёмное фэнтези"})
	for user := int64(1); user <= 8; user++ {
		state.Characters = append(state.Characters, game.NewCharacter(user, "Искатель приключений", "Человек", "Воин"))
	}
	state.Scene = &game.Scene{Title: "Таверна", Location: "Таверна", Description: "Старая таверна на окраине города."}
	state.NPCs = []game.NPC{{Name: "Хозяин", Description: "За стойкой стоит хозяин таверны.", HP: 12, MaxHP: 12, ArmorClass: 12, Alive: true, Location: "Таверна", Disposition: "friendly"}}
	in := Input{State: state, PlayerID: 1, Text: strings.Repeat("я", 1000), Results: []game.Result{{Type: "SKILL_CHECK", Text: "Проверка завершена"}}}
	if _, err := prepareInput(in, DefaultContextWindow); err != nil {
		t.Fatal("ordinary multiplayer narration exceeds default budget", err)
	}
}
