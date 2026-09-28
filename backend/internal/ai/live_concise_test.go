package ai

import (
	"context"
	"dnd-bot/backend/internal/game"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Opt-in: tests the installed Ollama grammar and response budgets with an
// invented scene. Does not access campaigns, the database, or Telegram.
func TestLiveConciseActions(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	provider := New(endpoint)
	state := game.NewState(game.Settings{Name: "Занавес", OllamaModel: model, WorldDescription: "Обычная деревенская таверна. Тонкий льняной занавес висит в открытом проходе: без замка, ловушки, магии или препятствий. Хозяин и его помощница просят отряд найти потерянный ключ у моста. Начни с них и понятной цели, без боя."})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Scene = &game.Scene{Title: "Таверна", Location: "Таверна", Description: "Льняной занавес в открытом проходе."}
	for _, tc := range []struct {
		name string
		in   Input
		want string
	}{
		{"opening", Input{Opening: true, State: state, PlayerID: 1, Text: "Открой приключение: создай сцену таверны, одну цель и двух NPC — хозяина и помощницу."}, "opening"},
		{"touch curtain", Input{State: state, PlayerID: 1, Text: "Прикасаюсь к занавесу."}, "none"},
		{"independent dice", Input{State: state, PlayerID: 1, Text: "Брось только 2d6+3 как самостоятельный случайный бросок, без проверки характеристики и без других изменений мира."}, "dice"},
		{"repair mistaken roll", Input{State: state, PlayerID: 1, Text: "Прикасаюсь к занавесу.", Correction: &PlanCorrection{Actions: []game.Action{{Type: "DICE_ROLL", Name: "прикосновение к занавесе"}}, Reason: "действие 1 (DICE_ROLL): используй запись d20 или 2d6+3"}}, "none"},
		{"engine narration", Input{State: state, PlayerID: 1, Text: "Проверяю следы.", Results: []game.Result{{Type: "SKILL_CHECK", Text: "Проверка wisdom: 15 против 12; успех: true"}}}, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			started := time.Now()
			out, err := provider.GenerateTurn(ctx, model, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %d chars, %d actions, %s; %s", model, utf8.RuneCountInString(out.Narrative), len(out.Actions), time.Since(started).Round(time.Millisecond), out.Narrative)
			if err := game.ValidateActions(&state, 1, out.Actions); err != nil {
				t.Fatal(err, out.Actions)
			}
			switch tc.want {
			case "none":
				if len(out.Actions) != 0 {
					t.Fatal("unnecessary mechanics", out.Actions)
				}
			case "dice":
				if len(out.Actions) != 1 || out.Actions[0].Type != "DICE_ROLL" || out.Actions[0].Name != "2d6+3" {
					t.Fatal("wrong standalone roll", out.Actions)
				}
			case "opening":
				if len(out.Actions) != 4 || out.Actions[0].Type != "MOVE_SCENE" || out.Actions[1].Type != "CREATE_QUEST" {
					t.Fatal("incomplete opening", out.Actions)
				}
			}
			maxParagraphs := 2
			if tc.in.Opening {
				maxParagraphs = 3
			}
			if len(strings.Split(strings.TrimSpace(out.Narrative), "\n\n")) > maxParagraphs {
				t.Fatal("too many paragraphs")
			}
		})
	}
}
