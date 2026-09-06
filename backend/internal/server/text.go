package server

import (
	"context"
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"
)

const textHelp = `Можно играть прямо здесь, без Mini App.

/create Название — новая кампания и готовый герой
/join КОД — войти в кампанию друзей до старта
/play — начать или продолжить игру (владелец)
/state — сцена, отряд, HP и инвентарь
/history — последние события
/roll d20 — бросить кубик
/use 1 — использовать предмет по номеру из /state
/pause — поставить игру на паузу
/delete — удалить выбранную комнату (владелец, с подтверждением)
/rooms — мои кампании
/room КОД — переключиться на свою кампанию

После /play пиши действия обычными сообщениями, например: «Я вхожу в заброшенную таверну». Ответ ведущего может занять около полуминуты. Другие игроки могут посмотреть твой ход через /history.`

// TextMessage accepts identities exclusively from Telegram Bot API updates.
// It is not exposed as an HTTP endpoint and does not bypass Mini App authentication.
func (s *Server) TextMessage(ctx context.Context, user auth.User, text string) (string, error) {
	out, err := s.textMessage(ctx, user, text)
	if err == nil {
		return out, nil
	}
	var a *apiError
	if errors.As(err, &a) {
		return a.Message, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "Комната не найдена. Создай её: /create Моя кампания, или выбери существующую через /rooms и /room КОД.", nil
	}
	slog.Error("text game failed", "user", user.ID, "error", err)
	return "Не удалось выполнить действие. Попробуй ещё раз; /state покажет сохранённое состояние.", nil
}
func (s *Server) textMessage(ctx context.Context, user auth.User, text string) (string, error) {
	if user.ID <= 0 {
		return "", denied()
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return textHelp, nil
	}
	if len(text) > 2500 {
		return "Сократи сообщение до 2500 байт.", nil
	}
	q := store.New(s.Pool)
	if err := q.UpsertUser(ctx, store.UpsertUserParams{ID: user.ID, FirstName: user.FirstName, Username: user.Username}); err != nil {
		return "", err
	}
	command, arg, isCommand := parseTextCommand(text)
	switch command {
	case "/start", "/help":
		return textHelp, nil
	case "/create":
		if arg == "" {
			arg = "Ночная таверна"
		}
		hero := game.NewCharacter(user.ID, user.FirstName, "Человек", "Воин")
		room, err := s.createCampaign(ctx, user.ID, game.Settings{Name: arg, Setting: "Тёмное фэнтези", Tone: "Таинственный", WorldDescription: "После долгой дороги герои оказываются у заброшенной таверны. Начни с исследования, без внезапного боя.", Rules: "Упрощённая fantasy RPG", Difficulty: "Обычная", GMStyle: "Предлагай понятный выбор, отвечай кратко по-русски", MaxPlayers: 6}, &hero)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Кампания «%s» создана. Герой %s готов: 20 HP.\n\nКод для друзей: %s\nОни могут написать боту /join %s\n\nНапиши /play, чтобы начать. Можно играть одному.", room.State.Settings.Name, hero.Name, room.Code, room.Code), nil
	case "/rooms":
		rows, err := q.ListRooms(ctx, user.ID)
		if err != nil {
			return "", err
		}
		if len(rows) == 0 {
			return "Кампаний пока нет. Напиши /create Ночная таверна", nil
		}
		var out strings.Builder
		out.WriteString("Твои кампании:\n")
		for _, r := range rows {
			fmt.Fprintf(&out, "%s — %s\n/room %s\n\n", r.Name, r.Status, r.Code)
		}
		return out.String(), nil
	case "/join", "/room":
		raw, err := q.FindRoom(ctx, strings.ToUpper(arg))
		if err != nil {
			return "", err
		}
		rid := key(raw.ID)
		if command == "/room" {
			if _, err = s.load(ctx, rid, user.ID); err != nil {
				return "", err
			}
		} else {
			_, err = s.mutate(ctx, rid, user.ID, true, func(q *store.Queries, r *Room) error {
				if !isMember(r.Members, user.ID) {
					if r.Status != "WAITING" {
						return bad("Присоединиться можно до старта")
					}
					if len(r.Members) >= r.State.Settings.MaxPlayers {
						return bad("Комната заполнена")
					}
					if err := q.AddMember(ctx, store.AddMemberParams{RoomID: raw.ID, UserID: user.ID, Role: "PLAYER"}); err != nil {
						return err
					}
				}
				if r.State.Hero(user.ID) == nil && r.Status == "WAITING" {
					r.State.Characters = append(r.State.Characters, game.NewCharacter(user.ID, user.FirstName, "Человек", "Воин"))
				}
				return q.SelectBotRoom(ctx, store.SelectBotRoomParams{UserID: user.ID, RoomID: raw.ID})
			})
			if err != nil {
				return "", err
			}
			s.Hub.Publish(rid, "player_joined")
		}
		if err = q.SelectBotRoom(ctx, store.SelectBotRoomParams{UserID: user.ID, RoomID: raw.ID}); err != nil {
			return "", err
		}
		room, err := s.load(ctx, rid, user.ID)
		if err != nil {
			return "", err
		}
		return describeRoom(room, user.ID), nil
	}
	if isCommand {
		switch command {
		case "/play", "/pause", "/state", "/history", "/roll", "/use", "/delete":
		default:
			return "Неизвестная команда. /help — список команд.", nil
		}
	}
	selected, err := q.GetBotRoom(ctx, user.ID)
	if err != nil {
		return "", err
	}
	rid := key(selected)
	room, err := s.load(ctx, rid, user.ID)
	if err != nil {
		return "", err
	}
	switch command {
	case "/delete":
 if !CanManage(room.OwnerID,user.ID){return "",denied()}
 if arg=="" {return fmt.Sprintf("Удалить «%s» вместе со всеми персонажами и историей? Это нельзя отменить. Для подтверждения отправь:\n/delete %s",room.State.Settings.Name,room.Code),nil}
 if err=s.deleteCampaign(ctx,rid,user.ID,arg);err!=nil{return "",err}
 return "Комната удалена. /rooms — оставшиеся кампании; /create — новая.",nil
 case "/state":
		return describeRoom(room, user.ID), nil
	case "/history":
		return s.textHistory(ctx, rid)
	case "/play":
		if err = s.startGame(ctx, rid, user.ID); err != nil {
			return "", err
		}
		s.Hub.Publish(rid, "game_status_changed")
		return "Игра началась. Напиши первое действие, например: «Я открываю дверь таверны и осматриваюсь».", nil
	case "/pause":
		_, err = s.mutate(ctx, rid, user.ID, false, func(q *store.Queries, r *Room) error {
			if !CanManage(r.OwnerID, user.ID) {
				return denied()
			}
			if r.Status != "PLAYING" {
				return bad("Игра не запущена")
			}
			r.Status = "PAUSED"
			return nil
		})
		if err != nil {
			return "", err
		}
		s.Hub.Publish(rid, "game_status_changed")
		return "Игра на паузе. /play — продолжить.", nil
	}
	if room.Status != "PLAYING" {
		return "Сначала владелец должен написать /play. /state — состояние кампании.", nil
	}
	cmd := Command{Type: "player_action"}
	cmd.Data.Text = text
	switch command {
	case "/roll":
		cmd.Type = "roll_dice"
		cmd.Data.Notation = arg
		if arg == "" {
			cmd.Data.Notation = "d20"
		}
	case "/use":
		n, e := strconv.Atoi(arg)
		hero := room.State.Hero(user.ID)
		if e != nil || hero == nil || n < 1 || n > len(hero.Inventory) {
			return "Укажи номер предмета: /use 1. Номера доступны в /state.", nil
		}
		cmd.Type = "use_item"
		cmd.Data.ItemID = hero.Inventory[n-1].ID
	}
	before, err := s.eventList(ctx, rid)
	if err != nil {
		return "", err
	}
	known := make(map[string]bool, len(before))
	for _, event := range before {
		known[event.ID] = true
	}
	// Shares the engine and PostgreSQL row lock with WebSocket/REST actions.
	if err = s.command(ctx, rid, user.ID, cmd); err != nil {
		return "", err
	}
	s.Hub.Publish(rid, "room_state")
	after, err := s.eventList(ctx, rid)
	if err != nil {
		return "", err
	}
	fresh := []Event{}
	for _, event := range after {
		if !known[event.ID] {
			fresh = append(fresh, event)
		}
	}
	return renderTextEvents(fresh), nil
}
func parseTextCommand(text string) (command, arg string, isCommand bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	parts := strings.SplitN(text, " ", 2)
	command = strings.ToLower(strings.SplitN(parts[0], "@", 2)[0])
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	return command, arg, true
}
func describeRoom(r Room, user int64) string {
	var out strings.Builder
	labels := map[string]string{"WAITING": "сбор отряда", "PLAYING": "игра идёт", "PAUSED": "пауза", "FINISHED": "завершена"}
	fmt.Fprintf(&out, "%s · %s\nКод: %s\n", r.State.Settings.Name, labels[r.Status], r.Code)
	if r.State.Scene != nil {
		fmt.Fprintf(&out, "\n%s\n%s\n", r.State.Scene.Title, r.State.Scene.Description)
	}
	out.WriteString("\nОтряд:\n")
	for _, h := range r.State.Characters {
		fmt.Fprintf(&out, "%s: %d/%d HP\n", h.Name, h.HP, h.MaxHP)
	}
	if h := r.State.Hero(user); h != nil {
		out.WriteString("\nТвой инвентарь:\n")
		for i, it := range h.Inventory {
			fmt.Fprintf(&out, "%d. %s ×%d\n", i+1, it.Name, it.Quantity)
		}
	}
	if len(r.State.Quests) > 0 {
		out.WriteString("\nКвесты:\n")
		for _, v := range r.State.Quests {
			fmt.Fprintf(&out, "%s · %s\n", v.Title, v.Status)
		}
	}
	return out.String()
}
func (s *Server) textHistory(ctx context.Context, rid string) (string, error) {
	events, err := s.eventList(ctx, rid)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "Пока нет событий. Начни с /play.", nil
	}
	return renderTextEvents(events[max(0, len(events)-10):]), nil
}
func renderTextEvents(events []Event) string {
	var lines []string
	for _, e := range events {
		var payload game.Result
		if json.Unmarshal(e.Payload, &payload) == nil && payload.Text != "" {
			lines = append(lines, payload.Text)
		}
	}
	out := strings.Join(lines, "\n\n")
	if utf8.RuneCountInString(out) > 12000 {
		runes := []rune(out)
		out = "…\n" + string(runes[len(runes)-12000:])
	}
	return out
}
