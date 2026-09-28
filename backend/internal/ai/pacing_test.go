package ai

import (
	"dnd-bot/backend/internal/game"
	"strings"
	"testing"
)

func TestCustomCampaignPromptGuidesTowardVisibleFinalGoal(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Экспедиция", Setting: "Научная фантастика"})
	opening := Prompt(Input{Opening: true, State: state})
	for _, phrase := range []string{"ясную достижимую главную цель", "назови её игрокам во вступлении", "конечная задача короткой кампании"} {
		if !strings.Contains(opening, phrase) {
			t.Fatal("opening does not make the goal visible", phrase)
		}
	}
	state.Quests = []game.Quest{{ID: "goal", Status: "ACTIVE"}}
	for _, tc := range []struct {
		turn int
		want string
	}{
		{0, "полезную зацепку"},
		{4, "покажи конкретный путь"},
		{8, "дай явную зацепку"},
	} {
		state.Turn = tc.turn
		if prompt := Prompt(Input{State: state}); !strings.Contains(prompt, tc.want) {
			t.Fatalf("turn %d lacks pacing: %q", tc.turn, tc.want)
		}
	}
	state.Quests[0].Status = "COMPLETED"
	if !strings.Contains(Prompt(Input{State: state}), "подвести итоги кампании") {
		t.Fatal("completed goal did not prompt owner to finish")
	}
	state.Quests[0].Status = "FAILED"
	if strings.Contains(Prompt(Input{State: state}), "подвести итоги кампании") {
		t.Fatal("failed goal prompted a false victory")
	}
	state.Quests[0].Status = "ACTIVE"
	if strings.Contains(Prompt(Input{State: state, Results: []game.Result{}}), "дай явную зацепку") {
		t.Fatal("narration was told to plan new events")
	}
	state.Settings = game.Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень."}
	if strings.Contains(Prompt(Input{State: state}), "дай явную зацепку") {
		t.Fatal("custom campaign pacing leaked into the tutorial")
	}
}
