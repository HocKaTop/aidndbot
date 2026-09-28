package game

import (
	"github.com/google/uuid"
	"slices"
	"strings"
	"time"
)

type Settings struct {
	Name             string `json:"name"`
	Setting          string `json:"setting"`
	WorldDescription string `json:"worldDescription"`
	Tone             string `json:"tone"`
	Rules            string `json:"rules"`
	Difficulty       string `json:"difficulty"`
	GMStyle          string `json:"gmStyle"`
	OllamaModel      string `json:"ollamaModel"`
	MaxPlayers       int    `json:"maxPlayers"`
}

// Existing quick adventures did not store a scenario ID, so recognize their
// original template as well as newly created rooms without changing old data.
func (s Settings) LastLantern() bool {
	description := strings.ToLower(s.WorldDescription)
	return s.Name == "Последний фонарь" && strings.Contains(description, "тихий брод") && strings.Contains(description, "огненный камень")
}

type Stats struct {
	Strength     int `json:"strength"`
	Dexterity    int `json:"dexterity"`
	Constitution int `json:"constitution"`
	Intelligence int `json:"intelligence"`
	Wisdom       int `json:"wisdom"`
	Charisma     int `json:"charisma"`
}
type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Quantity    int    `json:"quantity"`
	Type        string `json:"type"`
}
type Character struct {
	ID          string `json:"id"`
	UserID      int64  `json:"userId"`
	Name        string `json:"name"`
	Race        string `json:"race"`
	Class       string `json:"class"`
	ClassID     string `json:"classId,omitempty"`
	Level       int    `json:"level"`
	HP          int    `json:"hp"`
	MaxHP       int    `json:"maxHp"`
	ArmorClass  int    `json:"armorClass"`
	Experience  int    `json:"experience"`
	Resource    int    `json:"resource,omitempty"`
	ResourceMax int    `json:"resourceMax,omitempty"`
	Stats       Stats  `json:"stats"`
	Inventory   []Item `json:"inventory"`
}
type NPC struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	HP          int    `json:"hp"`
	MaxHP       int    `json:"maxHp"`
	ArmorClass  int    `json:"armorClass"`
	Alive       bool   `json:"alive"`
	Disposition string `json:"disposition"`
	Location    string `json:"location"`
}
type Quest struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Rewarded    bool   `json:"rewarded,omitempty"`
}
type Scene struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Location    string `json:"location"`
}
type QuestCompletionProposal struct {
	QuestID string `json:"questId"`
	Reason  string `json:"reason"`
}
type State struct {
	Ending                 *Ending                  `json:"ending,omitempty"`
	PendingQuestCompletion *QuestCompletionProposal `json:"pendingQuestCompletion,omitempty"`
	Settings               Settings                 `json:"settings"`
	Characters             []Character              `json:"characters"`
	NPCs                   []NPC                    `json:"npcs"`
	Quests                 []Quest                  `json:"quests"`
	Scene                  *Scene                   `json:"scene"`
	Combat                 bool                     `json:"combat"`
	CombatOrder            []int64                  `json:"combatOrder,omitempty"`
	CombatIndex            int                      `json:"combatIndex"`
	CombatRound            int                      `json:"combatRound"`
	CombatTurnSince        time.Time                `json:"combatTurnSince"`
	NPCResponseCount       int                      `json:"npcResponseCount,omitempty"`
	Summary                string                   `json:"summary"`
	GMNotes                []string                 `json:"gmNotes,omitempty"`
	Turn                   int                      `json:"turn"`
}

type Ending struct {
	Reason     string    `json:"reason"`
	Note       string    `json:"note"`
	FinishedAt time.Time `json:"finishedAt"`
}

func NewState(s Settings) State {
	return State{Settings: s, Characters: []Character{}, NPCs: []NPC{}, Quests: []Quest{}}
}

// Clone isolates speculative validation from the authoritative state.
func (s State) Clone() State {
	if s.Ending != nil {
		ending := *s.Ending
		s.Ending = &ending
	}
	s.CombatOrder = slices.Clone(s.CombatOrder)
	s.Characters = slices.Clone(s.Characters)
	for i := range s.Characters {
		s.Characters[i].Inventory = slices.Clone(s.Characters[i].Inventory)
	}
	s.NPCs = slices.Clone(s.NPCs)
	s.Quests = slices.Clone(s.Quests)
	s.GMNotes = slices.Clone(s.GMNotes)
	if s.Scene != nil {
		scene := *s.Scene
		s.Scene = &scene
	}
	if s.PendingQuestCompletion != nil {
		proposal := *s.PendingQuestCompletion
		s.PendingQuestCompletion = &proposal
	}
	return s
}
func NewCharacter(user int64, name, race, class string) Character {
	return Character{ID: uuid.NewString(), UserID: user, Name: name, Race: race, Class: class, Level: 1, HP: 20, MaxHP: 20, ArmorClass: 13, Stats: Stats{14, 12, 14, 12, 10, 10}, Inventory: []Item{{uuid.NewString(), "Зелье лечения", "Восстанавливает 2d4+2 HP", 2, "HEALING"}, {uuid.NewString(), "Меч", "Урон 1d8+2", 1, "WEAPON"}}}
}
func (s *State) Hero(user int64) *Character {
	for i := range s.Characters {
		if s.Characters[i].UserID == user {
			return &s.Characters[i]
		}
	}
	return nil
}
func (s *State) NPC(id string) *NPC {
	for i := range s.NPCs {
		if s.NPCs[i].ID == id {
			return &s.NPCs[i]
		}
	}
	return nil
}

func (s *State) Present(n NPC) bool {
	if s.Scene == nil {
		return n.Location == ""
	}
	return n.Location == s.Scene.Location
}
func (s *State) HasEnemies() bool {
	for _, n := range s.NPCs {
		if n.Alive && n.Disposition == "hostile" && s.Present(n) {
			return true
		}
	}
	return false
}
func (s *State) PartyDefeated() bool {
	if len(s.Characters) == 0 {
		return false
	}
	for _, h := range s.Characters {
		if h.HP > 0 {
			return false
		}
	}
	return true
}
