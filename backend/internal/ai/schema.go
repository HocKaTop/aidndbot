package ai

import (
	"dnd-bot/backend/internal/game"
	"encoding/json"
	"strings"
)

type jsonSchema = map[string]any

// Constrain the decoder as well as validating the finished plan in the engine.
// Each tuple allows at most one mechanical action, with checks always first.
func responseSchema(in Input) json.RawMessage {
	text := jsonSchema{"type": "string", "minLength": 1}
	disposition := jsonSchema{"type": "string", "enum": []string{"hostile", "friendly", "neutral"}}
	action := func(kind string, fields jsonSchema) jsonSchema {
		// Ollama's grammar follows property order. Put the discriminator first
		// so choosing a field (e.g. target vs description) cannot pick the action.
		properties := struct {
			Type        any `json:"type"`
			Target      any `json:"target,omitempty"`
			Name        any `json:"name,omitempty"`
			Description any `json:"description,omitempty"`
			Skill       any `json:"skill,omitempty"`
			DC          any `json:"dc,omitempty"`
			Status      any `json:"status,omitempty"`
			Threat      any `json:"threat,omitempty"`
		}{jsonSchema{"const": kind}, fields["target"], fields["name"], fields["description"], fields["skill"], fields["dc"], fields["status"], fields["threat"]}
		required := []string{"type"}
		for _, key := range []string{"target", "name", "description", "skill", "dc", "status", "threat"} {
			if _, ok := fields[key]; ok {
				required = append(required, key)
			}
		}
		return jsonSchema{"type": "object", "required": required, "properties": properties, "additionalProperties": false}
	}
	defs := jsonSchema{
		"ATTACK":                   action("ATTACK", jsonSchema{"target": text}),
		"SKILL_CHECK":              action("SKILL_CHECK", jsonSchema{"skill": jsonSchema{"enum": []string{"strength", "dexterity", "constitution", "intelligence", "wisdom", "charisma"}}, "dc": jsonSchema{"type": "integer", "minimum": 5, "maximum": 25}}),
		"DICE_ROLL":                action("DICE_ROLL", jsonSchema{"name": jsonSchema{"type": "string", "pattern": game.DiceNotationPattern}}),
		"CREATE_NPC":               action("CREATE_NPC", jsonSchema{"name": text, "description": text, "status": disposition, "threat": jsonSchema{"enum": []string{"minor", "standard", "elite"}}}),
		"UPDATE_NPC":               action("UPDATE_NPC", jsonSchema{"target": text, "description": text}),
		"MOVE_NPC":                 action("MOVE_NPC", jsonSchema{"target": text, "name": text}),
		"SET_DISPOSITION":          action("SET_DISPOSITION", jsonSchema{"target": text, "status": disposition}),
		"MOVE_SCENE":               action("MOVE_SCENE", jsonSchema{"name": text, "description": text}),
		"CREATE_LOCATION":          action("CREATE_LOCATION", jsonSchema{"name": text, "description": text}),
		"REVISIT_SCENE":            action("REVISIT_SCENE", jsonSchema{"target": text}),
		"RECORD_LOCATION_FACT":     action("RECORD_LOCATION_FACT", jsonSchema{"description": jsonSchema{"type": "string", "minLength": 1, "maxLength": 300}}),
		"ADD_ITEM":                 action("ADD_ITEM", jsonSchema{"name": text, "description": text}),
		"REMOVE_ITEM":              action("REMOVE_ITEM", jsonSchema{"target": text}),
		"START_COMBAT":             action("START_COMBAT", nil),
		"END_COMBAT":               action("END_COMBAT", nil),
		"CREATE_QUEST":             action("CREATE_QUEST", jsonSchema{"name": text, "description": text}),
		"UPDATE_QUEST":             action("UPDATE_QUEST", jsonSchema{"target": text, "status": jsonSchema{"enum": []string{"COMPLETED", "FAILED"}}}),
		"PROPOSE_QUEST_COMPLETION": action("PROPOSE_QUEST_COMPLETION", jsonSchema{"target": text, "description": text}),
		"PROPOSE_QUEST_FAILURE":    action("PROPOSE_QUEST_FAILURE", jsonSchema{"target": text, "description": text}),
	}
	if in.State.Settings.LastLantern() || in.Opening || in.State.PendingQuestCompletion != nil || in.State.Combat || in.State.HasEnemies() {
		delete(defs, "PROPOSE_QUEST_COMPLETION")
		delete(defs, "PROPOSE_QUEST_FAILURE")
	}
	if in.State.Scene == nil || len(in.State.Scene.Facts) >= 20 {
		delete(defs, "RECORD_LOCATION_FACT")
	}
	if !in.State.Settings.LastLantern() {
		delete(defs, "UPDATE_QUEST")
	}
	if in.State.Settings.LastLantern() && in.Results == nil && !in.Opening {
		// The main objective has other approaches after a failed conversation;
		// ordinary roleplay does not call for an unrelated random dice roll.
		if !explicitDiceRequest(in.Text) {
			delete(defs, "DICE_ROLL")
		}
		defs["UPDATE_QUEST"] = action("UPDATE_QUEST", jsonSchema{"target": text, "status": jsonSchema{"enum": []string{"COMPLETED"}}})
	}
	// Targets must already exist. Entities created by this plan receive IDs only
	// when the engine applies it and cannot be targeted in the same response.
	localNPCs, allNPCs, quests, inventory := []string{}, []string{}, []string{}, []string{}
	for _, n := range in.State.NPCs {
		allNPCs = append(allNPCs, n.ID)
		if n.Alive && in.State.Present(n) {
			localNPCs = append(localNPCs, n.ID)
		}
	}
	for _, q := range in.State.Quests {
		if q.Status == "ACTIVE" {
			quests = append(quests, q.ID)
		}
	}
	if hero := in.State.Hero(in.PlayerID); hero != nil {
		for _, item := range hero.Inventory {
			inventory = append(inventory, item.ID)
		}
	}
	for kind, ids := range map[string][]string{"ATTACK": localNPCs, "SET_DISPOSITION": localNPCs, "UPDATE_NPC": allNPCs, "UPDATE_QUEST": quests, "PROPOSE_QUEST_COMPLETION": quests, "PROPOSE_QUEST_FAILURE": quests, "REMOVE_ITEM": inventory} {
		if _, allowed := defs[kind]; !allowed {
			continue
		}
		if len(ids) == 0 {
			delete(defs, kind)
			continue
		}
		fields := jsonSchema{"target": jsonSchema{"type": "string", "enum": ids}}
		switch kind {
		case "SET_DISPOSITION":
			fields["status"] = disposition
		case "UPDATE_NPC":
			fields["description"] = text
		case "UPDATE_QUEST":
			allowed := []string{"FAILED"}
			if in.State.Settings.LastLantern() {
				allowed = []string{"COMPLETED"}
			}
			fields["status"] = jsonSchema{"enum": allowed}
		case "PROPOSE_QUEST_COMPLETION", "PROPOSE_QUEST_FAILURE":
			fields["description"] = text
		}
		defs[kind] = action(kind, fields)
	}
	locations := []string{}
	revisitable := []string{}
	for _, place := range in.State.Locations {
		locations = append(locations, place.ID)
		if in.State.Scene == nil || place.ID != in.State.Scene.ID {
			revisitable = append(revisitable, place.ID)
		}
	}
	if len(locations) == 0 {
		delete(defs, "REVISIT_SCENE")
	} else {
		if len(revisitable) == 0 {
			delete(defs, "REVISIT_SCENE")
		} else {
			defs["REVISIT_SCENE"] = action("REVISIT_SCENE", jsonSchema{"target": jsonSchema{"type": "string", "enum": revisitable}})
		}
	}
	if len(allNPCs) == 0 {
		delete(defs, "MOVE_NPC")
	} else {
		defs["MOVE_NPC"] = action("MOVE_NPC", jsonSchema{"target": jsonSchema{"type": "string", "enum": allNPCs}, "name": text})
	}
	ref := func(name string) jsonSchema { return jsonSchema{"$ref": "#/$defs/" + name} }
	union := func(names ...string) jsonSchema {
		options := []jsonSchema{}
		for _, name := range names {
			if _, available := defs[name]; available {
				options = append(options, ref(name))
			}
		}
		if len(options) == 0 {
			return nil
		}
		return jsonSchema{"anyOf": options}
	}
	nonmechanical := []string{"CREATE_NPC", "UPDATE_NPC", "MOVE_NPC", "SET_DISPOSITION", "MOVE_SCENE", "CREATE_LOCATION", "REVISIT_SCENE", "RECORD_LOCATION_FACT", "ADD_ITEM", "REMOVE_ITEM", "START_COMBAT", "END_COMBAT", "CREATE_QUEST", "UPDATE_QUEST", "PROPOSE_QUEST_COMPLETION", "PROPOSE_QUEST_FAILURE"}
	if in.Intent != nil && in.Intent.HeldItemID != "" && in.Results == nil {
		delete(defs, "ADD_ITEM")
	}
	defs["nonmechanical"] = union(nonmechanical...)
	// Fixed tuples avoid mixing prefixItems with an items schema: Ollama's
	// grammar converter does not enforce that combination consistently.
	tuple := func(items []jsonSchema) jsonSchema {
		return jsonSchema{"type": "array", "minItems": len(items), "maxItems": len(items), "items": items, "additionalItems": false}
	}
	var actions jsonSchema
	switch {
	case in.Results != nil:
		actions = jsonSchema{"type": "array", "maxItems": 0, "items": ref("nonmechanical")}
	case in.Opening:
		defs["openingNPC"] = action("CREATE_NPC", jsonSchema{"name": text, "description": text, "status": jsonSchema{"enum": []string{"neutral", "friendly"}}})
		options := []jsonSchema{}
		for size := 2; size <= 4; size++ {
			items := []jsonSchema{ref("MOVE_SCENE"), ref("CREATE_QUEST")}
			for j := 2; j < size; j++ {
				items = append(items, ref("openingNPC"))
			}
			options = append(options, tuple(items))
		}
		actions = jsonSchema{"anyOf": options}
	default:
		required := ""
		if in.Intent != nil {
			if in.Intent.ItemRequest != "" {
				required = "ADD_ITEM"
			} else if in.Intent.GiveItemID != "" {
				required = "REMOVE_ITEM"
				defs[required] = action(required, jsonSchema{"target": jsonSchema{"const": in.Intent.GiveItemID}})
			}
		}
		if required != "" {
			others := []string{}
			for _, name := range nonmechanical {
				if name != required {
					others = append(others, name)
				}
			}
			defs["otherEffects"] = union(others...)
			options := []jsonSchema{{"type": "array", "maxItems": 0, "items": ref("otherEffects")}}
			for size := 1; size <= 4; size++ {
				for effectPosition := 0; effectPosition < size; effectPosition++ {
					for mechanicPosition := -1; mechanicPosition < size; mechanicPosition++ {
						if mechanicPosition == effectPosition {
							continue
						}
						mechanic := union("ATTACK", "DICE_ROLL")
						if mechanicPosition == 0 {
							mechanic = union("ATTACK", "DICE_ROLL", "SKILL_CHECK")
						}
						if mechanicPosition >= 0 && mechanic == nil {
							continue
						}
						items := make([]jsonSchema, size)
						for i := range items {
							items[i] = ref("otherEffects")
							if i == effectPosition {
								items[i] = ref(required)
							} else if i == mechanicPosition {
								items[i] = mechanic
							}
						}
						options = append(options, tuple(items))
					}
				}
			}
			actions = jsonSchema{"anyOf": options}
			break
		}
		options := []jsonSchema{{"type": "array", "maxItems": 4, "items": ref("nonmechanical")}}
		firstMechanic := union("ATTACK", "DICE_ROLL", "SKILL_CHECK")
		laterMechanic := union("ATTACK", "DICE_ROLL")
		for size := 1; size <= 4; size++ {
			for position := 0; position < size; position++ {
				// Some scenes have neither an attack target nor a requested
				// standalone roll. An empty anyOf cannot be parsed by Ollama.
				if (position == 0 && firstMechanic == nil) || (position > 0 && laterMechanic == nil) {
					continue
				}
				items := []jsonSchema{}
				for j := 0; j < size; j++ {
					item := ref("nonmechanical")
					if j == position {
						item = laterMechanic
						if j == 0 {
							item = firstMechanic
						}
					}
					items = append(items, item)
				}
				options = append(options, tuple(items))
			}
		}
		actions = jsonSchema{"anyOf": options}
	}
	// Select mechanics before generating prose. The grammar follows this order;
	// starting with prose can distract the model from an explicit dice request.
	properties := struct {
		Actions   any `json:"actions"`
		Narrative any `json:"narrative"`
		Memory    any `json:"memory"`
	}{actions, jsonSchema{"type": "string", "minLength": 1, "maxLength": narrativeMaxChars(in)}, jsonSchema{"type": "array", "maxItems": 10, "items": jsonSchema{"type": "string"}}}
	document := jsonSchema{"type": "object", "required": []string{"narrative", "actions", "memory"}, "properties": properties, "additionalProperties": false, "$defs": defs}
	data, _ := json.Marshal(document)
	return data
}

func explicitDiceRequest(text string) bool {
	text = strings.ToLower(text)
	for _, cue := range []string{"кубик", "кости", "брось", "бросаю", "бросить", "d20", "d6", "d8", "d10", "d12", "d100"} {
		if strings.Contains(text, cue) {
			return true
		}
	}
	return false
}
