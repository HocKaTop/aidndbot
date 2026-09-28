package game

import "fmt"

const MaxLevel = 3

func Proficiency(level int) int {
	return 2 + min(max(level-1, 0), MaxLevel-1)
}

func AwardProgress(s *State, turn []Result) []Result {
	xp := 0
	for _, result := range turn {
		if (result.Type == "ATTACK" || result.Type == "CLASS_ATTACK") && result.Attack != nil && result.Attack.TargetHP == 0 {
			xp += 30
		}
		if result.Type == "UPDATE_QUEST" && result.Status == "COMPLETED" {
			xp += 100
		}
	}
	if xp == 0 {
		return nil
	}
	out := []Result{}
	for i := range s.Characters {
		hero := &s.Characters[i]
		if hero.HP <= 0 {
			continue
		}
		hero.Experience += xp
		out = append(out, Result{Type: "EXPERIENCE_GAINED", Text: fmt.Sprintf("%s получает %d опыта (всего %d).", hero.Name, xp, hero.Experience)})
		for hero.Level < MaxLevel && hero.Experience >= []int{0, 100, 250}[hero.Level] {
			hero.Level++
			gain := 4
			switch hero.ClassID {
			case "warrior":
				gain = 5
			case "mage":
				gain = 3
			}
			hero.MaxHP += gain
			hero.HP += gain
			if hero.ClassID != "" {
				hero.ResourceMax++
				hero.Resource++
			}
			out = append(out, Result{Type: "LEVEL_UP", Text: fmt.Sprintf("%s достигает уровня %d: +%d HP, бонус мастерства +%d.", hero.Name, hero.Level, gain, Proficiency(hero.Level))})
		}
	}
	return out
}
