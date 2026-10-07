package ai

import (
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrContextTooLarge = errors.New("critical world state exceeds model context budget")

const DefaultContextWindow = 16384
const OutputTokens = 1024
const OpeningOutputTokens = 1280
const NarrationOutputTokens = 768

const NarrativeMaxChars = 900
const OpeningNarrativeMaxChars = 1400

func outputTokens(in Input) int {
	if in.Results != nil {
		return NarrationOutputTokens
	}
	if in.Opening {
		return OpeningOutputTokens
	}
	return OutputTokens
}

func narrativeMaxChars(in Input) int {
	if in.Opening && in.Results == nil {
		return OpeningNarrativeMaxChars
	}
	return NarrativeMaxChars
}

// UTF-8 byte length is a conservative upper bound for byte-level tokenizers.
// Reserve tokens for the answer and protocol/schema overhead. Never silently
// drop characters, inventory or current world state to make a request fit.
func prepareInput(in Input, window int) (Input, error) {
	in.State = projectStateForAI(in.State, in.Text)
	if in.Correction != nil {
		correction := *in.Correction
		correction.Actions = append([]game.Action(nil), correction.Actions...)
		for i := range correction.Actions {
			// Repair needs the action type and references, not repeated scenery.
			switch correction.Actions[i].Type {
			case "MOVE_SCENE", "CREATE_LOCATION", "CREATE_NPC", "UPDATE_NPC":
				correction.Actions[i].Description = ""
			}
		}
		in.Correction = &correction
	}
	budget := window - outputTokens(in) - 1024
	// Keep memory from crowding out all recent events. Retain its newest facts.
	in.State.Summary = tailUTF8(in.State.Summary, max(0, budget/4))
	size := func() int { data, _ := json.Marshal(in); return len(data) + len(Prompt(in)) }
	for size() > budget && len(in.Recent) > 1 {
		in.Recent = in.Recent[1:]
	}
	for size() > budget && len(in.State.Summary) > 0 {
		in.State.Summary = tailUTF8(in.State.Summary, max(0, len(in.State.Summary)-(size()-budget)))
	}
	if size() > budget {
		// Preserve references, mechanics, goals and location facts before flavor.
		if in.State.Scene != nil {
			in.State.Scene.Description = headUTF8(in.State.Scene.Description, 600)
		}
		for i := range in.State.NPCs {
			in.State.NPCs[i].Description = headUTF8(in.State.NPCs[i].Description, 400)
		}
		for i := range in.State.Characters {
			for j := range in.State.Characters[i].Inventory {
				item := &in.State.Characters[i].Inventory[j]
				item.Description = headUTF8(item.Description, 300)
			}
		}
		for i := range in.State.Locations {
			in.State.Locations[i].Description = headUTF8(in.State.Locations[i].Description, 600)
		}
	}
	for size() > budget && len(in.State.PlayerHistory) > 1 {
		in.State.PlayerHistory = in.State.PlayerHistory[1:]
	}
	for size() > budget && len(in.State.GMNotes) > 0 {
		in.State.GMNotes = in.State.GMNotes[1:]
	}
	if size() > budget && len(in.Recent) > 0 {
		in.Recent = nil
	}
	if size() > budget {
		in.State.PlayerHistory = nil
	}
	if size() > budget {
		return Input{}, ErrContextTooLarge
	}
	return in, nil
}

// Keep IDs and status of remote entities for references, while spending the
// narrative budget on the current place and entities the player named.
func projectStateForAI(state game.State, action string) game.State {
	state = state.Clone()
	action = strings.ToLower(action)
	for i := range state.NPCs {
		npc := &state.NPCs[i]
		if !state.Present(*npc) && (npc.Name == "" || !strings.Contains(action, strings.ToLower(npc.Name))) {
			npc.Description = ""
		}
	}
	for i := range state.Quests {
		if state.Quests[i].Status != "ACTIVE" {
			state.Quests[i].Description = ""
		}
	}
	for i := range state.Locations {
		place := &state.Locations[i]
		if state.Scene != nil && place.ID == state.Scene.ID {
			// The current scene already carries its full description and facts.
			place.Description = ""
			place.Facts = nil
			continue
		}
		if place.Title == "" || !placeMentioned(action, place.Title) {
			place.Description = ""
			if len(place.Facts) > 3 {
				place.Facts = place.Facts[len(place.Facts)-3:]
			}
		}
	}
	if len(state.Locations) > 16 {
		selected := make(map[string]bool, 16)
		if state.Scene != nil {
			selected[state.Scene.ID] = true
		}
		for _, place := range state.Locations {
			if place.Title != "" && placeMentioned(action, place.Title) {
				selected[place.ID] = true
			}
		}
		for i := len(state.Locations) - 1; i >= 0 && len(selected) < 16; i-- {
			selected[state.Locations[i].ID] = true
		}
		places := make([]game.Scene, 0, len(selected))
		for _, place := range state.Locations {
			if selected[place.ID] {
				places = append(places, place)
			}
		}
		state.Locations = places
	}
	return state
}

func placeMentioned(action, title string) bool {
	actionWords := strings.FieldsFunc(action, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		runes := []rune(word)
		if len(runes) > 5 {
			word = string(runes[:len(runes)-1])
		}
		found := false
		for _, candidate := range actionWords {
			if candidate == word || len(runes) > 5 && strings.HasPrefix(candidate, word) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func tailUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[len(s)-limit:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

func headUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
