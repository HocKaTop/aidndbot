package ai

import (
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestCustomSettingCorrectionsFitDefaultContext(t *testing.T) {
	data, err := os.ReadFile("testdata/custom_context.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string `json:"name"`
		Input Input  `json:"input"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			before, _ := json.Marshal(tc.Input)
			out, err := prepareInput(tc.Input, DefaultContextWindow)
			if err != nil {
				t.Fatal("a short custom campaign could not repair its plan", err)
			}
			after, _ := json.Marshal(tc.Input)
			if string(before) != string(after) || out.Text != tc.Input.Text || out.Correction.Reason != tc.Input.Correction.Reason || len(out.State.Characters) != len(tc.Input.State.Characters) {
				t.Fatal("context preparation changed the source or dropped the player's action")
			}
		})
	}
}

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
	in.Results = nil
	if _, err := prepareInput(in, DefaultContextWindow); err != nil {
		t.Fatal("ordinary multiplayer planning exceeds default budget", err)
	}
	in.State.Quests = []game.Quest{{ID: "goal", Title: "Вернуть сигнал", Description: "Добраться до передатчика.", Status: "ACTIVE"}}
	in.State.Turn = 8
	if _, err := prepareInput(in, DefaultContextWindow); err != nil {
		t.Fatal("paced multiplayer planning exceeds default budget", err)
	}
	in.Text = "Атакую стражника " + strings.Repeat("я", 980)
	in.Intent = &PlayerIntent{}
	if _, err := prepareInput(in, DefaultContextWindow); err != nil {
		t.Fatal("attack planning exceeds default budget", err)
	}
}

func TestRemoteWorldDoesNotBlockOrdinaryTurn(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Экспедиция"})
	state.Scene = &game.Scene{ID: "tavern", Title: "Таверна", Location: "Таверна"}
	state.Locations = []game.Scene{*state.Scene}
	state.Characters = []game.Character{game.NewCharacter(1, "Герой", "Человек", "Воин")}
	for i := 0; i < 9; i++ {
		state.NPCs = append(state.NPCs, game.NPC{ID: string(rune('a' + i)), Name: "Чужой", Description: strings.Repeat("с", 720), Alive: true, LocationID: "remote"})
	}
	in := Input{State: state, PlayerID: 1, Text: "Осматриваю таверну"}
	out, err := prepareInput(in, DefaultContextWindow)
	if err != nil {
		t.Fatal("remote NPC descriptions blocked a local action", err)
	}
	if len(out.State.NPCs) != 9 || out.State.NPCs[0].Description != "" || state.NPCs[0].Description == "" {
		t.Fatal("projection lost references or mutated saved state")
	}
}

func TestOldLocationsRemainAvailableWhenNamed(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Экспедиция"})
	state.Scene = &game.Scene{ID: "current", Title: "Текущий зал", Location: "Текущий зал"}
	for i := 0; i < 100; i++ {
		state.Locations = append(state.Locations, game.Scene{ID: fmt.Sprintf("place-%d", i), Title: fmt.Sprintf("Комната %d", i), Description: strings.Repeat("детали", 100)})
	}
	state.Locations = append(state.Locations, *state.Scene)
	input := Input{State: state, Text: "Возвращаюсь в Комнату 0"}
	out, err := prepareInput(input, DefaultContextWindow)
	if err != nil {
		t.Fatal("old locations blocked the turn", err)
	}
	if len(out.State.Locations) > 16 || out.State.Location("place-0") == nil || out.State.Location("current") == nil || len(state.Locations) != 101 {
		t.Fatal("named location was lost or saved world mutated")
	}
}
