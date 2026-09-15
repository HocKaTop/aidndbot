package game

import "time"

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
