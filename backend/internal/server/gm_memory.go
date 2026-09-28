package server

import (
	"dnd-bot/backend/internal/game"
	"strings"
)

const maxGMNotes = 24
const maxGMNotesBytes = 4000

// Keep a bounded set of private story facts. Repeated facts move to the end so
// current plot threads survive longer campaigns without growing the prompt.
func rememberGMNotes(state *game.State, facts []string) {
	for _, raw := range facts {
		fact := strings.TrimSpace(raw)
		if fact == "" || len(fact) > 500 {
			continue
		}
		// Private notes describe the world, not facts about a player's body,
		// possessions or choices. Those require a validated game action.
		aboutHero := false
		for _, hero := range state.Characters {
			if hero.Name != "" && strings.Contains(strings.ToLower(fact), strings.ToLower(hero.Name)) {
				aboutHero = true
				break
			}
		}
		if aboutHero {
			continue
		}
		for i, old := range state.GMNotes {
			if strings.EqualFold(old, fact) {
				state.GMNotes = append(state.GMNotes[:i], state.GMNotes[i+1:]...)
				break
			}
		}
		state.GMNotes = append(state.GMNotes, fact)
	}
	bytes := 0
	for _, note := range state.GMNotes {
		bytes += len(note)
	}
	for len(state.GMNotes) > maxGMNotes || bytes > maxGMNotesBytes {
		bytes -= len(state.GMNotes[0])
		state.GMNotes = state.GMNotes[1:]
	}
}
