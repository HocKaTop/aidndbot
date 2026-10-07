package game

import "testing"

func TestAidAllyConsumesPotionAndRevives(t *testing.T) {
	state := NewState(Settings{})
	actor := NewCharacter(1, "Мира", "Человек", "Воин")
	ally := NewCharacter(2, "Олег", "Человек", "Маг")
	ally.HP = 0
	state.Characters = []Character{actor, ally}
	state.Combat = true
	state.CombatOrder = []int64{1}
	if _, err := AidAlly(&state, 1, actor.ID); err == nil {
		t.Fatal("hero aided self")
	}
	before := state.Characters[0].Inventory[0].Quantity
	result, err := AidAlly(&state, 1, ally.ID)
	if err != nil || result.Type != "ALLY_AIDED" || state.Characters[1].HP < 4 || state.Characters[1].HP > 10 || state.Characters[0].Inventory[0].Quantity != before-1 {
		t.Fatal("aid did not revive ally and consume one potion", result, err)
	}
	if len(state.CombatOrder) != 2 || state.CombatOrder[1] != 2 {
		t.Fatal("revived ally did not rejoin combat order", state.CombatOrder)
	}
	if _, err := AidAlly(&state, 1, ally.ID); err == nil {
		t.Fatal("healthy ally consumed another potion")
	}
}
