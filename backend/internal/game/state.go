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
	ID                 string   `json:"id"`
	UserID             int64    `json:"userId"`
	Name               string   `json:"name"`
	Race               string   `json:"race"`
	Class              string   `json:"class"`
	ClassID            string   `json:"classId,omitempty"`
	Level              int      `json:"level"`
	HP                 int      `json:"hp"`
	MaxHP              int      `json:"maxHp"`
	ArmorClass         int      `json:"armorClass"`
	Experience         int      `json:"experience"`
	Resource           int      `json:"resource,omitempty"`
	ResourceMax        int      `json:"resourceMax,omitempty"`
	LastRestLocationID string   `json:"lastRestLocationId,omitempty"`
	RestedLocationIDs  []string `json:"restedLocationIds,omitempty"`
	Stats              Stats    `json:"stats"`
	Inventory          []Item   `json:"inventory"`
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
	LocationID  string `json:"locationId,omitempty"`
	Threat      string `json:"threat,omitempty"`
}
type Quest struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Rewarded    bool   `json:"rewarded,omitempty"`
}
type Scene struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Location    string   `json:"location"`
	Facts       []string `json:"facts,omitempty"`
	Exits       []string `json:"exits,omitempty"`
}
type QuestCompletionProposal struct {
	ID      string `json:"id"`
	QuestID string `json:"questId"`
	Reason  string `json:"reason"`
	Status  string `json:"status,omitempty"`
}
type State struct {
	Ending                 *Ending                  `json:"ending,omitempty"`
	PendingQuestCompletion *QuestCompletionProposal `json:"pendingQuestCompletion,omitempty"`
	Settings               Settings                 `json:"settings"`
	Characters             []Character              `json:"characters"`
	NPCs                   []NPC                    `json:"npcs"`
	Quests                 []Quest                  `json:"quests"`
	Scene                  *Scene                   `json:"scene"`
	Locations              []Scene                  `json:"locations,omitempty"`
	Combat                 bool                     `json:"combat"`
	CombatOrder            []int64                  `json:"combatOrder,omitempty"`
	CombatIndex            int                      `json:"combatIndex"`
	CombatRound            int                      `json:"combatRound"`
	CombatTurnSince        time.Time                `json:"combatTurnSince"`
	NPCResponseCount       int                      `json:"npcResponseCount,omitempty"`
	Summary                string                   `json:"summary"`
	PlayerHistory          []string                 `json:"playerHistory,omitempty"`
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
		s.Characters[i].RestedLocationIDs = slices.Clone(s.Characters[i].RestedLocationIDs)
	}
	s.NPCs = slices.Clone(s.NPCs)
	s.Quests = slices.Clone(s.Quests)
	s.Locations = slices.Clone(s.Locations)
	for i := range s.Locations {
		s.Locations[i].Facts = slices.Clone(s.Locations[i].Facts)
		s.Locations[i].Exits = slices.Clone(s.Locations[i].Exits)
	}
	s.GMNotes = slices.Clone(s.GMNotes)
	s.PlayerHistory = slices.Clone(s.PlayerHistory)
	if s.Scene != nil {
		scene := *s.Scene
		scene.Facts = slices.Clone(scene.Facts)
		scene.Exits = slices.Clone(scene.Exits)
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
	if n.LocationID != "" {
		return n.LocationID == s.Scene.ID
	}
	return n.Location == s.Scene.Location
}

func (s *State) Location(id string) *Scene {
	for i := range s.Locations {
		if s.Locations[i].ID == id {
			return &s.Locations[i]
		}
	}
	return nil
}

// A plan can refer to a newly created place by title, or to the party's new
// scene after MOVE_SCENE. Saved NPCs always retain the resolved location ID.
func (s *State) ResolveLocation(ref string) *Scene {
	if ref == "@current" {
		if s.Scene != nil && s.Scene.ID != "" {
			return s.Scene
		}
		return nil
	}
	if place := s.Location(ref); place != nil {
		return place
	}
	var found *Scene
	for i := range s.Locations {
		if strings.EqualFold(strings.TrimSpace(s.Locations[i].Title), strings.TrimSpace(ref)) {
			if found != nil {
				return nil // Ambiguous legacy names require an explicit ID.
			}
			found = &s.Locations[i]
		}
	}
	return found
}

// RememberLocation also upgrades rooms created before the location list existed.
func (s *State) RememberLocation(scene Scene) {
	if scene.ID == "" || s.Location(scene.ID) != nil {
		return
	}
	s.Locations = append(s.Locations, scene)
	for i := range s.NPCs {
		if s.NPCs[i].LocationID == "" && s.NPCs[i].Location == scene.Location {
			s.NPCs[i].LocationID = scene.ID
		}
	}
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
