package game

import "testing"

func TestRestOnlyOnceInSafeLocation(t *testing.T) {
	state := NewState(Settings{})
	hero := NewCharacter(1, "Герой", "Человек", "Воин")
	hero.HP = 5
	state.Characters = []Character{hero}
	state.Scene = &Scene{ID: "camp", Title: "Лагерь", Location: "Лагерь"}
	state.NPCs = []NPC{{ID: "enemy", Alive: true, Disposition: "hostile", Location: "Лагерь"}}
	if _, err := ShortRest(&state, 1); err == nil {
		t.Fatal("rest allowed beside an enemy")
	}
	state.NPCs = nil
	before := state.Characters[0].HP
	result, err := ShortRest(&state, 1)
	if err != nil || result.Type != "SHORT_REST" || state.Characters[0].HP <= before || state.Characters[0].LastRestLocationID != "camp" {
		t.Fatal("safe rest did not heal", result, err)
	}
	if _, err := ShortRest(&state, 1); err == nil {
		t.Fatal("rest repeated in the same place")
	}
	state.Scene = &Scene{ID: "tavern", Title: "Таверна", Location: "Таверна"}
	state.Characters[0].HP = 5
	if _, err := ShortRest(&state, 1); err != nil {
		t.Fatal("first rest in another location failed", err)
	}
	state.Scene = &Scene{ID: "camp", Title: "Лагерь", Location: "Лагерь"}
	state.Characters[0].HP = 5
	if _, err := ShortRest(&state, 1); err == nil {
		t.Fatal("returning to an old location allowed a second rest")
	}
}
