package server

import (
	"dnd-bot/backend/internal/game"
	"testing"
)

func TestNegotiationSettlesCombatBeforeRetaliation(t *testing.T) {
	for _, success := range []bool{false, true} {
		r := Room{Status: "PLAYING", State: game.NewState(game.Settings{})}
		r.State.Characters = []game.Character{game.NewCharacter(1, "Hero", "", "")}
		r.State.Scene = &game.Scene{Location: "mill"}
		r.State.NPCs = []game.NPC{{ID: "thief", Name: "Thief", HP: 12, Alive: true, Disposition: "hostile", Location: "mill"}}
		r.State.Combat = true
		r.State.Characters[0].Stats.Charisma = 0
		if success {
			r.State.Characters[0].Stats.Charisma = 60
		}
		results, err := game.ApplyActions(&r.State, 1, []game.Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 25}, {Type: "SET_DISPOSITION", Target: "thief", Status: "neutral"}})
		if err != nil {
			t.Fatal(err)
		}
		results, err = settleTurn(&r, 1, true, results)
		if err != nil {
			t.Fatal(err)
		}
		ended, retaliated := false, false
		for _, result := range results {
			ended = ended || result.Type == "COMBAT_ENDED"
			retaliated = retaliated || result.Type == "NPC_ATTACK"
		}
		if ended != success || retaliated == success || r.State.Combat == success || r.Status != "PLAYING" {
			t.Fatal("negotiation outcome did not control combat", results, r.State.Combat)
		}
		if success && r.State.Characters[0].HP != 20 {
			t.Fatal("peaceful NPC damaged hero")
		}
	}
}
