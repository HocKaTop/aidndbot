package game

import (
	"errors"
	"fmt"
)

// Retreat moves the whole party to a known adjacent location on a successful
// dexterity check. Failure leaves them in combat for the enemy response.
func Retreat(state *State, user int64, destination string) (Result, error) {
	return retreatWithRoll(state, user, destination, RollDice)
}

func retreatWithRoll(state *State, user int64, destination string, roll func(string) (Roll, error)) (Result, error) {
	hero := state.Hero(user)
	if !state.Combat || hero == nil || hero.HP <= 0 || state.Scene == nil {
		return Result{}, errors.New("сейчас отступить нельзя")
	}
	adjacent := false
	for _, id := range state.Scene.Exits {
		adjacent = adjacent || id == destination
	}
	place := state.Location(destination)
	if !adjacent || place == nil {
		return Result{}, errors.New("нужен известный выход из текущей сцены")
	}
	r, err := roll(fmt.Sprintf("1d20%+d", Modifier(hero.Stats.Dexterity)))
	if err != nil {
		return Result{}, err
	}
	success := r.Total >= 12
	result := Result{Type: "RETREAT", Roll: &r, Success: &success}
	if !success {
		result.Text = fmt.Sprintf("%s не смог отступить к месту «%s» (%d против 12).", hero.Name, place.Title, r.Total)
		return result, nil
	}
	previous := *state.Scene
	state.RememberLocation(previous)
	next := *place
	state.Scene = &next
	state.Combat = false
	state.CombatOrder = nil
	state.CombatIndex = 0
	state.NPCResponseCount = 0
	result.Text = fmt.Sprintf("Отряд отступил к месту «%s» (%d против 12).", place.Title, r.Total)
	return result, nil
}
