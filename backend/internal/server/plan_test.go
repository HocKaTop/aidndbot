package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"errors"
	"strings"
	"testing"
)

func TestPlanRepairsFormatWithoutLosingInput(t *testing.T) {
	state := game.NewState(game.Settings{OllamaModel: "test"})
	state.Characters = []game.Character{game.NewCharacter(1, "Анна", "", "")}
	in := ai.Input{State: state, PlayerID: 1, Text: "Осматриваюсь", Recent: []string{"Отряд пришёл к мосту"}}
	calls := 0
	s := &Server{AI: openingProvider(func(_ context.Context, model string, got ai.Input) (ai.Output, error) {
		calls++
		if model != "test" || got.Text != in.Text || string(blob(got.State)) != string(blob(in.State)) || string(blob(got.Recent)) != string(blob(in.Recent)) {
			t.Fatal("repair lost campaign context")
		}
		if calls == 1 {
			return ai.Output{}, ai.ErrInvalidResponse
		}
		if got.ResponseCorrection == "" || got.Results != nil {
			t.Fatal("missing format correction")
		}
		return ai.Output{Narrative: "Видны следы у моста.", Actions: []game.Action{}, Memory: []string{}}, nil
	})}
	if _, err := s.planTurn(context.Background(), "test", in); err != nil || calls != 2 {
		t.Fatal(calls, err)
	}
	if in.ResponseCorrection != "" {
		t.Fatal("caller input changed")
	}
}

func TestPlanKeepsLocationUntilPlayerTravels(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Экспедиция"})
	state.Scene = &game.Scene{Location: "Коридор", Title: "Коридор"}
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	calls := 0
	s := &Server{AI: openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		if calls == 1 {
			return ai.Output{Narrative: "Ты уже в подвале.", Actions: []game.Action{{Type: "MOVE_SCENE", Name: "Подвал", Description: "Тёмный подвал."}}}, nil
		}
		if in.Correction == nil {
			t.Fatal("scene correction missing")
		}
		return ai.Output{Narrative: "Дверь видна в конце коридора.", Actions: []game.Action{}}, nil
	})}
	out, err := s.planTurn(context.Background(), "room", ai.Input{State: state, PlayerID: 1, Text: "Подойду к двери"})
	if err != nil || calls != 2 || len(out.Actions) != 0 {
		t.Fatal(out, err)
	}
	for _, text := range []string{"Где дверь?", "Подойду к двери", "Осматриваю подвал"} {
		if explicitTravel(text) {
			t.Fatal("non-travel action moved scene", text)
		}
	}
	for _, text := range []string{"Иду в подвал", "Вхожу в дверь", "Возвращаюсь к мосту"} {
		if !explicitTravel(text) {
			t.Fatal("travel action was blocked", text)
		}
	}
}

func TestAIRepairsAreBoundedAndInfrastructureFailuresAreNotRetried(t *testing.T) {
	for _, failure := range []error{ai.ErrInvalidResponse, ai.ErrTimeout, ai.ErrUnavailable, ai.ErrModelUnavailable, ai.ErrBusy, ai.ErrRejected, ai.ErrContextTooLarge, context.Canceled} {
		for _, narration := range []bool{false, true} {
			calls := 0
			s := &Server{AI: openingProvider(func(context.Context, string, ai.Input) (ai.Output, error) { calls++; return ai.Output{}, failure })}
			var err error
			if narration {
				_, err = s.narrateTurn(context.Background(), "test", ai.Input{Results: []game.Result{}})
			} else {
				_, err = s.planTurn(context.Background(), "test", ai.Input{})
			}
			want := 1
			if errors.Is(failure, ai.ErrInvalidResponse) {
				want = 2
			}
			if err == nil || calls != want || !strings.Contains(err.Error(), "Ход не сохранён") {
				t.Fatal(failure, narration, calls, err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Server{AI: openingProvider(func(context.Context, string, ai.Input) (ai.Output, error) {
		t.Fatal("cancelled context called AI")
		return ai.Output{}, nil
	})}
	if _, err := s.planTurn(ctx, "test", ai.Input{}); err == nil {
		t.Fatal("cancelled plan succeeded")
	}
	if _, err := s.narrateTurn(ctx, "test", ai.Input{}); err == nil {
		t.Fatal("cancelled narration succeeded")
	}
}

func TestAIErrorMessagesExplainRecovery(t *testing.T) {
	for _, tc := range []struct {
		err  error
		text string
	}{
		{ai.ErrTimeout, "не успел"}, {ai.ErrUnavailable, "Нет связи"},
		{ai.ErrModelUnavailable, "модель не найдена"}, {ai.ErrBusy, "перегружен"},
		{ai.ErrRejected, "отклонил"}, {ai.ErrInvalidResponse, "формат"},
		{ai.ErrContextTooLarge, "контекст"}, {context.Canceled, "отменён"},
	} {
		if err := aiFailure(tc.err); !strings.Contains(err.Error(), tc.text) || !strings.Contains(err.Error(), "Ход не сохранён") {
			t.Fatal(err)
		}
	}
}

func TestLastLanternNarrativeCannotInventStoneOrFinale(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень."})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "quest", Status: "ACTIVE"}}
	for _, tc := range []struct {
		name      string
		narrative string
		actions   []game.Action
		valid     bool
	}{
		{"false stone transfer", "Вы принимаете сияющий камень в руки.", nil, false},
		{"real stone transfer", "Вы принимаете сияющий камень в руки.", []game.Action{{Type: "ADD_ITEM", Name: "Огненный камень"}}, true},
		{"false finale", "Квест успешно завершён.", nil, false},
		{"real finale", "Квест успешно завершён.", []game.Action{{Type: "UPDATE_QUEST", Target: "quest", Status: "COMPLETED"}}, true},
		{"unfinished quest", "Квест пока не завершён.", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStoryNarrative(state, 1, tc.actions, nil, tc.narrative)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestCustomCampaignNarrativeRequiresSavedOutcomes(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Экспедиция", WorldDescription: "Научная фантастика"})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "signal", Status: "ACTIVE"}}
	for _, tc := range []struct {
		name      string
		narrative string
		actions   []game.Action
		valid     bool
	}{
		{"unearned goal", "Цель выполнена.", nil, false},
		{"unearned mission", "Миссия передачи сигнала завершена.", nil, false},
		{"proposal is not completion", "Миссия передачи сигнала завершена.", []game.Action{{Type: "PROPOSE_QUEST_COMPLETION", Target: "signal", Description: "Сигнал принят."}}, false},
		{"earned goal", "Цель выполнена.", []game.Action{{Type: "UPDATE_QUEST", Target: "signal", Status: "COMPLETED"}}, true},
		{"unearned campaign", "Кампания завершена.", nil, false},
		{"goal is not finale", "Кампания завершена.", []game.Action{{Type: "UPDATE_QUEST", Target: "signal", Status: "COMPLETED"}}, false},
		{"explicit finale", "Кампания завершена.", []game.Action{{Type: "UPDATE_QUEST", Target: "signal", Status: "COMPLETED"}, {Type: "FINISH_CAMPAIGN"}}, true},
		{"not yet", "Кампания пока не завершена, цель ещё не выполнена.", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStoryNarrative(state, 1, tc.actions, nil, tc.narrative)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}

func TestCompletedPastQuestDoesNotValidateCurrentQuestClaim(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Экспедиция"})
	state.Quests = []game.Quest{{ID: "past", Status: "COMPLETED"}, {ID: "current", Status: "ACTIVE"}}
	if err := validateStoryNarrative(state, 1, nil, nil, "Квест завершён."); err == nil {
		t.Fatal("old completed quest allowed unsupported claim")
	}
	state.Quests[1].Status = "COMPLETED"
	if err := validateStoryNarrative(state, 1, nil, []game.Result{{Type: "UPDATE_QUEST", Status: "COMPLETED"}}, "Квест завершён."); err != nil {
		t.Fatal("new completion rejected", err)
	}
}

func TestLastLanternNarrativeMismatchIsRepaired(t *testing.T) {
	state := game.NewState(game.Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод ждёт огненный камень."})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	state.Quests = []game.Quest{{ID: "quest", Status: "ACTIVE"}}
	for _, narration := range []bool{false, true} {
		calls := 0
		s := &Server{AI: openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
			calls++
			if calls == 1 {
				return ai.Output{Narrative: "Квест успешно завершён.", Actions: []game.Action{}}, nil
			}
			if narration && in.ResponseCorrection == "" || !narration && in.Correction == nil {
				t.Fatal("missing narrative correction")
			}
			return ai.Output{Narrative: "Пока свет не вернулся к фонарю.", Actions: []game.Action{}}, nil
		})}
		in := ai.Input{State: state, PlayerID: 1, Text: "Осматриваюсь"}
		var err error
		if narration {
			in.Results = []game.Result{}
			_, err = s.narrateTurn(context.Background(), "test", in)
		} else {
			_, err = s.planTurn(context.Background(), "test", in)
		}
		if err != nil || calls != 2 {
			t.Fatal(narration, calls, err)
		}
	}
}
