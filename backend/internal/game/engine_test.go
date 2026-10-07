package game

import (
	"errors"
	"strings"
	"testing"
)

func TestNegotiationRequiresCheckAndChangesOnlyLivingLocalNPC(t *testing.T) {
	for _, success := range []bool{false, true} {
		s := NewState(Settings{})
		s.Characters = []Character{NewCharacter(1, "Hero", "", "")}
		s.Scene = &Scene{Location: "mill"}
		s.NPCs = []NPC{{ID: "thief", HP: 12, MaxHP: 12, Alive: true, Disposition: "hostile", Location: "mill"}}
		s.Combat = true
		peace := Action{Type: "SET_DISPOSITION", Target: "thief", Status: "neutral"}
		if _, err := ApplyActions(&s, 1, []Action{peace}); err == nil {
			t.Fatal("free combat escape accepted")
		}
		s.Characters[0].Stats.Charisma = 0
		if success {
			s.Characters[0].Stats.Charisma = 60
		}
		results, err := ApplyActions(&s, 1, []Action{{Type: "SKILL_CHECK", Skill: "charisma", DC: 25}, peace})
		if err != nil || *results[0].Success != success {
			t.Fatal(err, results)
		}
		want := "hostile"
		if success {
			want = "neutral"
		}
		if s.NPCs[0].Disposition != want || s.NPCs[0].HP != 12 {
			t.Fatal("negotiation ignored check or changed HP")
		}
		for _, npc := range []NPC{{ID: "dead", Alive: false, Location: "mill"}, {ID: "remote", Alive: true, Location: "forest"}} {
			s.NPCs = append(s.NPCs, npc)
			if ValidateAction(&s, 1, Action{Type: "SET_DISPOSITION", Target: npc.ID, Status: "friendly"}) == nil {
				t.Fatal("changed dead/remote NPC")
			}
		}
	}
}

func TestReturnToKnownLocationKeepsNPCAndDescription(t *testing.T) {
	s := NewState(Settings{})
	s.Scene = &Scene{ID: "tavern", Title: "Таверна", Description: "У очага тепло.", Location: "Таверна"}
	s.NPCs = []NPC{{ID: "keeper", Name: "Хозяин", Alive: true, Location: "Таверна"}}
	if _, err := Apply(&s, 1, Action{Type: "RECORD_LOCATION_FACT", Description: "Под стойкой спрятан ключ."}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(&s, 1, Action{Type: "MOVE_SCENE", Name: "Лес", Description: "Тёмная тропа."}); err != nil {
		t.Fatal(err)
	}
	if s.Present(s.NPCs[0]) || s.NPCs[0].LocationID != "tavern" {
		t.Fatal("NPC не привязан к прежнему месту")
	}
	forest := s.Scene.ID
	if ValidateAction(&s, 1, Action{Type: "MOVE_SCENE", Name: "Таверна", Description: "Другая таверна"}) == nil {
		t.Fatal("повторное создание известного места разрешено")
	}
	if _, err := Apply(&s, 1, Action{Type: "REVISIT_SCENE", Target: "tavern"}); err != nil {
		t.Fatal(err)
	}
	if s.Scene.ID != "tavern" || s.Scene.Description != "У очага тепло." || len(s.Scene.Facts) != 1 || s.Scene.Facts[0] != "Под стойкой спрятан ключ." || !s.Present(s.NPCs[0]) {
		t.Fatal("возвращение не восстановило место и его NPC", s.Scene, s.NPCs[0])
	}
	if len(s.Scene.Exits) != 1 || s.Scene.Exits[0] != forest {
		t.Fatal("переход между местами не сохранён", s.Scene.Exits)
	}
	if _, err := Apply(&s, 1, Action{Type: "MOVE_NPC", Target: "keeper", Name: forest}); err != nil {
		t.Fatal(err)
	}
	if s.Present(s.NPCs[0]) || s.NPCs[0].LocationID != forest {
		t.Fatal("NPC не переместился в выбранное место")
	}
}

func TestNPCLeavesForNewPlaceWithoutMovingParty(t *testing.T) {
	s := NewState(Settings{})
	s.Scene = &Scene{ID: "hall", Title: "Зал", Location: "Зал"}
	s.RememberLocation(*s.Scene)
	s.NPCs = []NPC{{ID: "passenger", Name: "Пассажирка", Alive: true, Location: "Зал", LocationID: "hall"}}
	actions := []Action{{Type: "CREATE_LOCATION", Name: "Багажное отделение", Description: "Стеклянные перегородки."}, {Type: "MOVE_NPC", Target: "passenger", Name: "Багажное отделение"}}
	if err := ValidateActions(&s, 1, actions); err != nil || len(s.Locations) != 1 || s.NPCs[0].LocationID != "hall" {
		t.Fatal("validation changed the world or rejected the departure", s, err)
	}
	if _, err := ApplyActions(&s, 1, actions); err != nil {
		t.Fatal(err)
	}
	if s.Scene.ID != "hall" || len(s.Locations) != 2 || s.Present(s.NPCs[0]) || s.NPCs[0].LocationID != s.Locations[1].ID {
		t.Fatal("NPC departure moved the party or lost its destination", s)
	}
	if len(s.Scene.Exits) != 1 || s.Scene.Exits[0] != s.NPCs[0].LocationID {
		t.Fatal("discovered exit was not stored", s.Scene)
	}
	if _, err := ApplyActions(&s, 1, []Action{{Type: "MOVE_SCENE", Name: "Платформа"}, {Type: "MOVE_NPC", Target: "passenger", Name: "@current"}}); err != nil || !s.Present(s.NPCs[0]) {
		t.Fatal("NPC could not accompany the party to a newly generated location", s, err)
	}
	if ValidateAction(&s, 1, Action{Type: "MOVE_NPC", Target: "passenger", Name: "Неизвестное место"}) == nil {
		t.Fatal("unknown destination was accepted")
	}
	s.Locations = append(s.Locations, Scene{ID: "other", Title: "Багажное отделение"})
	if s.ResolveLocation("Багажное отделение") != nil {
		t.Fatal("ambiguous old location names were resolved silently")
	}
}

func TestCreateNPCRejectsExistingAliasAndTutorialItem(t *testing.T) {
	s := NewState(Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод: вернуть огненный камень."})
	s.Scene = &Scene{ID: "bridge", Title: "Мост"}
	s.NPCs = []NPC{{ID: "thief", Name: "Похититель", Alive: true, LocationID: "bridge"}}
	for _, name := range []string{"Мельник-похититель", "Огненный камень"} {
		if err := ValidateAction(&s, 1, Action{Type: "CREATE_NPC", Name: name, Status: "neutral"}); err == nil {
			t.Fatalf("создание %q должно быть отклонено", name)
		}
	}
	if err := ValidateAction(&s, 1, Action{Type: "CREATE_NPC", Name: "Стражник", Status: "neutral"}); err != nil {
		t.Fatal(err)
	}
	if npcNameAlias("Страж", "Стражник") || npcNameAlias("Стражник восточный", "Стражник западный") {
		t.Fatal("разных NPC приняли за одного")
	}
}

func TestTutorialKeepsItsOriginalGoal(t *testing.T) {
	s := NewState(Settings{Name: "Последний фонарь", WorldDescription: "Тихий Брод: вернуть огненный камень."})
	goal := Action{Type: "CREATE_QUEST", Name: "Вернуть камень"}
	if _, err := Apply(&s, 1, goal); err != nil {
		t.Fatal("initial tutorial goal rejected", err)
	}
	if err := ValidateAction(&s, 1, Action{Type: "CREATE_QUEST", Name: "Смягчить сердце похитителя"}); err == nil {
		t.Fatal("tutorial was extended with another quest")
	}
	s.Settings.Name = "Свободное приключение"
	if err := ValidateAction(&s, 1, Action{Type: "CREATE_QUEST", Name: "Помочь мельнику"}); err != nil {
		t.Fatal("custom campaign cannot add a goal", err)
	}
}

func TestDice(t *testing.T) {
	for _, s := range []string{"d20", "d6", "2d6", "2d6+3", "1d20+5", "1d8-1"} {
		d, e := ParseDice(s)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 100; i++ {
			r, e := RollDice(s)
			if e != nil || r.Total < d.Count+d.Modifier || r.Total > d.Count*d.Sides+d.Modifier {
				t.Fatal(r, e)
			}
		}
	}
	for _, s := range []string{"", "0d6", "d1", "21d6", "d1001", "1d6+101", "d6;rm", "-1d6", "999999999d6"} {
		if _, e := ParseDice(s); e == nil {
			t.Fatal(s)
		}
	}
}
func TestCombat(t *testing.T) {
	if Hits(1, 100, 1) || !Hits(20, 0, 100) || !Hits(10, 3, 13) || Hits(9, 3, 13) {
		t.Fatal("hit rules")
	}
	if ApplyDamage(3, 8) != 0 || ApplyDamage(3, -1) != 3 || Modifier(9) != -1 {
		t.Fatal("bounds")
	}
}

func TestAttackCriticalAndDeath(t *testing.T) {
	for _, tc := range []struct {
		natural int
		armor   int
		wantHit bool
		wantHP  int
	}{
		{1, 1, false, 9},
		{20, 99, true, 0},
		{10, 12, true, 2},
	} {
		calls := 0
		result, err := attackWithRoll("Enemy", 9, tc.armor, 2, "1d8+2", func(notation string) (Roll, error) {
			calls++
			switch notation {
			case "d20":
				return Roll{Notation: notation, Rolls: []int{tc.natural}, Total: tc.natural}, nil
			case "1d8+2":
				return Roll{Notation: notation, Rolls: []int{5}, Total: 7}, nil
			default:
				return Roll{}, errors.New("unexpected dice")
			}
		})
		if err != nil || result.Hit != tc.wantHit || result.TargetHP != tc.wantHP {
			t.Fatal("attack outcome", tc, result, err)
		}
		if tc.natural == 1 && calls != 1 || tc.natural == 20 && result.Damage != 14 {
			t.Fatal("critical damage or miss rules", tc, result, calls)
		}
	}
}

func TestEnemyResponsesRotateAndDefenseChangesArmor(t *testing.T) {
	s := NewState(Settings{})
	hero := NewCharacter(1, "Hero", "", "")
	hero.MaxHP, hero.HP = 100, 100
	s.Characters = []Character{hero}
	s.Scene = &Scene{Location: "mill"}
	s.NPCs = []NPC{
		{Name: "First", HP: 12, Alive: true, Disposition: "hostile", Location: "mill"},
		{Name: "Remote", HP: 12, Alive: true, Disposition: "hostile", Location: "forest"},
		{Name: "Second", HP: 12, Alive: true, Disposition: "hostile", Location: "mill"},
	}
	s.Combat = true
	for i, name := range []string{"First", "Second", "First"} {
		bonus := 0
		if i == 1 {
			bonus = 2
		}
		before := s.Hero(1).HP
		results, err := Retaliate(&s, 1, bonus)
		if err != nil || len(results) != 1 || !strings.HasPrefix(results[0].Text, name+" атакует") {
			t.Fatal("wrong enemy response", i, results, err)
		}
		attack := results[0].Attack
		if attack.Hit != Hits(attack.Roll.Total, 2, 13+bonus) || s.Hero(1).HP != before-attack.Damage {
			t.Fatal("defense or damage ignored", attack, before, s.Hero(1).HP)
		}
	}
	if s.NPCResponseCount != 3 {
		t.Fatal("enemy response position not saved")
	}
}
func TestActionValidation(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = append(s.Characters, NewCharacter(1, "A", "human", "warrior"))
	for _, a := range []Action{{Type: "SET_HP"}, {Type: "ATTACK", Target: "missing"}, {Type: "SKILL_CHECK", Skill: "strength", DC: 100}, {Type: "REMOVE_ITEM", Target: "missing"}, {Type: "START_COMBAT"}} {
		if ValidateAction(&s, 1, a) == nil {
			t.Fatal(a)
		}
	}
	if _, e := Apply(&s, 1, Action{Type: "CREATE_NPC", Name: "Goblin", Status: "hostile"}); e != nil {
		t.Fatal(e)
	}
	if _, e := Apply(&s, 1, Action{Type: "START_COMBAT"}); e != nil {
		t.Fatal(e)
	}
	if ValidateAction(&s, 1, Action{Type: "MOVE_SCENE", Name: "elsewhere"}) == nil {
		t.Fatal("escaped combat")
	}
}

func TestCampaignFinaleRequiresCompletedGoalsAndNoCombat(t *testing.T) {
	s := NewState(Settings{Name: "Экспедиция"})
	s.Characters = []Character{NewCharacter(1, "Hero", "Human", "Warrior")}
	s.Quests = []Quest{{ID: "signal", Status: "ACTIVE"}}
	finish := Action{Type: "FINISH_CAMPAIGN"}
	if err := ValidateActions(&s, 1, []Action{finish}); err == nil {
		t.Fatal("active quest allowed finale")
	}
	plan := []Action{{Type: "UPDATE_QUEST", Target: "signal", Status: "COMPLETED"}, finish}
	if err := ValidateActions(&s, 1, plan); err == nil {
		t.Fatal("AI completed custom objective without confirmation")
	}
	if err := ValidateActions(&s, 1, []Action{{Type: "UPDATE_QUEST", Target: "signal", Status: "FAILED"}}); err == nil {
		t.Fatal("AI failed custom objective without confirmation")
	}
	failure := Action{Type: "PROPOSE_QUEST_FAILURE", Target: "signal", Description: "Передатчик уничтожен."}
	if err := ValidateActions(&s, 1, []Action{failure}); err != nil {
		t.Fatal(err)
	}
	proposal := Action{Type: "PROPOSE_QUEST_COMPLETION", Target: "signal", Description: "Передатчик включён и сигнал принят."}
	if err := ValidateActions(&s, 1, []Action{proposal}); err != nil {
		t.Fatal(err)
	}
	s.Combat = true
	if err := ValidateActions(&s, 1, []Action{proposal}); err == nil {
		t.Fatal("combat proposal accepted")
	}
	s.Combat = false
	results, err := ApplyActions(&s, 1, []Action{proposal})
	if err != nil || len(results) != 1 || results[0].Type != "PROPOSE_QUEST_COMPLETION" || s.Quests[0].Status != "ACTIVE" || s.PendingQuestCompletion == nil {
		t.Fatal(results, err)
	}
	if err := ValidateActions(&s, 1, []Action{proposal}); err == nil {
		t.Fatal("duplicate proposal accepted")
	}
	if s.PendingQuestCompletion.ID == "" || s.PendingQuestCompletion.Status != "COMPLETED" {
		t.Fatal("proposal has no stable outcome identity")
	}
}

func TestEnemyThreatChangesCombatValues(t *testing.T) {
	for _, tc := range []struct {
		threat string
		hp     int
		ac     int
		bonus  int
	}{
		{"minor", 8, 10, 1},
		{"standard", 12, 12, 2},
		{"elite", 24, 14, 4},
	} {
		s := NewState(Settings{})
		hero := NewCharacter(1, "Герой", "Человек", "Воин")
		hero.HP, hero.MaxHP = 100, 100
		s.Characters = []Character{hero}
		s.Scene = &Scene{ID: "hall", Title: "Зал", Location: "Зал"}
		result, err := Apply(&s, 1, Action{Type: "CREATE_NPC", Name: "Враг", Description: "Страж", Status: "hostile", Threat: tc.threat})
		if err != nil || result.Type != "CREATE_NPC" || s.NPCs[0].HP != tc.hp || s.NPCs[0].ArmorClass != tc.ac {
			t.Fatal(tc, result, err)
		}
		s.Combat = true
		attack, err := Retaliate(&s, 1, 0)
		if err != nil || len(attack) != 1 || attack[0].Attack.Hit != Hits(attack[0].Attack.Roll.Total, tc.bonus, hero.ArmorClass) {
			t.Fatal("threat profile was not used for retaliation", tc, attack, err)
		}
	}
}

func TestSceneAndInventoryRules(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = append(s.Characters, NewCharacter(1, "Hero", "Human", "Warrior"))
	s.Scene = &Scene{Location: "tavern"}
	s.NPCs = []NPC{{ID: "remote", HP: 12, Alive: true, Disposition: "hostile", Location: "forest"}}
	if ValidateAction(&s, 1, Action{Type: "START_COMBAT"}) == nil {
		t.Fatal("remote enemy started combat")
	}
	s.Combat = true
	if ValidateAction(&s, 1, Action{Type: "ATTACK", Target: "remote"}) == nil {
		t.Fatal("remote attack accepted")
	}
	results, e := Retaliate(&s, 1, 0)
	if e != nil || len(results) != 0 || s.Characters[0].HP != 20 {
		t.Fatal("remote enemy retaliated", results, e)
	}
	potion := s.Characters[0].Inventory[0].ID
	if _, e = UseItem(&s, 1, potion); e == nil || s.Characters[0].Inventory[0].Quantity != 2 {
		t.Fatal("full health consumed potion")
	}
	s.Characters[0].HP = 19
	if _, e = UseItem(&s, 1, potion); e != nil || s.Characters[0].HP != 20 || s.Characters[0].Inventory[0].Quantity != 1 {
		t.Fatal("healing boundaries", e)
	}
	s.Characters[0].Inventory[0].Quantity = 2
	if _, e = Apply(&s, 1, Action{Type: "REMOVE_ITEM", Target: potion}); e != nil || s.Characters[0].Inventory[0].Quantity != 1 {
		t.Fatal("removed entire stack", e)
	}
	s.Quests = []Quest{{ID: "done", Status: "COMPLETED"}}
	if ValidateAction(&s, 1, Action{Type: "UPDATE_QUEST", Target: "done", Status: "FAILED"}) == nil {
		t.Fatal("terminal quest changed")
	}
}
func TestSkillCheckConsequences(t *testing.T) {
	s := NewState(Settings{})
	s.Characters = append(s.Characters, NewCharacter(1, "Hero", "Human", "Warrior"))
	actions := []Action{{Type: "SKILL_CHECK", Skill: "strength", DC: 25}, {Type: "ADD_ITEM", Name: "Treasure"}}
	out, e := ApplyActions(&s, 1, actions)
	if e != nil || out[0].Success == nil || *out[0].Success || out[1].Type != "ACTION_SKIPPED" || len(s.Characters[0].Inventory) != 2 {
		t.Fatal("failed check consequences", out, e)
	}
	s.Characters[0].Stats.Strength = 60
	if out, e = ApplyActions(&s, 1, actions); e != nil || !*out[0].Success || len(s.Characters[0].Inventory) != 3 {
		t.Fatal("successful check consequences", e)
	}
	if _, e = ApplyActions(&s, 1, []Action{actions[1], actions[0]}); e == nil {
		t.Fatal("reward before check")
	}
	if _, e = ApplyActions(&s, 1, []Action{actions[0], {Type: "DICE_ROLL", Name: "d20"}}); e == nil {
		t.Fatal("multiple checks accepted")
	}
}
