package game

import "testing"

func TestProgressionRewardsLivingPartyAndCapsLevel(t *testing.T) {
	hero, _ := NewClassCharacter(1, "Воин", "Человек", "Воин")
	fallen, _ := NewClassCharacter(2, "Плут", "Человек", "Плут")
	fallen.HP = 0
	s := NewState(Settings{})
	s.Characters = []Character{hero, fallen}
	rewards := []Result{
		{Type: "ATTACK", Attack: &AttackResult{TargetHP: 0}},
		{Type: "UPDATE_QUEST", Status: "COMPLETED"},
	}
	out := AwardProgress(&s, rewards)
	if len(out) != 2 || out[0].Type != "EXPERIENCE_GAINED" || out[1].Type != "LEVEL_UP" {
		t.Fatal("reward or level event missing", out)
	}
	if h := s.Hero(1); h.Experience != 130 || h.Level != 2 || h.HP != 29 || h.MaxHP != 29 || h.ResourceMax != 3 || Proficiency(h.Level) != 3 {
		t.Fatal("wrong warrior level two", h)
	}
	if h := s.Hero(2); h.Experience != 0 || h.Level != 1 {
		t.Fatal("fallen hero received experience", h)
	}
	AwardProgress(&s, rewards)
	if h := s.Hero(1); h.Experience != 260 || h.Level != 3 || h.MaxHP != 34 || h.ResourceMax != 4 || Proficiency(h.Level) != 4 {
		t.Fatal("wrong level three", h)
	}
	AwardProgress(&s, rewards)
	if h := s.Hero(1); h.Level != MaxLevel || h.Experience != 390 {
		t.Fatal("level cap or experience failed", h)
	}
	if len(AwardProgress(&s, []Result{{Type: "UPDATE_QUEST", Status: "FAILED"}, {Type: "ATTACK", Attack: &AttackResult{TargetHP: 1}}})) != 0 {
		t.Fatal("failed quest or surviving enemy awarded experience")
	}
}
