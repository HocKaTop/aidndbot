package game

import (
	"errors"
	"fmt"
)

// UseAbility owns all combat effects. The caller applies it to a private room
// state and commits only if the whole turn, including retaliation, succeeds.
func UseAbility(s *State, user int64, ability, target string) ([]Result, error) {
	hero := s.Hero(user)
	if hero == nil || hero.HP <= 0 || hero.ClassID == "" {
		return nil, errors.New("классовое действие недоступно")
	}
	class, cost, attack, bonus, damage, armor := "", 0, false, 0, "", 0
	switch ability {
	case "power_strike":
		class, cost, attack = "warrior", 1, true
		bonus, damage = Modifier(hero.Stats.Strength)+Proficiency(hero.Level), fmt.Sprintf("1d8%+d", Modifier(hero.Stats.Strength)+2)
	case "guard":
		class, cost, armor = "warrior", 1, 4
	case "precise_strike":
		class, cost, attack = "rogue", 1, true
		bonus, damage = Modifier(hero.Stats.Dexterity)+Proficiency(hero.Level)+2, fmt.Sprintf("1d6%+d", Modifier(hero.Stats.Dexterity))
	case "evade":
		class, cost, armor = "rogue", 1, 4
	case "firebolt":
		class, attack = "mage", true
		bonus, damage = Modifier(hero.Stats.Intelligence)+Proficiency(hero.Level), fmt.Sprintf("1d8%+d", Modifier(hero.Stats.Intelligence))
	case "barrier":
		class, cost, armor = "mage", 1, 4
	default:
		return nil, errors.New("неизвестная способность")
	}
	if hero.ClassID != class {
		return nil, errors.New("эта способность недоступна твоему классу")
	}
	if !attack {
		if !s.Combat || !s.HasEnemies() {
			return nil, errors.New("защитная способность доступна только в бою")
		}
		if hero.Resource < cost {
			return nil, errors.New("не хватает классового ресурса")
		}
		hero.Resource -= cost
		return []Result{{Type: "CLASS_DEFEND", Text: fmt.Sprintf("%s использует %s: +%d AC против ответной атаки.", hero.Name, ability, armor), ArmorBonus: armor}}, nil
	}
	npc := s.NPC(target)
	if npc == nil || !npc.Alive || !s.Present(*npc) {
		return nil, errors.New("цель способности недоступна")
	}
	results := []Result{}
	if npc.Disposition != "hostile" {
		result, err := Apply(s, user, Action{Type: "SET_DISPOSITION", Target: npc.ID, Status: "hostile"})
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if !s.Combat {
		result, err := Apply(s, user, Action{Type: "START_COMBAT"})
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if hero.Resource < cost {
		return nil, errors.New("не хватает классового ресурса")
	}
	result, err := Attack(npc.Name, npc.HP, npc.ArmorClass, bonus, damage)
	if err != nil {
		return nil, err
	}
	hero.Resource -= cost
	npc.HP, npc.Alive = result.TargetHP, result.TargetHP > 0
	results = append(results, Result{Type: "CLASS_ATTACK", Text: fmt.Sprintf("%s использует %s против %s: попадание %t, урон %d, HP цели %d", hero.Name, ability, npc.Name, result.Hit, result.Damage, npc.HP), Attack: &result})
	if !s.HasEnemies() {
		s.Combat = false
	}
	return results, nil
}
