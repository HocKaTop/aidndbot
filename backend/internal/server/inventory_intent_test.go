package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"strings"
	"testing"
)

func inventoryTestState() game.State {
	s := claimTestState()
	s.Characters[0].Name = "Лея"
	s.NPCs = []game.NPC{{ID: "passenger", Name: "Пассажирка в плаще", Alive: true, LocationID: "hall", Location: "Коридор"}}
	s.RememberLocation(*s.Scene)
	return s
}

func TestInventoryIntentHandlesPhysicalRequestsAndFreeDialogue(t *testing.T) {
	s := inventoryTestState()
	for _, text := range []string{"Прошу пассажирку в плаще вернуть цилиндр Мары.", "Беру подлинный магнитный цилиндр Мары и кладу его в карман.", "Я прошу Вектора выдать цилиндр Мары."} {
		in, err := turnIntent(s, 1, text)
		if err != nil || in.ItemRequest != "цилиндр" {
			t.Fatal(text, in, err)
		}
	}
	for _, text := range []string{"Не беру цилиндр.", "Если она согласится, беру цилиндр.", "Прошу Вектора вернуть память.", "Передаю слово пассажирке.", "Беру себя в руки.", "Беру ребёнка на руки.", "Принимаю решение идти дальше.", "Прошу не передавать цилиндр."} {
		in, err := turnIntent(s, 1, text)
		if err != nil || in.ItemRequest != "" || in.GiveItemID != "" || in.HeldItemID != "" {
			t.Fatal("dialogue/negation was treated as inventory", text, in, err)
		}
	}
	if _, err := turnIntent(s, 1, "Передаю Маре цилиндр из кармана."); err == nil {
		t.Fatal("a nonexistent item could be handed over")
	}
	s.Characters[0].Inventory = append(s.Characters[0].Inventory, game.Item{ID: "cylinder", Name: "Подлинный цилиндр Мары", Quantity: 1, Type: "QUEST"})
	for _, text := range []string{"Передаю Маре цилиндр.", "Передаю технику цилиндр Мары."} {
		in, err := turnIntent(s, 1, text)
		if err != nil || in.GiveItemID != "cylinder" {
			t.Fatal("transfer did not bind to the real item", text, in, err)
		}
	}
	held, err := turnIntent(s, 1, "Беру цилиндр и кладу в карман.")
	if err != nil || held.HeldItemID != "cylinder" || held.ItemRequest != "" {
		t.Fatal("already held item was requested again", held, err)
	}
}

func TestPlaytestProseCannotInventItemsOrNPCDeparture(t *testing.T) {
	data, err := os.ReadFile("testdata/custom_effects.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string
		State     game.State
		Text      string
		Narrative string
		Results   []game.Result
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			in := ai.Input{State: tc.State, BeforeState: &tc.State, PlayerID: 991001, Text: tc.Text, Results: tc.Results}
			if err := validatePhysicalNarrative(in, nil, tc.Narrative); err == nil {
				t.Fatal("the playtest's unsupported physical effect was accepted", tc.Narrative)
			}
		})
	}
}

func TestItemRequestIsRepairedBeforeDice(t *testing.T) {
	s := inventoryTestState()
	calls := 0
	server := &Server{AI: openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		actions := []game.Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 12}}
		if calls == 2 {
			if in.Correction == nil || in.Intent.ItemRequest != "цилиндр" {
				t.Fatal("missing item correction", in)
			}
			actions = append(actions, game.Action{Type: "ADD_ITEM", Name: "Цилиндр Мары", Description: "Подлинная запись."})
		}
		return ai.Output{Narrative: "Пассажирка передаёт вам цилиндр Мары.", Actions: actions}, nil
	})}
	before := string(blob(s))
	out, err := server.planTurn(context.Background(), "test", ai.Input{State: s, PlayerID: 1, Text: "Прошу пассажирку вернуть цилиндр Мары."})
	if err != nil || calls != 2 || len(out.Actions) != 2 || string(blob(s)) != before {
		t.Fatal("planning lost the item or mutated state", out, calls, err)
	}
}

func TestPhysicalClaimsAcceptRecordedEffectsAndOrdinaryDialogue(t *testing.T) {
	s := inventoryTestState()
	intent, _ := turnIntent(s, 1, "Беру цилиндр Мары.")
	for _, text := range []string{"Лея бережно кладет цилиндр Мары в карман.", "Пассажирка передаёт его вам."} {
		in := ai.Input{State: s, PlayerID: 1, Intent: &intent, Text: "Беру цилиндр Мары."}
		actions := []game.Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 12}, {Type: "ADD_ITEM", Name: "Цилиндр Мары"}}
		if err := validatePhysicalNarrative(in, actions, text); err != nil {
			t.Fatal("a planned successful consequence was rejected", text, err)
		}
	}
	for _, text := range []string{"Вы не получаете цилиндр.", "Если вы получите цилиндр, вернитесь к Маре.", "Вы получаете 100 опыта.", "Лея берёт себя в руки.", "Пассажирка скрылась за колонной.", "Пассажирка говорит: «Я вчера ушла на вокзал»."} {
		if err := validatePhysicalNarrative(ai.Input{State: s, PlayerID: 1}, nil, text); err != nil {
			t.Fatal("ordinary prose was rejected", text, err)
		}
	}
	before := s.Clone()
	results, err := game.ApplyActions(&s, 1, []game.Action{{Type: "ADD_ITEM", Name: "Цилиндр Мары"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePhysicalNarrative(ai.Input{State: s, BeforeState: &before, PlayerID: 1, Results: results}, nil, "Лея бережно кладёт цилиндр в карман."); err != nil {
		t.Fatal("a granted item was rejected", err)
	}
	intent, err = turnIntent(s, 1, "Передаю Маре цилиндр.")
	if err != nil {
		t.Fatal(err)
	}
	before = s.Clone()
	results, err = game.ApplyActions(&s, 1, []game.Action{{Type: "REMOVE_ITEM", Target: intent.GiveItemID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePhysicalNarrative(ai.Input{State: s, BeforeState: &before, PlayerID: 1, Intent: &intent, Results: results}, nil, "Лея передаёт Маре цилиндр."); err != nil {
		t.Fatal("a recorded handover was rejected", err)
	}
	if err := validatePhysicalNarrative(ai.Input{State: s, BeforeState: &s, PlayerID: 1, Intent: &intent, Results: []game.Result{}}, nil, "Лея передаёт Маре цилиндр."); err == nil || !strings.Contains(err.Error(), "REMOVE_ITEM") {
		t.Fatal("a handover without removal passed", err)
	}
}

func TestNPCDepartureRequiresChangedPositionEvenWhenPartyMoves(t *testing.T) {
	s := inventoryTestState()
	before := s.Clone()
	results, err := game.ApplyActions(&s, 1, []game.Action{{Type: "MOVE_SCENE", Name: "Платформа"}})
	if err != nil {
		t.Fatal(err)
	}
	in := ai.Input{State: s, BeforeState: &before, PlayerID: 1, Results: results}
	if err := validatePhysicalNarrative(in, nil, "Пассажирка ушла в багажное отделение."); err == nil {
		t.Fatal("party travel was mistaken for NPC travel")
	}
	s = before.Clone()
	results, err = game.ApplyActions(&s, 1, []game.Action{{Type: "CREATE_LOCATION", Name: "Багажное отделение", Description: "Перегородки."}, {Type: "MOVE_NPC", Target: "passenger", Name: "Багажное отделение"}})
	if err != nil {
		t.Fatal(err)
	}
	in.State, in.Results = s, results
	if err := validatePhysicalNarrative(in, nil, "Пассажирка ушла в багажное отделение."); err != nil {
		t.Fatal("a recorded NPC move was rejected", err)
	}
}

func TestCompanionIntentDoesNotConfuseDestinationWithCompanion(t *testing.T) {
	s := inventoryTestState()
	s.NPCs = append(s.NPCs, game.NPC{ID: "vector", Name: "Техник-контролёр Вектор", Alive: true, LocationID: "hall"})
	s.Characters = append(s.Characters, game.NewCharacter(2, "Анна", "", ""))
	in, err := turnIntent(s, 1, "Иду с Анной к Вектору.")
	if err != nil || len(in.CompanionNPCs) != 0 {
		t.Fatal("destination NPC became a companion", in, err)
	}
	in, err = turnIntent(s, 1, "Иду вместе с Вектором в багажное отделение.")
	if err != nil || len(in.CompanionNPCs) != 1 || in.CompanionNPCs[0] != "vector" {
		t.Fatal("named companion was lost", in, err)
	}
	input := ai.Input{State: s, PlayerID: 1, Intent: &in}
	move := game.Action{Type: "MOVE_SCENE", Name: "Багажное отделение"}
	if err := validatePhysicalNarrative(input, []game.Action{move}, "Вектор идёт вместе с вами."); err == nil {
		t.Fatal("travel did not require updating the companion's location")
	}
	if err := validatePhysicalNarrative(input, []game.Action{move, {Type: "MOVE_NPC", Target: "vector", Name: "@current"}}, "Вектор идёт вместе с вами."); err != nil {
		t.Fatal("recorded companion travel was rejected", err)
	}
}

func TestCustomItemReceiptHandoverAndFinalePersistOnce(t *testing.T) {
	ctx, server, _ := reviewServer(t)
	room := multiplayerRoom(t, ctx, server)
	questID := uuid.NewString()
	if _, err := server.mutate(ctx, room.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.Hero(1).Name = "Лея"
		r.State.Hero(1).Stats.Charisma = 60
		r.State.Quests = []game.Quest{{ID: questID, Title: "Вернуть память Маре", Status: "ACTIVE"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plans, narrations := 0, 0
	server.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Results != nil {
			narrations++
			if in.Intent.GiveItemID != "" {
				return ai.Output{Narrative: "Лея передаёт Маре цилиндр. Ведущий предлагает подтвердить результат."}, nil
			}
			return ai.Output{Narrative: "Пассажирка передаёт его вам."}, nil
		}
		plans++
		if in.Intent.GiveItemID != "" {
			return ai.Output{Narrative: "Лея передаёт Маре цилиндр.", Actions: []game.Action{{Type: "REMOVE_ITEM", Target: in.Intent.GiveItemID}, {Type: "PROPOSE_QUEST_COMPLETION", Target: questID, Description: "Мара узнаёт дочь после воспроизведения подлинной записи."}}}, nil
		}
		actions := []game.Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 12}}
		if plans > 1 {
			actions = append(actions, game.Action{Type: "ADD_ITEM", Name: "Подлинный цилиндр Мары", Description: "Запись о дочери."})
		}
		return ai.Output{Narrative: "Пассажирка передаёт его вам.", Actions: actions}, nil
	})
	for _, text := range []string{"Прошу пассажирку в плаще вернуть цилиндр Мары.", "Передаю Маре цилиндр из кармана."} {
		cmd := Command{ID: uuid.NewString(), Type: "player_action"}
		cmd.Data.Text = text
		if err := server.command(ctx, room.ID, 1, cmd); err != nil {
			t.Fatal(text, err)
		}
		beforeCalls := plans + narrations
		if err := server.command(ctx, room.ID, 1, cmd); err != nil || plans+narrations != beforeCalls {
			t.Fatal("retry reran a physical transfer", text, err)
		}
	}
	saved, err := server.load(ctx, room.ID, 1)
	if err != nil || saved.State.PendingQuestCompletion == nil || saved.State.Turn != 2 || saved.State.Quests[0].Status != "ACTIVE" {
		t.Fatal("handover or proposal did not persist", saved, err)
	}
	events, err := server.eventList(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if counts["ADD_ITEM"] != 1 || counts["REMOVE_ITEM"] != 1 || counts["SKILL_CHECK"] != 1 || plans != 3 || narrations != 2 {
		t.Fatal("items or dice were duplicated/lost", counts, plans, narrations)
	}
	confirm := Command{ID: uuid.NewString(), Type: "confirm_quest", ExpectedTurn: &saved.State.Turn}
	confirm.Data.ProposalID = saved.State.PendingQuestCompletion.ID
	if err := server.command(ctx, room.ID, 1, confirm); err != nil {
		t.Fatal(err)
	}
	saved, err = server.load(ctx, room.ID, 1)
	if err != nil || saved.Status != "FINISHED" || saved.State.Quests[0].Status != "COMPLETED" || saved.State.Hero(1).Experience != 100 {
		t.Fatal("confirmed custom goal did not finish", saved, err)
	}
}

func TestFailedItemCheckCannotBecomeAProseOnlyGift(t *testing.T) {
	ctx, server, _ := reviewServer(t)
	room := multiplayerRoom(t, ctx, server)
	if _, err := server.mutate(ctx, room.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.Hero(1).Stats.Charisma = -40
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plans, narrations := 0, 0
	server.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Results == nil {
			plans++
			return ai.Output{Narrative: "Пассажирка передаёт вам цилиндр.", Actions: []game.Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 12}, {Type: "ADD_ITEM", Name: "Цилиндр Мары"}}}, nil
		}
		narrations++
		return ai.Output{Narrative: "Пассажирка передаёт вам цилиндр."}, nil
	})
	cmd := Command{ID: uuid.NewString(), Type: "player_action"}
	cmd.Data.Text = "Прошу пассажирку вернуть цилиндр Мары."
	if err := server.command(ctx, room.ID, 1, cmd); err != nil {
		t.Fatal("failed check was not saved", err)
	}
	saved, err := server.load(ctx, room.ID, 1)
	if err != nil || ownsNamedItem(saved.State, 1, "цилиндр") || saved.State.Turn != 1 {
		t.Fatal("a failed check granted an item or lost the turn", saved, err)
	}
	if err := server.command(ctx, room.ID, 1, cmd); err != nil || plans != 1 || narrations != 2 {
		t.Fatal("retry repeated the check or narration", plans, narrations, err)
	}
	events, err := server.eventList(ctx, room.ID)
	if err != nil || !strings.Contains(string(events[len(events)-1].Payload), "Результат хода:") {
		t.Fatal("unsupported gift survived narration fallback", events, err)
	}
}
