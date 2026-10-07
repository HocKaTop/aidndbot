package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"os"
	"strings"
	"testing"
	"time"
)

// Uses only an invented in-memory campaign: no Telegram messages or real rooms.
func TestLiveAdventureJourney(t *testing.T) {
	endpoint, model := os.Getenv("OLLAMA_SMOKE_URL"), os.Getenv("OLLAMA_SMOKE_MODEL")
	if endpoint == "" || model == "" {
		t.Skip("set OLLAMA_SMOKE_URL and OLLAMA_SMOKE_MODEL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	s := &Server{AI: ai.New(endpoint)}
	r := Room{Status: "WAITING", State: game.NewState(game.Settings{Name: "Последний фонарь", OllamaModel: model, Setting: "Тёмное фэнтези", Tone: "Таинственный, с надеждой", WorldDescription: "Короткое приключение. У моста деревни Тихий Брод погас фонарь: пропал огненный камень. Смотрительница Мира видела следы к старой мельнице. Там напуганный похититель, с которым можно договориться. Нужно вернуть камень к мосту, зажечь фонарь и закончить квест эпилогом. Начни у моста с разговора, без боя."})}
	r.State.Characters = []game.Character{game.NewCharacter(1, "Олег", "Человек", "Воин"), game.NewCharacter(2, "Анна", "Эльф", "Следопыт")}
	r.State.Characters[0].Stats.Charisma = 60 // This route checks model choices, not random negotiation failure.
	r.State.Characters[0].Stats.Wisdom = 60
	recent := []string{}
	input := ai.Input{Opening: true, State: r.State, PlayerID: 1, Text: "Открой приключение для отряда."}
	out, err := s.planTurn(ctx, "live-journey", input)
	if err != nil {
		t.Fatal("opening:", err)
	}
	if _, err = game.ApplyActions(&r.State, 1, out.Actions); err != nil {
		t.Fatal(err)
	}
	rememberGMNotes(&r.State, out.Memory)
	r.Status = "PLAYING"
	mainQuest := r.State.Quests[0].ID
	startScene := r.State.Scene.ID
	recent = append(recent, out.Narrative)
	t.Log("Opening:", out.Narrative)
	stoneIDs := map[string]bool{}
	steps := []string{
		"Спрашиваю Миру, что она видела, и осматриваю следы: куда идти за камнем?",
		"Вместе с Анной иду по следам к старой мельнице и осматриваю вход.",
		"Вхожу в мельницу и спокойно зову того, кто взял камень. Обещаю выслушать его и помочь.",
		"Объясняю, что без камня деревня погибнет в тумане. Прошу вернуть его; мы поможем решить проблему мирно.",
		"Прошу передать огненный камень и протягиваю ладони, чтобы принять его. Обещаю помочь и благодарю за доверие.",
		"Вместе с Анной возвращаюсь к мосту с огненным камнем.",
		"Устанавливаю камень обратно в фонарь и зажигаю его. Проверяю, что мост и деревня снова защищены.",
	}
	for i, text := range steps {
		stepCtx, stop := context.WithTimeout(ctx, 150*time.Second)
		input = ai.Input{State: r.State, Recent: recent, PlayerID: 1, Text: text}
		wasCombat := r.State.Combat
		results, handled, err := applyLanternIntent(&r.State, 1, text)
		out = ai.Output{}
		if err == nil && !handled {
			out, err = s.planTurn(stepCtx, "live-journey", input)
			if err == nil {
				results, err = game.ApplyActions(&r.State, 1, out.Actions)
			}
		}
		if err == nil {
			var appeared *game.Result
			appeared, err = ensureLanternThief(&r.State, 1)
			if appeared != nil {
				results = append(results, *appeared)
			}
		}
		if err == nil {
			results, err = settleTurn(&r, 1, wasCombat, results)
		}
		if err != nil {
			stop()
			t.Fatal(err)
		}
		if handled && len(results) == 1 && results[0].Type == "ITEM_ALREADY_HELD" {
			out.Narrative = results[0].Text
		} else if len(results) > 0 {
			input.State, input.Results = r.State.Clone(), results
			rememberGMNotes(&input.State, out.Memory)
			narration, e := s.narrateTurn(stepCtx, "live-journey", input)
			if e != nil {
				stop()
				t.Fatal(e)
			}
			out.Narrative = narration.Narrative
			out.Memory = append(out.Memory, narration.Memory...)
		}
		stop()
		rememberTurn(&r.State, results)
		rememberPlayerAction(&r.State, r.State.Hero(1).Name, text)
		rememberGMNotes(&r.State, out.Memory)
		for _, item := range r.State.Hero(1).Inventory {
			if item.Type == "QUEST" && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень") {
				stoneIDs[item.ID] = true
			}
		}
		recent = append(recent, text, out.Narrative)
		t.Logf("Step %d: actions=%s; results=%s; %s", i+1, blob(out.Actions), blob(results), out.Narrative)
		if i == 0 && r.State.Scene.ID != startScene {
			t.Fatal("asking for directions moved the party")
		}
		if i == 4 && len(stoneIDs) == 0 {
			t.Fatal("player explicitly took the stone, but the model did not give a quest item")
		}
		if i == 5 && r.State.Scene.ID != startScene {
			t.Fatal("returning to the bridge created a different location")
		}
		for _, quest := range r.State.Quests {
			if quest.ID != mainQuest || quest.Status != "COMPLETED" {
				continue
			}
			if len(stoneIDs) == 0 {
				t.Fatal("quest completed without obtaining the stone")
			}
			for _, item := range r.State.Hero(1).Inventory {
				if stoneIDs[item.ID] {
					t.Fatal("quest completed but installed stone is still in inventory", item)
				}
			}
		}
		if r.State.Combat || r.Status == "FINISHED" && (r.State.Ending == nil || r.State.Ending.Reason != "objective") {
			t.Fatal("peaceful route unexpectedly became combat/defeat")
		}
	}
	completed := false
	for _, quest := range r.State.Quests {
		completed = completed || quest.ID == mainQuest && quest.Status == "COMPLETED"
	}
	if !completed || r.Status != "FINISHED" || r.State.Ending == nil || r.State.Ending.Reason != "objective" {
		t.Fatal("story did not reach the verified tutorial ending", r.State.Quests, r.State.Ending)
	}
	if len(stoneIDs) == 0 {
		t.Fatal("quest completed without obtaining the stone")
	}
	for _, item := range r.State.Hero(1).Inventory {
		if stoneIDs[item.ID] {
			t.Fatal("quest completed while stone remains in inventory", item)
		}
	}
	t.Log(describeEnding(r))
}
