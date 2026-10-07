package game

import "testing"

func TestRetreatRequiresKnownExitAndResolvesBothOutcomes(t *testing.T) {
	state := NewState(Settings{})
	state.Characters = []Character{NewCharacter(1, "Герой", "Человек", "Воин")}
	state.Scene = &Scene{ID: "mill", Title: "Мельница", Exits: []string{"bridge"}}
	state.Locations = []Scene{*state.Scene, {ID: "bridge", Title: "Мост"}}
	state.Combat = true
	if _, err := retreatWithRoll(&state, 1, "unknown", nil); err == nil {
		t.Fatal("retreat to an unknown place was accepted")
	}
	failure, err := retreatWithRoll(&state, 1, "bridge", func(string) (Roll, error) { return Roll{Total: 1}, nil })
	if err != nil || failure.Success == nil || *failure.Success || state.Scene.ID != "mill" || !state.Combat {
		t.Fatal("failed retreat changed the scene", failure, err)
	}
	success, err := retreatWithRoll(&state, 1, "bridge", func(string) (Roll, error) { return Roll{Total: 20}, nil })
	if err != nil || success.Success == nil || !*success.Success || state.Scene.ID != "bridge" || state.Combat {
		t.Fatal("successful retreat did not move the party", success, err)
	}
}
