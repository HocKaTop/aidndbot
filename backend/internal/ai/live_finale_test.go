package ai

import (
	"context"
	"dnd-bot/backend/internal/game"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in check against the configured local model and Ollama grammar.
func TestLiveCustomQuestProposal(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	state := game.NewState(game.Settings{Name: "Орбитальная экспедиция", Setting: "Научная фантастика", WorldDescription: "Экипаж чинит передатчик и затем возвращается домой.", OllamaModel: model})
	state.Scene = &game.Scene{ID: "bridge", Title: "Центр связи", Location: "Орбитальная станция", Description: "Передатчик восстановлен. На терминале официальный ответ командования: «Сигнал принят»."}
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "signal", Title: "Передать сигнал командованию", Description: "Отправить сигнал с орбитальной станции и получить подтверждение приёма.", Status: "ACTIVE"}}
	state.Turn = 8
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	in := Input{State: state, PlayerID: 1, Text: "Я показываю отряду официальный ответ на терминале. Сигнал принят командованием; это была наша цель."}
	out, err := New(endpoint).GenerateTurn(ctx, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := game.ValidateActions(&state, 1, out.Actions); err != nil {
		t.Fatalf("model proposed invalid goal action: %v; actions=%+v", err, out.Actions)
	}
	proposed := false
	for _, action := range out.Actions {
		proposed = proposed || action.Type == "PROPOSE_QUEST_COMPLETION" && action.Target == "signal" && action.Description != ""
	}
	if !proposed {
		in.Correction = &PlanCorrection{Actions: out.Actions, Reason: "Ты описал завершение миссии, но не предложил PROPOSE_QUEST_COMPLETION. Цель пока активна; добавь предложение с конкретной причиной, не объявляй её завершённой."}
		out, err = New(endpoint).GenerateTurn(ctx, model, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range out.Actions {
			proposed = proposed || action.Type == "PROPOSE_QUEST_COMPLETION" && action.Target == "signal" && action.Description != ""
		}
	}
	if !proposed {
		t.Fatalf("model omitted goal proposal: %+v; narrative=%q", out.Actions, out.Narrative)
	}
	t.Logf("proposal actions=%+v; narrative=%q", out.Actions, out.Narrative)
}

func TestLivePacedCustomQuest(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	state := game.NewState(game.Settings{Name: "Орбитальная экспедиция", Setting: "Научная фантастика", WorldDescription: "Экипаж должен восстановить сигнал станции и вернуться домой.", OllamaModel: model})
	state.Scene = &game.Scene{ID: "bridge", Title: "Рубка", Location: "Орбитальная станция", Description: "Аварийное освещение. Передатчик пока недоступен."}
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "signal", Title: "Восстановить сигнал", Description: "Добраться до передатчика и запустить его.", Status: "ACTIVE"}}
	state.GMNotes = []string{"Служебный лифт к передатчику открывается панелью B7 в рубке."}
	state.Turn = 8
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	out, err := New(endpoint).GenerateTurn(ctx, model, Input{State: state, PlayerID: 1, Text: "Осматриваюсь и ищу путь к передатчику."})
	if err != nil {
		t.Fatal(err)
	}
	if err := game.ValidateActions(&state, 1, out.Actions); err != nil {
		t.Fatalf("invalid paced plan: %v; actions=%+v", err, out.Actions)
	}
	if !strings.Contains(strings.ToLower(out.Narrative), "лифт") {
		t.Fatalf("model invented a route instead of using its notes: %q", out.Narrative)
	}
	t.Logf("late quest turn: %s; actions=%+v", out.Narrative, out.Actions)
}
