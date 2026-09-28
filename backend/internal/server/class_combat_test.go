package server

import (
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"github.com/google/uuid"
	"testing"
)

func TestClassAbilityAndTargetedAttackPersist(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	hero, err := game.NewClassCharacter(1, "Олег", "Человек", "Воин")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.New(s.Pool).UpsertUser(ctx, store.UpsertUserParams{ID: 1, FirstName: "Олег"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.createCampaign(ctx, 1, game.Settings{Name: "Class fight", MaxPlayers: 2}, &hero)
	if err != nil {
		t.Fatal(err)
	}
	target := uuid.NewString()
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Scene = &game.Scene{ID: uuid.NewString(), Title: "Мельница", Location: "Мельница"}
		r.State.NPCs = []game.NPC{{ID: target, Name: "Похититель", HP: 100, MaxHP: 100, ArmorClass: 12, Alive: true, Disposition: "neutral", Location: "Мельница"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Type: "class_ability"}
	cmd.Data.Ability, cmd.Data.Target = "power_strike", target
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Turn != 1 || !loaded.State.Combat || loaded.State.Hero(1).Resource != 1 || loaded.State.CombatHero().UserID != 1 {
		t.Fatal("class ability did not persist", loaded.State, err)
	}
	events, err := s.eventList(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	initiative, attack := false, false
	for _, event := range events {
		initiative = initiative || event.Type == "INITIATIVE_ROLL"
		attack = attack || event.Type == "CLASS_ATTACK"
	}
	if !initiative || !attack {
		t.Fatal("class attack or initiative event missing", events)
	}
	cmd = Command{Type: "attack_npc"}
	cmd.Data.Target = target
	if err = s.command(ctx, r.ID, 1, cmd); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Turn != 2 || loaded.State.Hero(1).Resource != 1 || loaded.State.NPCResponseCount != 2 {
		t.Fatal("targeted attack did not preserve class resource and combat", err)
	}
}

func TestTextClassAbilitySelectsPresentTarget(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	if err := store.New(s.Pool).UpsertUser(ctx, store.UpsertUserParams{ID: 1, FirstName: "Маг"}); err != nil {
		t.Fatal(err)
	}
	hero, _ := game.NewClassCharacter(1, "Маг", "Человек", "Маг")
	r, err := s.createCampaign(ctx, 1, game.Settings{Name: "Magic", MaxPlayers: 1}, &hero)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
		r.Status = "PLAYING"
		r.State.Scene = &game.Scene{ID: uuid.NewString(), Title: "Мельница", Location: "Мельница"}
		r.State.NPCs = []game.NPC{{ID: uuid.NewString(), Name: "Похититель", HP: 100, MaxHP: 100, ArmorClass: 12, Alive: true, Disposition: "neutral", Location: "Мельница"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.textMessage(ctx, auth.User{ID: 1, FirstName: "Маг"}, "/firebolt Похититель"); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.load(ctx, r.ID, 1)
	if err != nil || loaded.State.Turn != 1 || loaded.State.Hero(1).Resource != 3 || !loaded.State.Combat {
		t.Fatal("text spell did not start persisted combat", err)
	}
}

func TestLastLanternFightAndFinaleWithEachClass(t *testing.T) {
	ctx, s, _ := reviewServer(t)
	if err := store.New(s.Pool).UpsertUser(ctx, store.UpsertUserParams{ID: 1, FirstName: "Герой"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		class, ability string
	}{
		{"Воин", "power_strike"},
		{"Плут", "precise_strike"},
		{"Маг", "firebolt"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			hero, err := game.NewClassCharacter(1, "Герой", "Человек", tc.class)
			if err != nil {
				t.Fatal(err)
			}
			initialResourceMax := hero.ResourceMax
			hero.HP, hero.MaxHP = 1000, 1000 // Keep this route test about state transitions, not random survival.
			r, err := s.createCampaign(ctx, 1, game.Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод: пропал огненный камень", MaxPlayers: 1}, &hero)
			if err != nil {
				t.Fatal(err)
			}
			target := uuid.NewString()
			_, err = s.mutate(ctx, r.ID, 1, false, func(_ *store.Queries, r *Room) error {
				r.Status = "PLAYING"
				r.State.Scene = &game.Scene{ID: uuid.NewString(), Title: "Старая мельница", Location: "Старая мельница"}
				r.State.NPCs = []game.NPC{{ID: target, Name: "Похититель", HP: 1, MaxHP: 1, ArmorClass: 1, Alive: true, Disposition: "neutral", Location: "Старая мельница"}}
				r.State.Quests = []game.Quest{{ID: uuid.NewString(), Title: "Вернуть свет", Status: "ACTIVE"}}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			cmd := Command{Type: "class_ability"}
			cmd.Data.Ability, cmd.Data.Target = tc.ability, target
			if err = s.command(ctx, r.ID, 1, cmd); err != nil {
				t.Fatal(err)
			}
			for tries := 0; tries < 30; tries++ {
				r, err = s.load(ctx, r.ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				if !r.State.NPCs[0].Alive {
					break
				}
				cmd = Command{Type: "attack_npc"}
				cmd.Data.Target = target
				if err = s.command(ctx, r.ID, 1, cmd); err != nil {
					t.Fatal(err)
				}
			}
			r, err = s.load(ctx, r.ID, 1)
			if err != nil || r.State.NPCs[0].Alive || r.State.Combat || r.State.Hero(1).Experience != 30 {
				t.Fatal("class fight did not resolve", err)
			}
			for _, kind := range []string{"claim_stone", "return_to_bridge", "install_stone"} {
				if err = s.command(ctx, r.ID, 1, Command{Type: kind}); err != nil {
					t.Fatal(kind, err)
				}
			}
			r, err = s.load(ctx, r.ID, 1)
			if err != nil || r.Status != "FINISHED" || r.State.Ending == nil || r.State.Ending.Reason != "objective" || r.State.Hero(1).Experience != 130 || r.State.Hero(1).Level != 2 || r.State.Hero(1).ResourceMax != initialResourceMax+1 {
				t.Fatal("class finale or progression missing", err)
			}
		})
	}
}
