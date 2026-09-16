package server

import (
	"context"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Finishing freezes the saved world. It does not award victory or resolve quests.
func endCampaign(r *Room, reason, note string) game.Result {
	r.Status = "FINISHED"
	r.State.Ending = &game.Ending{Reason: reason, Note: note, FinishedAt: time.Now().UTC()}
	r.State.Combat = false
	r.State.EnsureCombatOrder(0, time.Now())
	text := note
	if reason == "owner" {
		text = "Кампания завершена владельцем.\n" + note
	}
	return game.Result{Type: "GAME_FINISHED", Text: text}
}

func (s *Server) finishCampaign(ctx context.Context, q *store.Queries, r *Room, user int64, code, note string) error {
	if !CanManage(r.OwnerID, user) {
		return denied()
	}
	if strings.ToUpper(strings.TrimSpace(code)) != r.Code {
		return bad("Для завершения введи код кампании.")
	}
	if r.Status != "PLAYING" && r.Status != "PAUSED" {
		return bad("Завершить можно только начатую кампанию. История уже завершённой кампании доступна для чтения.")
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 1000 {
		return bad("Итог должен содержать не больше 1000 символов.")
	}
	if note == "" {
		note = "Владелец завершил приключение. История кампании сохранена."
	}
	result := endCampaign(r, "owner", note)
	// A lifecycle change is not an additional game turn.
	return s.addEvent(ctx, q, r.ID, user, result.Type, result)
}

func describeEnding(r Room) string {
	note := "Кампания завершена."
	if r.State.Ending != nil {
		note = r.State.Ending.Note
	} else if r.State.PartyDefeated() {
		note = "Весь отряд пал."
	}
	alive, completed, failed, active := 0, 0, 0, 0
	for _, h := range r.State.Characters {
		if h.HP > 0 {
			alive++
		}
	}
	for _, q := range r.State.Quests {
		switch q.Status {
		case "COMPLETED":
			completed++
		case "FAILED":
			failed++
		case "ACTIVE":
			active++
		}
	}
	return fmt.Sprintf("Итоги приключения\n%s\nХодов: %d. Выжило героев: %d/%d.\nКвесты: выполнено %d, провалено %d, незавершено %d.\n/history — последние события; /create Название — новое приключение.", note, r.State.Turn, alive, len(r.State.Characters), completed, failed, active)
}
