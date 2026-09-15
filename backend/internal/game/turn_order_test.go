package game

import (
	"reflect"
	"testing"
	"time"
)

func TestCombatOrderAndDeath(t *testing.T) {
	s := NewState(Settings{})
	for _, id := range []int64{1, 2, 3} {
		s.Characters = append(s.Characters, NewCharacter(id, "Hero", "", ""))
	}
	s.Combat = true
	now := time.Now()
	s.EnsureCombatOrder(2, now)
	if !reflect.DeepEqual(s.CombatOrder, []int64{2, 1, 3}) || s.CombatHero().UserID != 2 {
		t.Fatal(s.CombatOrder)
	}
	copy := s.Clone()
	copy.CombatOrder[0] = 99
	if s.CombatOrder[0] != 2 {
		t.Fatal("clone shares combat order")
	}
	s.AdvanceCombatTurn(now.Add(time.Second))
	if s.CombatHero().UserID != 1 || s.CombatRound != 1 {
		t.Fatal("wrong next hero")
	}
	s.Hero(3).HP = 0
	s.AdvanceCombatTurn(now.Add(2 * time.Second))
	if s.CombatHero().UserID != 2 || s.CombatRound != 2 || !s.CombatTurnSince.Equal(now.Add(2*time.Second)) {
		t.Fatal("dead hero not skipped or timer wrong")
	}
	s.Combat = false
	s.AdvanceCombatTurn(now)
	if s.CombatHero() != nil || len(s.CombatOrder) != 0 || !s.CombatTurnSince.IsZero() {
		t.Fatal("battle order retained after combat")
	}
}
