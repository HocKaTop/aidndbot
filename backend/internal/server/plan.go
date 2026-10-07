package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Repair at most once, before any dice or changes. The command's existing context
// bounds both attempts and narration; there is no retry of an executed turn.
func (s *Server) planTurn(ctx context.Context, rid string, input ai.Input) (ai.Output, error) {
	if !input.Opening {
		intent, err := turnIntent(input.State, input.PlayerID, input.Text)
		if err != nil {
			return ai.Output{}, err
		}
		input.Intent = &intent
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return ai.Output{}, aiFailure(err)
		}
		started := time.Now()
		out, err := s.AI.GenerateTurn(ctx, input.State.Settings.OllamaModel, input)
		slog.Info("AI request", "room", rid, "attempt", attempt+1, "duration", time.Since(started), "success", err == nil)
		if err != nil {
			slog.Warn("AI failed", "room", rid, "error", err)
			if errors.Is(err, ai.ErrInvalidResponse) && attempt == 0 {
				input.ResponseCorrection = err.Error()
				continue
			}
			return ai.Output{}, aiFailure(err)
		}
		err = game.ValidateActions(&input.State, input.PlayerID, out.Actions)
		if err == nil && !input.Opening {
			err = validateAttackPlan(&input.State, input.Text, out.Actions)
		}
		if err == nil && !input.Opening {
			err = validateInventoryPlan(input.Intent, out.Actions)
		}
		if err == nil {
			err = validatePhysicalNarrative(input, out.Actions, out.Narrative)
		}
		if err == nil && input.Opening {
			err = validateOpening(out.Actions)
		}
		if err == nil && !input.Opening {
			for _, action := range out.Actions {
				if (action.Type == "MOVE_SCENE" || action.Type == "REVISIT_SCENE") && !input.Intent.SceneChange {
					err = errors.New("игрок не просил перейти в другую локацию; оставайся в текущей сцене")
					break
				}
			}
		}
		if err == nil && strings.TrimSpace(out.Narrative) == "" {
			err = errors.New("нужно непустое повествование")
		}
		if err == nil && (input.Opening || len(out.Actions) == 0) {
			err = validateStoryNarrative(input.State, input.PlayerID, out.Actions, nil, out.Narrative)
		}
		if err == nil {
			return out, nil
		}
		kinds := make([]string, len(out.Actions))
		for i, action := range out.Actions {
			kinds[i] = action.Type
		}
		slog.Warn("AI plan rejected", "room", rid, "attempt", attempt+1, "actions", kinds, "reason", err)
		if attempt == 1 {
			return ai.Output{}, bad("Мастер не смог согласовать действие с правилами даже после исправления: " + err.Error() + ". Ход не сохранён.")
		}
		input.Correction = &ai.PlanCorrection{Actions: out.Actions, Reason: err.Error()}
	}
	return ai.Output{}, bad("Не удалось подготовить ход")
}

func explicitTravel(text string) bool {
	return playerIntent(text).SceneChange
}

// A narration repair sees the exact same engine results. It must never replan
// the action or run the engine again, even when the first response is malformed.
func (s *Server) narrateTurn(ctx context.Context, rid string, input ai.Input) (ai.Output, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return ai.Output{}, aiFailure(err)
		}
		started := time.Now()
		out, err := s.AI.GenerateTurn(ctx, input.State.Settings.OllamaModel, input)
		if err == nil && (strings.TrimSpace(out.Narrative) == "" || len(out.Actions) != 0) {
			err = fmt.Errorf("%w: нужно непустое описание engineResults без новых actions", ai.ErrInvalidResponse)
		}
		if err == nil {
			if issue := validateStoryNarrative(input.State, input.PlayerID, nil, input.Results, out.Narrative); issue != nil {
				err = fmt.Errorf("%w: %v", ai.ErrInvalidResponse, issue)
			}
		}
		if err == nil {
			if issue := validatePhysicalNarrative(input, nil, out.Narrative); issue != nil {
				err = fmt.Errorf("%w: %v", ai.ErrInvalidResponse, issue)
			}
		}
		slog.Info("AI narration", "room", rid, "attempt", attempt+1, "duration", time.Since(started), "success", err == nil)
		if err == nil {
			return out, nil
		}
		slog.Warn("AI narration failed", "room", rid, "error", err)
		if !errors.Is(err, ai.ErrInvalidResponse) || attempt == 1 {
			return ai.Output{}, aiFailure(err)
		}
		input.ResponseCorrection = err.Error()
	}
	return ai.Output{}, bad("Не удалось завершить повествование. Ход не сохранён.")
}

var (
	questCompletionClaim    = regexp.MustCompile(`(?i)(квест|задани[ея]|цель|мисси[яию]|задач[а-я]*)[^.!?\n]{0,70}(заверш[её]н[аоы]?|выполнен[аоы]?|окончен[аоы]?)`)
	questFailureClaim       = regexp.MustCompile(`(?i)(квест|задани[ея]|цель|мисси[яию]|задач[а-я]*)[^.!?\n]{0,70}(провален[аоы]?|потерян[аоы]?)`)
	campaignCompletionClaim = regexp.MustCompile(`(?i)(приключени[ея]|кампани[яию])[^.!?\n]{0,60}(заверш[её]н[аоы]?|выполнен[аоы]?|окончен[аоы]?)`)
	lanternStoneClaim       = regexp.MustCompile(`(?i)(вы|ты)(?:\s+[\p{L}-]+){0,3}\s+(принимаете|принимаешь|забираете|забираешь|берёте|берете|берёшь|берешь|получаете|получаешь|подбираете|подбираешь)[^.!?\n]{0,45}(камень|кристалл|его дар)`)
)

func claimsCompletion(pattern *regexp.Regexp, narrative string) bool {
	for _, bounds := range pattern.FindAllStringIndex(narrative, -1) {
		claim := strings.ToLower(narrative[bounds[0]:bounds[1]])
		if strings.Contains(claim, "не заверш") || strings.Contains(claim, "не выполн") || strings.Contains(claim, "не окончен") || strings.Contains(claim, "не провал") || strings.Contains(claim, "не потерян") {
			continue
		}
		if strings.Contains(claim, "будет ") || strings.Contains(claim, "может быть ") || strings.Contains(claim, "должн") || strings.Contains(claim, "нужн") {
			continue
		}
		start := strings.LastIndexAny(narrative[:bounds[0]], ".!?\n;") + 1
		context := strings.ToLower(narrative[start:bounds[0]])
		if strings.Contains(context, "если ") || strings.Contains(context, "когда ") || strings.Contains(context, "как только ") || strings.Contains(context, "чтобы ") || strings.Contains(context, "после того как ") {
			continue
		}
		return true
	}
	return false
}

// Story outcomes in prose must match engine transitions for every setting.
func validateStoryNarrative(state game.State, user int64, actions []game.Action, results []game.Result, narrative string) error {
	completedQuest := false
	failedQuest := false
	activeQuest := false
	for _, quest := range state.Quests {
		completedQuest = completedQuest || quest.Status == "COMPLETED"
		failedQuest = failedQuest || quest.Status == "FAILED"
		activeQuest = activeQuest || quest.Status == "ACTIVE"
	}
	finishAction := false
	completedNow := false
	failedNow := false
	for _, action := range actions {
		completedNow = completedNow || action.Type == "UPDATE_QUEST" && action.Status == "COMPLETED"
		failedNow = failedNow || action.Type == "UPDATE_QUEST" && action.Status == "FAILED"
		finishAction = finishAction || action.Type == "FINISH_CAMPAIGN"
	}
	for _, result := range results {
		completedNow = completedNow || result.Type == "UPDATE_QUEST" && result.Status == "COMPLETED"
		failedNow = failedNow || result.Type == "UPDATE_QUEST" && result.Status == "FAILED"
	}
	if claimsCompletion(questCompletionClaim, narrative) && ((!completedQuest && !completedNow) || (activeQuest && !completedNow)) {
		return errors.New("нельзя объявлять цель выполненной без подтверждённого результата; в пользовательской кампании предложи PROPOSE_QUEST_COMPLETION и опиши итог как предварительный")
	}
	if claimsCompletion(questFailureClaim, narrative) && ((!failedQuest && !failedNow) || (activeQuest && !failedNow)) {
		return errors.New("нельзя объявлять цель проваленной без подтверждённого исхода")
	}
	if claimsCompletion(campaignCompletionClaim, narrative) && state.Ending == nil && !finishAction {
		lanternFinish := state.Settings.LastLantern() && len(state.Quests) > 0
		if lanternFinish {
			lanternFinish = false
			for _, action := range actions {
				lanternFinish = lanternFinish || action.Type == "UPDATE_QUEST" && action.Target == state.Quests[0].ID && action.Status == "COMPLETED"
			}
		}
		if !lanternFinish {
			return errors.New("нельзя объявлять кампанию завершённой без FINISH_CAMPAIGN")
		}
	}
	if err := validateWorldClaims(state, user, actions, results, narrative); err != nil {
		return err
	}
	if !state.Settings.LastLantern() {
		return nil
	}
	stoneClaim := false
	for _, claim := range lanternStoneClaim.FindAllString(narrative, -1) {
		if !strings.Contains(strings.ToLower(claim), " не ") {
			stoneClaim = true
			break
		}
	}
	if stoneClaim {
		stone := false
		if hero := state.Hero(user); hero != nil {
			for _, item := range hero.Inventory {
				stone = stone || item.Quantity > 0 && strings.EqualFold(strings.TrimSpace(item.Name), "Огненный камень")
			}
		}
		for _, action := range actions {
			stone = stone || action.Type == "ADD_ITEM" && strings.EqualFold(strings.TrimSpace(action.Name), "Огненный камень")
		}
		if !stone {
			return errors.New("нельзя описывать получение огненного камня без ADD_ITEM")
		}
	}
	return nil
}

func aiFailure(err error) error {
	message := "Мастер не смог ответить. Попробуй отправить действие ещё раз."
	switch {
	case errors.Is(err, context.Canceled):
		message = "Запрос к мастеру отменён. После подключения можно повторить отправку."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ai.ErrTimeout):
		message = "Мастер не успел ответить вовремя. Попробуй ещё раз; если это повторяется, владельцу стоит проверить нагрузку на сервер мастера."
	case errors.Is(err, ai.ErrContextTooLarge):
		message = "История и состояние мира не помещаются в контекст модели. Владельцу нужно увеличить OLLAMA_CONTEXT_LENGTH на сервере."
	case errors.Is(err, ai.ErrModelUnavailable):
		message = "Выбранная модель не найдена. Владельцу нужно проверить, что она загружена в Ollama и адрес сервера указан верно."
	case errors.Is(err, ai.ErrBusy):
		message = "Сервер мастера перегружен. Подожди немного и повтори отправку."
	case errors.Is(err, ai.ErrUnavailable):
		message = "Нет связи с сервером мастера или он завершил запрос с ошибкой. Владельцу нужно проверить Ollama; затем можно повторить отправку."
	case errors.Is(err, ai.ErrRejected):
		message = "Сервер мастера отклонил запрос. Владельцу нужно проверить доступ и совместимость модели с форматом игры."
	case errors.Is(err, ai.ErrInvalidResponse):
		message = "Мастер не смог исправить формат ответа. Попробуй уточнить действие и отправить его снова."
	}
	return bad(message + " Ход не сохранён.")
}
