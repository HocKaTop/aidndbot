package game

import (
	"errors"
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

func TestInitiativeSortAndOpeningAction(t *testing.T) {
	s := NewState(Settings{})
	for _, id := range []int64{1, 2, 3} {
		s.Characters = append(s.Characters, NewCharacter(id, "Hero", "", ""))
	}
	s.Characters[1].Stats.Dexterity = 14
	s.Combat = true
	rolls := []int{10, 9, 10}
	now := time.Now()
	results, err := s.beginInitiativeWith(now, func(notation string) (Roll, error) {
		value := rolls[0]
		rolls = rolls[1:]
		modifier := 1
		if notation == "d20+2" {
			modifier = 2
		}
		return Roll{Notation: notation, Rolls: []int{value}, Total: value + modifier}, nil
	})
	if err != nil || len(results) != 3 || !reflect.DeepEqual(s.CombatOrder, []int64{2, 1, 3}) || s.CombatRound != 0 {
		t.Fatal(results, s.CombatOrder, err)
	}
	s.AdvanceCombatTurn(now.Add(time.Second))
	if s.CombatHero().UserID != 2 || s.CombatRound != 1 {
		t.Fatal("highest initiative must take the first normal turn")
	}
	s.Combat = false
	s.EnsureCombatOrder(0, now)
	if s.NPCResponseCount != 0 {
		t.Fatal("enemy response index must reset after combat")
	}
}

func TestInitiativeRollFailureDoesNotChangeOrder(t *testing.T) {
	s := NewState(Settings{})
	s.Combat = true
	s.Characters = []Character{NewCharacter(1, "Hero", "", "")}
	_, err := s.beginInitiativeWith(time.Now(), func(string) (Roll, error) { return Roll{}, errors.New("dice unavailable") })
	if err == nil || len(s.CombatOrder) != 0 {
		t.Fatal("failed initiative modified combat order", err)
	}
}
