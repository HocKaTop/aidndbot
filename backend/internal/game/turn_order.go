package game

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type initiativeEntry struct {
	userID int64
	total  int
	dex    int
}

// The opening action happens before initiative. Once it resolves, the first
// normal round starts with the highest initiative result.
func (s *State) BeginInitiative(now time.Time) ([]Result, error) {
	return s.beginInitiativeWith(now, RollDice)
}

func (s *State) beginInitiativeWith(now time.Time, roll func(string) (Roll, error)) ([]Result, error) {
	entries := make([]initiativeEntry, 0, len(s.Characters))
	results := make([]Result, 0, len(s.Characters))
	for _, hero := range s.Characters {
		if hero.HP <= 0 {
			continue
		}
		modifier := Modifier(hero.Stats.Dexterity)
		r, err := roll(fmt.Sprintf("d20%+d", modifier))
		if err != nil {
			return nil, err
		}
		entries = append(entries, initiativeEntry{hero.UserID, r.Total, hero.Stats.Dexterity})
		results = append(results, Result{Type: "INITIATIVE_ROLL", Text: fmt.Sprintf("Инициатива %s: %d", hero.Name, r.Total), Roll: &r})
	}
	if len(entries) == 0 {
		return nil, errors.New("нет живых героев для инициативы")
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].total != entries[j].total {
			return entries[i].total > entries[j].total
		}
		if entries[i].dex != entries[j].dex {
			return entries[i].dex > entries[j].dex
		}
		return entries[i].userID < entries[j].userID
	})
	s.CombatOrder = make([]int64, 0, len(entries))
	for _, entry := range entries {
		s.CombatOrder = append(s.CombatOrder, entry.userID)
	}
	s.CombatIndex = len(s.CombatOrder) - 1
	s.CombatRound = 0
	s.CombatTurnSince = now
	s.NPCResponseCount = 0
	return results, nil
}

func (s *State) CombatHero() *Character {
	if !s.Combat || s.CombatIndex < 0 || s.CombatIndex >= len(s.CombatOrder) {
		return nil
	}
	h := s.Hero(s.CombatOrder[s.CombatIndex])
	if h == nil || h.HP <= 0 {
		return nil
	}
	return h
}

// A battle starts with its initiator, then follows the saved party order.
func (s *State) EnsureCombatOrder(first int64, now time.Time) {
	if !s.Combat {
		s.CombatOrder = nil
		s.CombatIndex, s.CombatRound = 0, 0
		s.CombatTurnSince = time.Time{}
		s.NPCResponseCount = 0
		return
	}
	if len(s.CombatOrder) == 0 {
		if h := s.Hero(first); h != nil && h.HP > 0 {
			s.CombatOrder = append(s.CombatOrder, first)
		}
		for _, h := range s.Characters {
			if h.UserID != first && h.HP > 0 {
				s.CombatOrder = append(s.CombatOrder, h.UserID)
			}
		}
		s.CombatIndex, s.CombatRound = 0, 1
		s.CombatTurnSince = now
	}
	if s.CombatHero() == nil {
		s.AdvanceCombatTurn(now)
	}
}

func (s *State) AdvanceCombatTurn(now time.Time) {
	if !s.Combat {
		s.EnsureCombatOrder(0, now)
		return
	}
	for range s.CombatOrder {
		s.CombatIndex++
		if s.CombatIndex >= len(s.CombatOrder) {
			s.CombatIndex = 0
			s.CombatRound++
		}
		if s.CombatHero() != nil {
			s.CombatTurnSince = now
			return
		}
	}
}
