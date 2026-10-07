package game

import (
	"errors"
	"fmt"
	"slices"
)

// ShortRest is available once per location to a living hero outside combat.
func ShortRest(state *State, user int64) (Result, error) {
	hero := state.Hero(user)
	if hero == nil || hero.HP <= 0 || state.Scene == nil || state.Scene.ID == "" || state.Combat || state.HasEnemies() {
		return Result{}, errors.New("здесь нельзя безопасно отдохнуть")
	}
	if hero.HP >= hero.MaxHP {
		return Result{}, errors.New("здоровье уже полное")
	}
	if hero.LastRestLocationID == state.Scene.ID || slices.Contains(hero.RestedLocationIDs, state.Scene.ID) {
		return Result{}, errors.New("в этом месте герой уже отдыхал")
	}
	roll, err := RollDice(fmt.Sprintf("1d6%+d", Modifier(hero.Stats.Constitution)))
	if err != nil {
		return Result{}, err
	}
	hero.HP = min(hero.MaxHP, hero.HP+max(1, roll.Total))
	if hero.LastRestLocationID != "" && !slices.Contains(hero.RestedLocationIDs, hero.LastRestLocationID) {
		hero.RestedLocationIDs = append(hero.RestedLocationIDs, hero.LastRestLocationID)
	}
	hero.RestedLocationIDs = append(hero.RestedLocationIDs, state.Scene.ID)
	hero.LastRestLocationID = state.Scene.ID
	return Result{Type: "SHORT_REST", Text: fmt.Sprintf("%s отдохнул: HP %d/%d", hero.Name, hero.HP, hero.MaxHP), Roll: &roll}, nil
}
