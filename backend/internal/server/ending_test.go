package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestCustomCampaignFinaleRequiresOwnerConfirmation(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := testCampaign(t, ctx, s, 1)
	var err error
	r, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Scene = &game.Scene{ID: uuid.NewString(), Title: "Орбитальная станция", Location: "Орбита"}
		r.State.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Восстановить сигнал", Status: "ACTIVE"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		if in.Results != nil {
			if in.State.PendingQuestCompletion == nil || in.State.Quests[0].Status != "ACTIVE" {
				t.Fatal("narration did not receive pending proposal")
			}
			return ai.Output{Narrative: "Сигнал принят. Ведущий предлагает завершить цель; можно подтвердить или продолжить."}, nil
		}
		return ai.Output{Actions: []game.Action{{Type: "PROPOSE_QUEST_COMPLETION", Target: in.State.Quests[0].ID, Description: "Сигнал принят командованием."}}, Narrative: "Сигнал принят; ожидается решение отряда."}, nil
	})
	cmd := Command{Type: "player_action", ExpectedTurn: &r.State.Turn}
	cmd.Data.Text = "Восстанавливаю сигнал и возвращаюсь домой"
	if err := s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.Status != "PLAYING" || loaded.State.PendingQuestCompletion == nil || loaded.State.Quests[0].Status != "ACTIVE" {
		t.Fatal("AI proposal did not keep campaign active", loaded, err)
	}
	decline := Command{Type: "continue_quest", ExpectedTurn: &loaded.State.Turn}
	decline.Data.ProposalID = loaded.State.PendingQuestCompletion.ID
	if err := s.command(ctx, r.ID, 1, decline); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.PendingQuestCompletion != nil || loaded.State.Quests[0].Status != "ACTIVE" || loaded.State.Characters[0].Experience != 0 {
		t.Fatal("declined proposal changed goal or rewards", loaded, err)
	}
	cmd.ExpectedTurn = &loaded.State.Turn
	if err := s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.PendingQuestCompletion == nil {
		t.Fatal("second proposal missing", err)
	}
	confirm := Command{Type: "confirm_quest", ExpectedTurn: &loaded.State.Turn}
	confirm.Data.ProposalID = loaded.State.PendingQuestCompletion.ID
	if err := s.command(ctx, r.ID, 1, confirm); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.Status != "FINISHED" || loaded.State.Ending == nil || loaded.State.Ending.Reason != "objective" || loaded.State.Quests[0].Status != "COMPLETED" || loaded.State.PendingQuestCompletion != nil || loaded.State.Characters[0].Experience != 100 {
		t.Fatal("confirmed finale not saved", loaded, err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || len(events) < 3 || events[len(events)-1].Type != "GAME_FINISHED" {
		t.Fatal("finale event missing", events, err)
	}
	if err := s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("finished campaign accepted another turn")
	}
}

func TestReopenLegacyQuestDoesNotGrantExperienceTwice(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := testCampaign(t, ctx, s, 1)
	questID := uuid.NewString()
	_, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Quests = []game.Quest{{ID: questID, Title: "Найти Маркова", Status: "COMPLETED"}}
		r.State.Characters[0].Experience = 100
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reopen := Command{Type: "reopen_quest"}
	reopen.Data.Target = questID
	if err := s.command(ctx, r.ID, 1, reopen); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Quests[0].Status != "ACTIVE" || !loaded.State.Quests[0].Rewarded || loaded.State.Characters[0].Experience != 100 {
		t.Fatal("legacy quest not restored safely", err)
	}
	proposalID := uuid.NewString()
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.PendingQuestCompletion = &game.QuestCompletionProposal{ID: proposalID, QuestID: questID, Reason: "Марков найден."}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	confirm := Command{Type: "confirm_quest"}
	confirm.Data.ProposalID = proposalID
	if err := s.command(ctx, r.ID, 1, confirm); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.Status != "FINISHED" || loaded.State.Characters[0].Experience != 100 {
		t.Fatal("reopened quest granted duplicate experience", err)
	}
}

func TestStaleQuestDecisionCannotResolveReplacementProposal(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := testCampaign(t, ctx, s, 1)
	questID, firstID, secondID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Quests = []game.Quest{{ID: questID, Title: "Найти Маркова", Status: "ACTIVE"}}
		r.State.PendingQuestCompletion = &game.QuestCompletionProposal{ID: firstID, QuestID: questID, Reason: "Марков найден.", Status: "COMPLETED"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.State.PendingQuestCompletion = &game.QuestCompletionProposal{ID: secondID, QuestID: questID, Reason: "Марков погиб.", Status: "FAILED"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"confirm_quest", "continue_quest"} {
		stale := Command{Type: kind}
		stale.Data.ProposalID = firstID
		if err := s.command(ctx, r.ID, 1, stale); err == nil {
			t.Fatalf("stale %s changed a replacement proposal", kind)
		}
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.PendingQuestCompletion == nil || loaded.State.PendingQuestCompletion.ID != secondID || loaded.State.Quests[0].Status != "ACTIVE" {
		t.Fatal("stale command changed quest", err)
	}
	confirm := Command{Type: "confirm_quest"}
	confirm.Data.ProposalID = secondID
	if err := s.command(ctx, r.ID, 1, confirm); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Quests[0].Status != "FAILED" || loaded.Status != "PLAYING" || loaded.State.Characters[0].Experience != 0 {
		t.Fatal("failed outcome was not saved correctly", err)
	}
	reopen := Command{Type: "reopen_quest"}
	reopen.Data.Target = questID
	if err := s.command(ctx, r.ID, 1, reopen); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Quests[0].Status != "ACTIVE" || loaded.State.Quests[0].Rewarded {
		t.Fatal("failed quest could not be restored", err)
	}
}

func TestFinishCampaignPersistsOnceAndFreezesWorld(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	r, err := s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PAUSED"
		r.State.Turn = 7
		r.State.Combat = true
		r.State.EnsureCombatOrder(1, r.State.CombatTurnSince)
		r.State.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Спасти мост", Status: "ACTIVE"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{ID: uuid.NewString(), Type: "finish_game", ExpectedTurn: &r.State.Turn}
	cmd.Data.Code, cmd.Data.Text = r.Code, "Мы вернулись домой."
	if err = s.command(ctx, r.ID, 2, cmd); err == nil {
		t.Fatal("non-owner finished campaign")
	}
	cmd.Data.Code = "WRONG"
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("confirmation not required")
	}
	cmd.Data.Code = r.Code
	cmd.Data.Text = strings.Repeat("я", 1001)
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("unbounded ending")
	}
	cmd.Data.Text = "Мы вернулись домой."
	old := r.State.Turn - 1
	cmd.ExpectedTurn = &old
	if err = s.command(ctx, r.ID, 1, cmd); err == nil {
		t.Fatal("stale finish accepted")
	}
	cmd.ExpectedTurn = &r.State.Turn
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	restored := &Server{Config: s.Config, Pool: s.Pool}
	saved, err := restored.load(ctx, r.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "FINISHED" || saved.State.Ending == nil || saved.State.Ending.Reason != "owner" || saved.State.Ending.Note != cmd.Data.Text || saved.State.Ending.FinishedAt.IsZero() {
		t.Fatal("ending not restored", saved, err)
	}
	if saved.State.Turn != 7 || saved.State.Combat || len(saved.State.CombatOrder) != 0 || saved.State.Quests[0].Status != "ACTIVE" {
		t.Fatal("ending changed turns/quests or left combat active")
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil || len(events) != 1 || events[0].Type != "GAME_FINISHED" {
		t.Fatal("ending event missing", events, err)
	}
	var notices int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox WHERE room_id=$1 AND body LIKE '%Итоги приключения%'", r.ID).Scan(&notices); err != nil || notices != 2 {
		t.Fatal("final notifications missing", notices, err)
	}
	if err = restored.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal("retry not idempotent", err)
	}
	if err = restored.startGame(ctx, r.ID, 1); err == nil {
		t.Fatal("finished campaign resumed")
	}
	for _, typ := range []string{"player_action", "use_item", "roll_dice", "pass_turn", "skip_turn", "ready", "finish_game"} {
		blocked := Command{Type: typ}
		blocked.Data.Code, blocked.Data.Text = r.Code, "Продолжаем"
		if err = restored.command(ctx, r.ID, 1, blocked); err == nil {
			t.Fatal("finished world mutated", typ)
		}
	}
	after, err := restored.load(ctx, r.ID, 1)
	if err != nil || string(blob(saved.State)) != string(blob(after.State)) {
		t.Fatal("finished world changed", err)
	}
	afterEvents, err := s.eventList(ctx, r.ID)
	if err != nil || len(afterEvents) != len(events) {
		t.Fatal("duplicate ending events", err)
	}
	var afterNotices int
	if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox WHERE room_id=$1", r.ID).Scan(&afterNotices); err != nil || afterNotices != notices {
		t.Fatal("duplicate final notifications", err)
	}
}

func TestTextFinishRequiresConfirmationAndExplainsFinishedState(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	u := auth.User{ID: 1, FirstName: "Tester"}
	before, err := s.TextMessage(ctx, u, "/finish")
	if err != nil || !strings.Contains(before, "/finish "+r.Code) {
		t.Fatal(before, err)
	}
	stillPlaying, _ := s.load(ctx, r.ID, 1)
	if stillPlaying.Status != "PLAYING" {
		t.Fatal("prompt finished campaign")
	}
	out, err := s.TextMessage(ctx, u, "/finish "+r.Code)
	if err != nil || !strings.Contains(out, "Итоги приключения") || !strings.Contains(out, "Ходов: 0") {
		t.Fatal(out, err)
	}
	for _, message := range []string{"/state", "/finish", "Я открываю дверь"} {
		out, err = s.TextMessage(ctx, u, message)
		if err != nil || !strings.Contains(out, "Итоги приключения") {
			t.Fatal(message, out, err)
		}
	}
	out, err = s.TextMessage(ctx, u, "/play")
	if err != nil || !strings.Contains(out, "Кампания завершена") {
		t.Fatal(out, err)
	}
	lobby := testCampaign(t, ctx, s, 3)
	cmd := Command{Type: "finish_game"}
	cmd.Data.Code = lobby.Code
	if err = s.command(ctx, lobby.ID, 3, cmd); err == nil {
		t.Fatal("unstarted campaign finished")
	}
}

func TestDefeatEndingSurvivesReload(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	_, err := s.mutate(ctx, r.ID, 1, false, func(q *store.Queries, r *Room) error {
		for i := range r.State.Characters {
			r.State.Characters[i].HP = 0
		}
		r.State.Combat = true
		results, err := settleTurn(r, 1, true, nil)
		if err != nil {
			return err
		}
		rememberTurn(&r.State, results)
		for _, result := range results {
			if err = s.addEvent(ctx, q, r.ID, 1, result.Type, result); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 2)
	if err != nil || loaded.Status != "FINISHED" || loaded.State.Ending == nil || loaded.State.Ending.Reason != "defeat" || loaded.State.Turn != 1 {
		t.Fatal("defeat ending missing", loaded, err)
	}
	if !strings.Contains(describeEnding(loaded), "Выжило героев: 0/2") {
		t.Fatal("wrong defeat results")
	}
	loaded.State.Ending = nil // Old FINISHED snapshots remain readable.
	if !strings.Contains(describeEnding(loaded), "Весь отряд пал") {
		t.Fatal("legacy defeat lost")
	}
}
