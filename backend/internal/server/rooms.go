package server

import (
	"context"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type Member struct {
	UserID int64  `json:"userId"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Ready  bool   `json:"ready"`
}
type Room struct {
	Activity  Activity   `json:"activity"`
	ID        string     `json:"id"`
	Code      string     `json:"code"`
	OwnerID   int64      `json:"ownerId"`
	Status    string     `json:"status"`
	State     game.State `json:"state"`
	Members   []Member   `json:"members"`
	InviteURL string     `json:"inviteUrl"`
}

// GM notes persist with the room but must never be included in player-facing
// room responses. The AI receives them through its separate world-state input.
func (r Room) MarshalJSON() ([]byte, error) {
	public := r
	public.State.GMNotes = nil
	type roomJSON Room
	return json.Marshal(roomJSON(public))
}

type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"createdAt"`
}

func CanManage(owner, user int64) bool { return owner == user }
func isMember(m []Member, user int64) bool {
	for _, v := range m {
		if v.UserID == user {
			return true
		}
	}
	return false
}
func (s *Server) room(ctx context.Context, q *store.Queries, raw store.Room) (Room, error) {
	out := Room{ID: key(raw.ID), Code: raw.Code, OwnerID: raw.OwnerID, Status: raw.Status, Members: []Member{}}
	if s.Hub != nil {
		out.Activity = s.Hub.Activity(out.ID)
	}
	if e := json.Unmarshal(raw.State, &out.State); e != nil {
		return out, e
	}
	if proposal := out.State.PendingQuestCompletion; proposal != nil && proposal.ID == "" {
		// Give pre-ID proposals a stable identity across reads until the room is saved.
		proposal.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(proposal.QuestID+"\x00"+proposal.Reason+"\x00"+fmt.Sprint(out.State.Turn))).String()
	}
	if len(out.State.Locations) == 0 {
		// Rooms saved before persistent locations still have their scene history
		// in the scenes table. Load it once, then persist it with the next turn.
		if out.State.Scene != nil {
			out.State.RememberLocation(*out.State.Scene)
		}
		past, err := q.ListScenes(ctx, raw.ID)
		if err != nil {
			return out, err
		}
		for _, data := range past {
			var scene game.Scene
			if err := json.Unmarshal(data, &scene); err != nil {
				return out, err
			}
			out.State.RememberLocation(scene)
		}
	}
	out.State.EnsureCombatOrder(0, raw.UpdatedAt.Time)
	ms, e := q.ListMembers(ctx, raw.ID)
	if e != nil {
		return out, e
	}
	for _, m := range ms {
		out.Members = append(out.Members, Member{m.UserID, m.FirstName, m.Role, m.Ready})
	}
	if s.Config.BotUsername != "" {
		out.InviteURL = "https://t.me/" + s.Config.BotUsername
		if s.Config.AppName != "" {
			out.InviteURL += "/" + s.Config.AppName
		}
		out.InviteURL += "?startapp=" + url.QueryEscape(out.Code)
	}
	return out, nil
}
func (s *Server) load(ctx context.Context, roomID string, user int64) (Room, error) {
	u, e := id(roomID)
	if e != nil {
		return Room{}, bad("Некорректный ID комнаты")
	}
	q := store.New(s.Pool)
	raw, e := q.GetRoom(ctx, u)
	if e != nil {
		return Room{}, e
	}
	out, e := s.room(ctx, q, raw)
	if e != nil {
		return out, e
	}
	if !isMember(out.Members, user) {
		return out, denied()
	}
	return out, nil
}

// Every mutation takes a database row lock, including HTTP and runtime commands.
func (s *Server) mutate(ctx context.Context, roomID string, user int64, allowJoin bool, fn func(*store.Queries, *Room) error) (Room, error) {
	u, e := id(roomID)
	if e != nil {
		return Room{}, bad("Некорректный ID комнаты")
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return Room{}, e
	}
	defer tx.Rollback(context.Background())
	q := store.New(tx)
	raw, e := q.LockRoomNowait(ctx, u)
	if e != nil {
		var pgerr *pgconn.PgError
		if errors.As(e, &pgerr) && pgerr.Code == "55P03" {
			return Room{}, &apiError{409, "ROOM_BUSY", "В этой комнате уже выполняется действие. Дождись ответа мастера."}
		}
		return Room{}, e
	}
	room, e := s.room(ctx, q, raw)
	if e != nil {
		return room, e
	}
	if !allowJoin && !isMember(room.Members, user) {
		return room, denied()
	}
	identity, _ := ctx.Value(commandKey{}).(*commandIdentity)
	if identity != nil {
		events, found, err := readCommandReceipt(ctx, tx, u, user, identity)
		if err != nil {
			return room, err
		}
		if found {
			if receipt, _ := ctx.Value(receiptKey{}).(*commandReceipt); receipt != nil {
				receipt.Events = events
			}
			return room, nil
		}
	}
	if kind, _ := ctx.Value(operationKey{}).(string); kind != "" && s.Hub != nil {
		name := "Игрок"
		for _, m := range room.Members {
			if m.UserID == user {
				name = m.Name
			}
		}
		if hero := room.State.Hero(user); hero != nil {
			name = hero.Name
		}
		end := s.Hub.beginActivity(roomID, user, name, kind)
		defer func() { _ = tx.Rollback(context.Background()); end() }()
	}
	before, e := q.ListEvents(ctx, u)
	if e != nil {
		return room, e
	}
	previousTurn := room.State.Turn
	previousStatus := room.Status
	if e = fn(q, &room); e != nil {
		return room, e
	}
	// The next player gets a full minute after narration finishes, not while
	// the model is still describing the preceding player's action.
	if room.State.Combat && room.State.Turn != previousTurn {
		room.State.CombatTurnSince = time.Now()
	}
	if e = s.persist(ctx, q, room); e != nil {
		return room, e
	}
	after, e := q.ListEvents(ctx, u)
	if e != nil {
		return room, e
	}
	known := make(map[string]bool, len(before))
	for _, v := range before {
		known[key(v.ID)] = true
	}
	fresh := []Event{}
	for _, v := range after {
		if !known[key(v.ID)] {
			fresh = append(fresh, Event{ID: key(v.ID), Type: v.Type, Payload: v.Payload})
		}
	}
	receipt, _ := ctx.Value(receiptKey{}).(*commandReceipt)
	exclude := int64(0)
	if receipt != nil {
		exclude = receipt.Author
	}
	if len(fresh) > 0 {
		message := room.State.Settings.Name + " · " + room.Code + "\n\n" + renderTextEvents(fresh)
		if hero := room.State.CombatHero(); hero != nil {
			message += "\n\nСейчас ходит: " + hero.Name
		}
		if room.Status == "FINISHED" && previousStatus != "FINISHED" {
			message += "\n\n" + describeEnding(room)
		}
		_, e = tx.Exec(ctx, `INSERT INTO telegram_outbox(room_id,user_id,body)
 SELECT s.room_id,s.user_id,$3 FROM bot_sessions s
 JOIN room_members m ON m.room_id=s.room_id AND m.user_id=s.user_id
 WHERE s.room_id=$1 AND s.user_id<>$2 AND s.notifications`, u, exclude, message)
		if e != nil {
			return room, e
		}
	}
	if identity != nil {
		_, e = tx.Exec(ctx, "INSERT INTO command_receipts(room_id,user_id,command_id,request_hash,events) VALUES($1,$2,$3,$4,$5)", u, user, identity.ID, identity.Hash, blob(fresh))
		if e != nil {
			return room, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return room, e
	}
	if receipt != nil {
		receipt.Events = fresh
	}
	return room, nil
}
func (s *Server) persist(ctx context.Context, q *store.Queries, r Room) error {
	rid, _ := id(r.ID)
	if e := q.SaveRoom(ctx, store.SaveRoomParams{ID: rid, Name: r.State.Settings.Name, Status: r.Status, State: blob(r.State)}); e != nil {
		return e
	}
	for _, h := range r.State.Characters {
		hid, _ := id(h.ID)
		if e := q.SaveCharacter(ctx, store.SaveCharacterParams{ID: hid, RoomID: rid, UserID: h.UserID, Data: blob(h)}); e != nil {
			return e
		}
		if e := q.ClearInventory(ctx, hid); e != nil {
			return e
		}
		for _, it := range h.Inventory {
			iid, _ := id(it.ID)
			if e := q.SaveItem(ctx, store.SaveItemParams{ID: iid, CharacterID: hid, Data: blob(it)}); e != nil {
				return e
			}
		}
	}
	for _, n := range r.State.NPCs {
		nid, _ := id(n.ID)
		if e := q.SaveNPC(ctx, store.SaveNPCParams{ID: nid, RoomID: rid, Data: blob(n)}); e != nil {
			return e
		}
	}
	for _, v := range r.State.Quests {
		vid, _ := id(v.ID)
		if e := q.SaveQuest(ctx, store.SaveQuestParams{ID: vid, RoomID: rid, Data: blob(v)}); e != nil {
			return e
		}
	}
	if v := r.State.Scene; v != nil {
		vid, _ := id(v.ID)
		if e := q.SaveScene(ctx, store.SaveSceneParams{ID: vid, RoomID: rid, Data: blob(v)}); e != nil {
			return e
		}
	}
	return q.SaveSummary(ctx, store.SaveSummaryParams{RoomID: rid, Summary: r.State.Summary})
}
func (s *Server) addEvent(ctx context.Context, q *store.Queries, roomID string, user int64, kind string, data any) error {
	rid, _ := id(roomID)
	eid, _ := id(uuid.NewString())
	return q.AddEvent(ctx, store.AddEventParams{ID: eid, RoomID: rid, ActorID: pgtype.Int8{Int64: user, Valid: user > 0}, Type: kind, Payload: blob(data)})
}
func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	q := store.New(s.Pool)
	rows, e := q.ListRooms(r.Context(), uid(r))
	if e != nil {
		fail(w, e)
		return
	}
	out := []Room{}
	for _, row := range rows {
		room, e := s.room(r.Context(), q, row)
		if e != nil {
			fail(w, e)
			return
		}
		out = append(out, room)
	}
	respond(w, 200, out)
}
func validSettings(c *game.Settings, model string) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.OllamaModel == "" {
		c.OllamaModel = model
	}
	if c.MaxPlayers == 0 {
		c.MaxPlayers = 6
	}
	if c.Name == "" || utf8.RuneCountInString(c.Name) > 80 || c.MaxPlayers < 1 || c.MaxPlayers > 8 {
		return bad("Название: 1–80 символов, игроков: 1–8")
	}
	if len(c.WorldDescription) > 4000 || len(c.Rules) > 2000 || len(c.Setting) > 200 || len(c.Tone) > 200 || len(c.GMStyle) > 500 || len(c.Difficulty) > 100 || len(c.OllamaModel) > 100 {
		return bad("Слишком длинные настройки")
	}
	return nil
}
func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var settings game.Settings
	if err := decode(w, r, &settings); err != nil {
		fail(w, err)
		return
	}
	room, err := s.createCampaign(r.Context(), uid(r), settings, nil)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 201, room)
}

// The optional hero and text-chat selection are created in the same transaction.
func (s *Server) createCampaign(ctx context.Context, user int64, settings game.Settings, hero *game.Character) (Room, error) {
	if err := validSettings(&settings, s.Config.Model); err != nil {
		return Room{}, err
	}
	rid, _ := id(uuid.NewString())
	code := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:10]
	state := game.NewState(settings)
	if hero != nil {
		state.Characters = append(state.Characters, *hero)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Room{}, err
	}
	defer tx.Rollback(context.Background())
	q := store.New(tx)
	if err = q.CreateRoom(ctx, store.CreateRoomParams{ID: rid, Code: code, OwnerID: user, Name: settings.Name, State: blob(state)}); err != nil {
		return Room{}, err
	}
	if err = q.AddMember(ctx, store.AddMemberParams{RoomID: rid, UserID: user, Role: "OWNER"}); err != nil {
		return Room{}, err
	}
	if hero != nil {
		room := Room{ID: key(rid), Status: "WAITING", State: state}
		if err = s.persist(ctx, q, room); err != nil {
			return Room{}, err
		}
		if err = q.SelectBotRoom(ctx, store.SelectBotRoomParams{UserID: user, RoomID: rid}); err != nil {
			return Room{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Room{}, err
	}
	slog.Info("room created", "room", key(rid), "owner", user)
	return s.load(ctx, key(rid), user)
}
func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	out, e := s.load(r.Context(), chi.URLParam(r, "id"), uid(r))
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, out)
}
func (s *Server) joinCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	raw, e := store.New(s.Pool).FindRoom(r.Context(), strings.ToUpper(strings.TrimSpace(in.Code)))
	if e != nil {
		fail(w, e)
		return
	}
	s.join(w, r, key(raw.ID))
}
func (s *Server) joinRoom(w http.ResponseWriter, r *http.Request) {
	s.join(w, r, chi.URLParam(r, "id"))
}
func (s *Server) join(w http.ResponseWriter, r *http.Request, rid string) {
	_, e := s.mutate(r.Context(), rid, uid(r), true, func(q *store.Queries, room *Room) error {
		if isMember(room.Members, uid(r)) {
			return nil
		}
		if room.Status != "WAITING" {
			return bad("Присоединиться можно до старта")
		}
		if len(room.Members) >= room.State.Settings.MaxPlayers {
			return bad("Комната заполнена")
		}
		u, _ := id(rid)
		return q.AddMember(r.Context(), store.AddMemberParams{RoomID: u, UserID: uid(r), Role: "PLAYER"})
	})
	s.finish(w, r, rid, e, "player_joined")
}
func (s *Server) leaveRoom(w http.ResponseWriter, r *http.Request) {
	rid := chi.URLParam(r, "id")
	_, e := s.mutate(r.Context(), rid, uid(r), false, func(q *store.Queries, room *Room) error {
		if CanManage(room.OwnerID, uid(r)) {
			return bad("Владелец остаётся в комнате; можно поставить игру на паузу")
		}
		if room.Status != "WAITING" {
			return bad("Покинуть комнату можно до старта")
		}
		for i, h := range room.State.Characters {
			if h.UserID == uid(r) {
				room.State.Characters = append(room.State.Characters[:i], room.State.Characters[i+1:]...)
				break
			}
		}
		u, _ := id(rid)
		if e := q.DeleteCharacter(r.Context(), store.DeleteCharacterParams{RoomID: u, UserID: uid(r)}); e != nil {
			return e
		}
		return q.RemoveMember(r.Context(), store.RemoveMemberParams{RoomID: u, UserID: uid(r)})
	})
	if e != nil {
		fail(w, e)
		return
	}
	s.Hub.LeaveRoom(rid, uid(r))
	s.Hub.Publish(rid, "player_left")
	respond(w, 200, map[string]bool{"ok": true})
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var in game.Settings
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	if e := validSettings(&in, s.Config.Model); e != nil {
		fail(w, e)
		return
	}
	rid := chi.URLParam(r, "id")
	_, e := s.mutate(r.Context(), rid, uid(r), false, func(q *store.Queries, room *Room) error {
		if !CanManage(room.OwnerID, uid(r)) {
			return denied()
		}
		if room.Status != "WAITING" {
			return bad("Настройки кампании доступны до старта")
		}
		if in.MaxPlayers < len(room.Members) {
			return bad("В комнате уже больше игроков")
		}
		room.State.Settings = in
		return nil
	})
	s.finish(w, r, rid, e, "room_state")
}
func (s *Server) character(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Race  string `json:"race"`
		Class string `json:"class"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 120 || len(in.Race) > 80 || len(in.Class) > 80 {
		fail(w, bad("Проверь имя, расу и класс"))
		return
	}
	hero, err := game.NewClassCharacter(uid(r), in.Name, in.Race, in.Class)
	if err != nil {
		fail(w, bad(err.Error()))
		return
	}
	rid := chi.URLParam(r, "id")
	_, e := s.mutate(r.Context(), rid, uid(r), false, func(q *store.Queries, room *Room) error {
		if room.Status != "WAITING" {
			return bad("Персонаж создаётся до старта")
		}
		if room.State.Hero(uid(r)) != nil {
			return bad("Персонаж уже создан")
		}
		room.State.Characters = append(room.State.Characters, hero)
		return nil
	})
	s.finish(w, r, rid, e, "character_updated")
}
func (s *Server) myCharacter(w http.ResponseWriter, r *http.Request) {
	out, e := s.load(r.Context(), chi.URLParam(r, "id"), uid(r))
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, out.State.Hero(uid(r)))
}
func (s *Server) quests(w http.ResponseWriter, r *http.Request) {
	out, e := s.load(r.Context(), chi.URLParam(r, "id"), uid(r))
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, out.State.Quests)
}
func (s *Server) eventList(ctx context.Context, rid string) ([]Event, error) {
	u, _ := id(rid)
	rows, e := store.New(s.Pool).ListEvents(ctx, u)
	out := []Event{}
	for _, v := range rows {
		out = append(out, Event{key(v.ID), v.Type, json.RawMessage(v.Payload), v.CreatedAt.Time.Format("2006-01-02T15:04:05.999999Z07:00")})
	}
	return out, e
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	rid := chi.URLParam(r, "id")
	if _, e := s.load(r.Context(), rid, uid(r)); e != nil {
		fail(w, e)
		return
	}
	out, e := s.eventList(r.Context(), rid)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, out)
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	rid := chi.URLParam(r, "id")
	e := s.startGame(r.Context(), rid, uid(r))
	s.finish(w, r, rid, e, "game_status_changed")
}
func (s *Server) startGame(ctx context.Context, rid string, user int64) error {
	ctx = context.WithValue(ctx, operationKey{}, "start_game")
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	_, e := s.mutate(ctx, rid, user, false, func(q *store.Queries, room *Room) error {
		if !CanManage(room.OwnerID, user) {
			return denied()
		}
		if room.Status == "FINISHED" {
			return bad("Кампания завершена. Создай новое приключение; история этой кампании сохранена.")
		}
		if room.Status != "WAITING" && room.Status != "PAUSED" {
			return bad("Игра уже запущена")
		}
		if len(room.State.Characters) == 0 || room.State.PartyDefeated() {
			return bad("Для старта нужен хотя бы один живой персонаж")
		}
		if room.Status == "WAITING" {
			if err := lobbyReady(room); err != nil {
				return err
			}
			return s.openAdventure(ctx, q, room, user)
		}
		room.Status = "PLAYING"
		if room.State.Combat {
			room.State.CombatTurnSince = time.Now()
		}
		return s.addEvent(ctx, q, rid, user, "GAME_RESUMED", game.Result{Type: "GAME_RESUMED", Text: "Кампания продолжается."})
	})
	if e == nil && s.Hub != nil {
		s.Hub.Publish(rid, "game_status_changed")
	}
	return e
}
func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	rid := chi.URLParam(r, "id")
	_, e := s.mutate(r.Context(), rid, uid(r), false, func(q *store.Queries, room *Room) error {
		if !CanManage(room.OwnerID, uid(r)) {
			return denied()
		}
		if room.Status != "PLAYING" {
			return bad("Игра не запущена")
		}
		room.Status = "PAUSED"
		return s.addEvent(r.Context(), q, rid, uid(r), "GAME_PAUSED", game.Result{Type: "GAME_PAUSED", Text: "Владелец поставил кампанию на паузу."})
	})
	s.finish(w, r, rid, e, "game_status_changed")
}
func (s *Server) finish(w http.ResponseWriter, r *http.Request, rid string, e error, kind string) {
	if e != nil {
		fail(w, e)
		return
	}
	s.Hub.Publish(rid, kind)
	out, e := s.load(r.Context(), rid, uid(r))
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, 200, out)
}

func (s *Server) members(w http.ResponseWriter, r *http.Request) {
	room, err := s.load(r.Context(), chi.URLParam(r, "id"), uid(r))
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, room.Members)
}
func (s *Server) editCharacter(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  *string `json:"name"`
		Race  *string `json:"race"`
		Class *string `json:"class"`
	}
	if err := decode(w, r, &in); err != nil {
		fail(w, err)
		return
	}
	hid, err := id(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, bad("Некорректный ID персонажа"))
		return
	}
	record, err := store.New(s.Pool).GetCharacter(r.Context(), hid)
	if err != nil {
		fail(w, err)
		return
	}
	if record.UserID != uid(r) {
		fail(w, denied())
		return
	}
	rid := key(record.RoomID)
	_, err = s.mutate(r.Context(), rid, uid(r), false, func(q *store.Queries, room *Room) error {
		if room.Status != "WAITING" {
			return bad("Персонажа можно менять до старта")
		}
		hero := room.State.Hero(uid(r))
		if hero == nil || hero.ID != key(hid) {
			return bad("Персонаж не найден")
		}
		for target, value := range map[*string]*string{&hero.Name: in.Name, &hero.Race: in.Race} {
			if value != nil {
				v := strings.TrimSpace(*value)
				if v == "" || utf8.RuneCountInString(v) > 40 {
					return bad("Поля персонажа: 1–40 символов")
				}
				*target = v
			}
		}
		if in.Class != nil {
			class := strings.TrimSpace(*in.Class)
			updated, e := game.NewClassCharacter(uid(r), hero.Name, hero.Race, class)
			if e != nil {
				return bad(e.Error())
			}
			updated.ID = hero.ID
			*hero = updated
		}
		return nil
	})
	s.finish(w, r, rid, err, "character_updated")
}
