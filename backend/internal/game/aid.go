package game

import (
	"errors"
	"fmt"
)

// AidAlly consumes one healing potion from the acting hero to revive a fallen
// party member. The surrounding turn still gives an enemy its response.
func AidAlly(state *State, user int64, targetID string) (Result, error) {
	actor := state.Hero(user)
	if actor == nil || actor.HP <= 0 {
		return Result{}, errors.New("нужен живой персонаж")
	}
	var target *Character
	for i := range state.Characters {
		if state.Characters[i].ID == targetID && state.Characters[i].UserID != user {
			target = &state.Characters[i]
			break
		}
	}
	if target == nil || target.HP > 0 {
		return Result{}, errors.New("нужен павший союзник")
	}
	for i, item := range actor.Inventory {
		if item.Type != "HEALING" || item.Quantity < 1 {
			continue
		}
		roll, err := RollDice("2d4+2")
		if err != nil {
			return Result{}, err
		}
		target.HP = min(target.MaxHP, roll.Total)
		if state.Combat {
			found := false
			for _, queued := range state.CombatOrder {
				found = found || queued == target.UserID
			}
			if !found {
				state.CombatOrder = append(state.CombatOrder, target.UserID)
			}
		}
		actor.Inventory[i].Quantity--
		if actor.Inventory[i].Quantity == 0 {
			actor.Inventory = append(actor.Inventory[:i], actor.Inventory[i+1:]...)
		}
		return Result{Type: "ALLY_AIDED", Text: fmt.Sprintf("%s помогает %s: HP %d/%d", actor.Name, target.Name, target.HP, target.MaxHP), Roll: &roll}, nil
	}
	return Result{}, errors.New("нужно лечебное зелье")
}
