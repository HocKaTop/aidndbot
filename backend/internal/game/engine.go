package game

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"strings"
)

type Action struct {
	Type        string `json:"type"`
	Target      string `json:"target,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Skill       string `json:"skill,omitempty"`
	DC          int    `json:"dc,omitempty"`
	Status      string `json:"status,omitempty"`
}
type Result struct {
	Type   string        `json:"type"`
	Text   string        `json:"text"`
	Roll   *Roll         `json:"roll,omitempty"`
	Attack *AttackResult `json:"attack,omitempty"`
 Success *bool `json:"success,omitempty"`
}

func ValidateAction(s *State, user int64, a Action) error {
	if len(a.Name) > 100 || len(a.Description) > 2000 {
		return errors.New("слишком длинные параметры действия")
	}
	switch a.Type {
	case "ATTACK":
		n := s.NPC(a.Target)
		if !s.Combat || n == nil || !n.Alive || n.Disposition != "hostile" || !s.Present(*n) {
			return errors.New("цель атаки недоступна")
		}
		if h := s.Hero(user); h == nil || h.HP <= 0 {
			return errors.New("персонаж не может атаковать")
		}
	case "SKILL_CHECK":
		h := s.Hero(user)
		if h == nil || h.HP <= 0 {
			return errors.New("нет активного персонажа")
		}
		if _, e := SkillModifier(h.Stats, a.Skill); e != nil {
			return e
		}
		if a.DC < 5 || a.DC > 25 {
			return errors.New("некорректная сложность")
		}
	case "CREATE_NPC":
		if strings.TrimSpace(a.Name) == "" || len(s.NPCs) >= 30 || (a.Status != "hostile" && a.Status != "friendly" && a.Status != "neutral") {
			return errors.New("некорректный NPC")
		}
	case "UPDATE_NPC":
		if s.NPC(a.Target) == nil {
			return errors.New("NPC не найден")
		}
	case "MOVE_SCENE":
		if s.Combat || strings.TrimSpace(a.Name) == "" {
			return errors.New("переход сейчас невозможен")
		}
	case "CREATE_QUEST":
		if strings.TrimSpace(a.Name) == "" || len(s.Quests) >= 30 {
			return errors.New("некорректный квест")
		}
	case "UPDATE_QUEST":
		found := false
		for _, q := range s.Quests {
			if q.ID == a.Target && q.Status=="ACTIVE" {
				found = true
			}
		}
		if !found || (a.Status != "COMPLETED" && a.Status != "FAILED") {
			return errors.New("некорректное обновление квеста")
		}
	case "START_COMBAT":
 if !s.HasEnemies()||s.Combat {return errors.New("нет противников для нового боя")}
 case "END_COMBAT":
 if !s.Combat||s.HasEnemies(){return errors.New("бой нельзя завершить, пока в сцене есть противники")}
	case "ADD_ITEM":
		if s.Hero(user) == nil || len(s.Hero(user).Inventory) >= 30 || strings.TrimSpace(a.Name) == "" {
			return errors.New("предмет недоступен")
		}
	case "REMOVE_ITEM":
		h := s.Hero(user)
		found := false
		if h != nil {
			for _, it := range h.Inventory {
				if it.ID == a.Target {
					found = true
				}
			}
		}
		if !found {
			return errors.New("предмет не найден")
		}
	case "DICE_ROLL":
		if _, e := ParseDice(a.Name); e != nil {
			return e
		}
	default:
		return errors.New("неизвестное действие модели")
	}
	return nil
}

// Apply mutates a private copy. The caller commits it only after every action succeeds.
func Apply(s *State, user int64, a Action) (Result, error) {
	out := Result{Type: a.Type}
	if e := ValidateAction(s, user, a); e != nil {
		return out, e
	}
	switch a.Type {
	case "ATTACK":
		n := s.NPC(a.Target)
		h:=s.Hero(user)
 sides:=4
 for _,item:=range h.Inventory{if item.Type=="WEAPON"&&item.Quantity>0{sides=8;break}}
 modifier:=Modifier(h.Stats.Strength)
 r, e := Attack(n.Name, n.HP, n.ArmorClass, modifier+2, fmt.Sprintf("1d%d%+d",sides,modifier))
		if e != nil {
			return out, e
		}
		n.HP = r.TargetHP
		n.Alive = n.HP > 0
		out.Attack = &r
		out.Text = fmt.Sprintf("Атака: %s, попадание: %t, урон: %d, HP цели: %d", n.Name, r.Hit, r.Damage, n.HP)
		if !s.HasEnemies() {s.Combat=false}
	case "SKILL_CHECK":
		m, _ := SkillModifier(s.Hero(user).Stats, a.Skill)
 r, e := RollDice(fmt.Sprintf("1d20%+d",m))
		if e != nil {
			return out, e
		}
success:=r.Total>=a.DC
 out.Success=&success
		out.Roll = &r
		out.Text = fmt.Sprintf("Проверка %s: %d против %d; успех: %t", a.Skill, r.Total, a.DC, r.Total >= a.DC)
	case "DICE_ROLL":
		r, e := RollDice(a.Name)
		if e != nil {
			return out, e
		}
		out.Roll = &r
		out.Text = fmt.Sprintf("%s = %d", a.Name, r.Total)
	case "CREATE_NPC":
		location := ""
		if s.Scene != nil {
			location = s.Scene.Location
		}
		s.NPCs = append(s.NPCs, NPC{uuid.NewString(), a.Name, a.Description, 12, 12, 12, true, a.Status, location})
		out.Text = "Появился NPC: " + a.Name
	case "UPDATE_NPC":
		s.NPC(a.Target).Description = a.Description
		out.Text = "Обновлено описание NPC"
	case "MOVE_SCENE":
		s.Scene = &Scene{uuid.NewString(), a.Name, a.Description, a.Name}
		out.Text = "Новая сцена: " + a.Name
	case "CREATE_QUEST":
		s.Quests = append(s.Quests, Quest{uuid.NewString(), a.Name, a.Description, "ACTIVE"})
		out.Text = "Новый квест: " + a.Name
	case "UPDATE_QUEST":
		for i := range s.Quests {
			if s.Quests[i].ID == a.Target {
				s.Quests[i].Status = a.Status
				out.Text = "Квест: " + s.Quests[i].Title + " — " + a.Status
			}
		}
	case "START_COMBAT":
		s.Combat = true
		out.Text = "Бой начался"
	case "END_COMBAT":
		s.Combat = false
		out.Text = "Бой завершён"
	case "ADD_ITEM":
		h := s.Hero(user)
		h.Inventory = append(h.Inventory, Item{uuid.NewString(), a.Name, a.Description, 1, "QUEST"})
		out.Text = "Получен предмет: " + a.Name
	case "REMOVE_ITEM":
		h := s.Hero(user)
		for i, it := range h.Inventory {
			if it.ID == a.Target {
				h.Inventory[i].Quantity--
 if h.Inventory[i].Quantity<=0{h.Inventory = append(h.Inventory[:i], h.Inventory[i+1:]...)}
 out.Text = "Удалена единица предмета: " + it.Name
				break
			}
		}
	}
	return out, nil
}
func UseItem(s *State, user int64, id string) (Result, error) {
	h := s.Hero(user)
	if h == nil || h.HP <= 0 {
		return Result{}, errors.New("нет активного персонажа")
	}
	if h.HP>=h.MaxHP {return Result{},errors.New("здоровье уже полное; зелье сохранено")}
 for i, it := range h.Inventory {
		if it.ID == id && it.Type == "HEALING" && it.Quantity > 0 {
			r, e := RollDice("2d4+2")
			if e != nil {
				return Result{}, e
			}
			h.HP = min(h.MaxHP, h.HP+r.Total)
			h.Inventory[i].Quantity--
			if h.Inventory[i].Quantity == 0 {
				h.Inventory = append(h.Inventory[:i], h.Inventory[i+1:]...)
			}
			return Result{Type: "ITEM_USED", Text: fmt.Sprintf("%s: HP %d/%d", h.Name, h.HP, h.MaxHP), Roll: &r}, nil
		}
	}
	return Result{}, errors.New("лечебное зелье не найдено")
}

// Enemies retaliate once per player action; all numbers are server-owned.
func Retaliate(s *State, user int64) ([]Result, error) {
	out := []Result{}
	h := s.Hero(user)
	if !s.Combat || h == nil {
		return out, nil
	}
	for _, n := range s.NPCs {
		if n.Alive && n.Disposition == "hostile" && s.Present(n) && h.HP > 0 {
			r, e := Attack(h.Name, h.HP, h.ArmorClass, 2, "1d4+1")
			if e != nil {
				return nil, e
			}
			h.HP = r.TargetHP
			out = append(out, Result{Type: "NPC_ATTACK", Text: fmt.Sprintf("%s атакует %s: урон %d, HP %d", n.Name, h.Name, r.Damage, h.HP), Attack: &r})
			break
		}
	}
	return out, nil
}

// Skill checks precede their consequences. Failed checks never grant rewards or moves.
// Work on the caller's private state; the enclosing transaction rolls back any error.
func ApplyActions(s *State,user int64,actions []Action)([]Result,error){
 if len(actions)>4{return nil,errors.New("максимум четыре действия за ход")}
 mechanical:=0
 for i,a:=range actions {
  switch a.Type{case "ATTACK","SKILL_CHECK","DICE_ROLL":mechanical++}
  if a.Type=="SKILL_CHECK"&&i!=0{return nil,errors.New("проверка должна предшествовать своим последствиям")}
 }
 if mechanical>1{return nil,errors.New("максимум одна атака или проверка за ход")}
 results:=[]Result{}
 failed:=false
 for _,a:=range actions {
  if failed{results=append(results,Result{Type:"ACTION_SKIPPED",Text:"Не выполнено после неудачной проверки: "+a.Type});continue}
  result,err:=Apply(s,user,a);if err!=nil{return nil,err}
  results=append(results,result)
  if result.Success!=nil&&!*result.Success{failed=true}
 }
 return results,nil
}
