package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestNarrationRepairKeepsRollAndConsumesItemOnce(t *testing.T) {
	for _, recover := range []bool{true, false} {
		t.Run(map[bool]string{true: "repair", false: "engine fallback"}[recover], func(t *testing.T) {
			ctx, s, _ := reviewServer(t)
			r := multiplayerRoom(t, ctx, s)
			item := r.State.Hero(1).Inventory[0]
			plans, narrations := 0, 0
			var results, world string
			s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
				if in.Results == nil {
					plans++
					return ai.Output{Narrative: "Проверим путь.", Actions: []game.Action{{Type: "DICE_ROLL", Name: "d20"}, {Type: "REMOVE_ITEM", Target: item.ID}}}, nil
				}
				narrations++
				if narrations == 1 {
					results, world = string(blob(in.Results)), string(blob(in.State))
					// A valid JSON response with forbidden new actions is also repaired.
					return ai.Output{Narrative: "Сделаю ещё бросок", Actions: []game.Action{{Type: "DICE_ROLL", Name: "d20"}}}, nil
				}
				if string(blob(in.Results)) != results || string(blob(in.State)) != world || in.ResponseCorrection == "" {
					t.Fatal("repair rerolled dice or changed world")
				}
				if !recover {
					return ai.Output{}, ai.ErrInvalidResponse
				}
				return ai.Output{Narrative: "Вы осмотрели путь и оставили зелье у моста.", Actions: []game.Action{}}, nil
			})
			cmd := Command{ID: uuid.NewString(), Type: "player_action"}
			cmd.Data.Text = "Осматриваю путь и оставляю зелье"
			err := s.command(ctx, r.ID, 1, cmd)
			if err != nil || plans != 1 || narrations != 2 {
				t.Fatal("unexpected repair attempts", plans, narrations, err)
			}
			saved, err := s.load(ctx, r.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			events, err := s.eventList(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			var notices, receipts int
			if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM telegram_outbox WHERE room_id=$1", r.ID).Scan(&notices); err != nil {
				t.Fatal(err)
			}
			if err = s.Pool.QueryRow(ctx, "SELECT count(*) FROM command_receipts WHERE room_id=$1", r.ID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if saved.State.Turn != 1 || saved.State.Hero(1).Inventory[0].Quantity != item.Quantity-1 || notices != 2 || receipts != 1 {
				t.Fatal("repair repeated or lost effects", saved.State, notices, receipts)
			}
			if !recover && !strings.Contains(string(events[len(events)-1].Payload), "Результат хода:") {
				t.Fatal("fallback did not describe confirmed engine results")
			}
			counts := map[string]int{}
			for _, event := range events {
				counts[event.Type]++
			}
			if counts["DICE_ROLL"] != 1 || counts["PLAYER_ACTION"] != 1 || counts["GM_MESSAGE"] != 1 {
				t.Fatal("repeated events", counts)
			}
			// A later client retry still uses the durable receipt, without new model calls.
			if err = s.command(ctx, r.ID, 1, cmd); err != nil || plans != 1 || narrations != 2 {
				t.Fatal("client retry repeated repaired turn", err)
			}
		})
	}
}

func TestNearDeadlineKeepsEngineResultsAndReceipt(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	r := multiplayerRoom(t, ctx, s)
	calls := 0
	s.AI = openingProvider(func(_ context.Context, _ string, in ai.Input) (ai.Output, error) {
		calls++
		if in.Results != nil {
			t.Fatal("narration should leave the remaining time for persistence")
		}
		return ai.Output{Narrative: "Бросаем кубик.", Actions: []game.Action{{Type: "DICE_ROLL", Name: "d20"}}}, nil
	})
	cmd := Command{ID: uuid.NewString(), Type: "player_action"}
	cmd.Data.Text = "Брось d20."
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.command(commandCtx, r.ID, 1, cmd); err != nil {
		t.Fatal("turn was lost near its deadline", err)
	}
	if err := s.command(ctx, r.ID, 1, cmd); err != nil || calls != 1 {
		t.Fatal("retry rerolled an already committed turn", calls, err)
	}
	saved, err := s.load(ctx, r.ID, 1)
	if err != nil || saved.State.Turn != 1 {
		t.Fatal("turn did not persist", saved.State.Turn, err)
	}
}
