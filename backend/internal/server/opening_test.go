package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func openingOutput() ai.Output {
	return ai.Output{Narrative: "У моста погас фонарь. Верните огненный камень до заката. Что вы делаете?", Actions: []game.Action{
		{Type: "MOVE_SCENE", Name: "Мост", Description: "Мост окутан туманом."},
		{Type: "CREATE_QUEST", Name: "Вернуть свет", Description: "Найдите камень и зажгите фонарь до заката."},
	}, Memory: []string{}}
}

func TestLobbyReady(t *testing.T) {
	r := Room{Members: []Member{{UserID: 1, Name: "Анна", Ready: true}, {UserID: 2, Name: "Олег"}}, State: game.NewState(game.Settings{})}
	r.State.Characters = []game.Character{game.NewCharacter(1, "Анна", "", "")}
	if err := lobbyReady(&r); err == nil || !strings.Contains(err.(*apiError).Message, "Олег — нужен живой персонаж") {
		t.Fatal("missing character did not block start", err)
	}
	r.State.Characters = append(r.State.Characters, game.NewCharacter(2, "Олег", "", ""))
	if err := lobbyReady(&r); err == nil || !strings.Contains(err.(*apiError).Message, "/ready") {
		t.Fatal("unready player did not block start", err)
	}
	r.Members[1].Ready = true
	if err := lobbyReady(&r); err != nil {
		t.Fatal(err)
	}
	r.State.Characters[1].HP = 0
	if err := lobbyReady(&r); err == nil {
		t.Fatal("dead hero accepted")
	}
}

func TestOpeningRejectsMissingGoalAndPlayerActions(t *testing.T) {
	base := openingOutput().Actions
	for _, actions := range [][]game.Action{
		nil, base[:1], {base[1], base[0]}, {base[0], base[1], base[1]},
		{base[0], base[1], {Type: "SKILL_CHECK", Skill: "wisdom", DC: 10}},
		{base[0], base[1], {Type: "CREATE_NPC", Name: "Враг", Status: "hostile"}},
		{base[0], base[1], {Type: "ADD_ITEM", Name: "Награда"}},
		{{Type: "MOVE_SCENE", Name: "Пустая сцена"}, base[1]},
	} {
		if err := validateOpening(actions); err == nil {
			t.Fatalf("invalid opening accepted: %+v", actions)
		}
	}
	if err := validateOpening(append(base, game.Action{Type: "CREATE_NPC", Name: "Мира", Status: "friendly"})); err != nil {
		t.Fatal(err)
	}
}

type openingProvider func(context.Context, string, ai.Input) (ai.Output, error)

func (p openingProvider) GenerateTurn(ctx context.Context, model string, in ai.Input) (ai.Output, error) {
	return p(ctx, model, in)
}

func TestOpeningAtomicStartAndResume(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := testCampaign(t, ctx, s, 1)
	calls, mode := 0, "fail"
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		if !in.Opening || in.Results != nil {
			t.Fatal("start was treated as a player turn")
		}
		if mode == "fail" {
			return ai.Output{}, errors.New("model unavailable")
		}
		out := openingOutput()
		if mode == "invalid" {
			out.Actions = append(out.Actions, game.Action{Type: "ADD_ITEM", Name: "Подарок"})
		}
		return out, nil
	})
	if err := s.startGame(ctx, r.ID, 1); err == nil || calls != 0 {
		t.Fatal("unready start reached model", err)
	}
	ready := Command{Type: "ready"}
	ready.Data.Ready = true
	if err := s.command(ctx, r.ID, 1, ready); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"fail", "invalid"} {
		mode = failure
		if err := s.startGame(ctx, r.ID, 1); err == nil {
			t.Fatal("bad opening accepted")
		}
		saved, err := s.load(ctx, r.ID, 1)
		if err != nil || saved.Status != "WAITING" || string(blob(saved.State)) != string(blob(r.State)) || !saved.Members[0].Ready {
			t.Fatal("failed opening changed lobby", saved, err)
		}
		events, err := s.eventList(ctx, r.ID)
		if err != nil || len(events) != 0 {
			t.Fatal("failed opening left events", events, err)
		}
	}
	mode = "ok"
	if err := s.startGame(ctx, r.ID, 1); err != nil {
		t.Fatal(err)
	}
	saved, err := s.load(ctx, r.ID, 1)
	if err != nil || saved.Status != "PLAYING" || saved.State.Scene == nil || len(saved.State.Quests) != 1 || saved.State.Turn != 0 || saved.State.Combat || saved.State.Characters[0].HP != 20 {
		t.Fatal("incomplete opening or opening consumed a turn", saved, err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || len(events) != 4 || events[3].Type != "GM_MESSAGE" {
		t.Fatal("opening events missing", events, err)
	}
	before, count := string(blob(saved.State)), calls
	if err := s.startGame(ctx, r.ID, 1); err == nil || calls != count {
		t.Fatal("duplicate start reached model", err)
	}
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, room *Room) error {
		room.Status = "PAUSED"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.startGame(ctx, r.ID, 1); err != nil || calls != count {
		t.Fatal("resume generated another opening", err)
	}
	saved, err = s.load(ctx, r.ID, 1)
	if err != nil || saved.Status != "PLAYING" || string(blob(saved.State)) != before {
		t.Fatal("resume changed world", err)
	}
}

// Opt-in: exercises the opening contract against the configured local model.
func TestLiveOpening(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	state := game.NewState(game.Settings{Name: "Последний фонарь", Setting: "Тёмное фэнтези", OllamaModel: model,
		WorldDescription: "Отряд у моста деревни Тихий Брод. Фонарь погас: пропал огненный камень. Смотрительница Мира просит вернуть его до заката. Следы ведут к старой мельнице. Начни с разговора и исследования, без боя.",
	})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	s := &Server{AI: ai.New(endpoint)}
	started := time.Now()
	out, err := s.planTurn(ctx, "smoke-opening", ai.Input{Opening: true, State: state, PlayerID: 1, Text: "Открой приключение для всего отряда: вступление, начальная сцена и первая цель."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = game.ApplyActions(&state, 1, out.Actions); err != nil || state.Scene == nil || len(state.Quests) != 1 || state.Combat || state.Characters[0].HP != 20 {
		t.Fatal("invalid live opening", err)
	}
	t.Logf("%s: opening, scene and quest validated in %s", model, time.Since(started).Round(time.Millisecond))
}

// Opt-in: verifies that an owner-defined non-fantasy setting reaches the
// opening planner and produces a usable scene, goal and private story notes.
func TestLiveCustomSettingOpening(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	state := game.NewState(game.Settings{
		Name: "Сигнал в глубине", Setting: "Научная фантастика", Tone: "Напряжённое расследование", OllamaModel: model,
		WorldDescription: "2080 год. Отряд прибывает на подводную исследовательскую станцию, откуда пропала связь. В этом мире нет магии и фэнтезийных существ. Придумай загадку и первую цель сам.",
	})
	state.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин")}
	s := &Server{AI: ai.New(endpoint)}
	out, err := s.planTurn(ctx, "synthetic", ai.Input{Opening: true, State: state, PlayerID: 1, Text: "Открой приключение."})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOpening(out.Actions); err != nil || len(out.Memory) == 0 {
		t.Fatal("custom setting did not produce a scene, goal and private plot hook", err, out)
	}
	for _, forbidden := range []string{"эльф", "магия", "заклинание", "таверна"} {
		if strings.Contains(strings.ToLower(out.Narrative), forbidden) {
			t.Fatalf("fantasy element %q ignored owner's setting: %s", forbidden, out.Narrative)
		}
	}
	t.Logf("opening: %s; notes: %v", out.Narrative, out.Memory)
}
