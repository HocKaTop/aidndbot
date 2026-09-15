package server

import (
	"context"
	"dnd-bot/backend/internal/ai"
	"dnd-bot/backend/internal/game"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// Repair at most once, before any dice or changes. The command's existing context
// bounds both attempts and narration; there is no retry of an executed turn.
func (s *Server) planTurn(ctx context.Context, rid string, input ai.Input) (ai.Output, error) {
	for attempt := 0; attempt < 2; attempt++ {
		started := time.Now()
		out, err := s.AI.GenerateTurn(ctx, input.State.Settings.OllamaModel, input)
		slog.Info("AI request", "room", rid, "attempt", attempt+1, "duration", time.Since(started), "success", err == nil)
		if err != nil {
			slog.Warn("AI failed", "room", rid, "error", err)
			if errors.Is(err, ai.ErrContextTooLarge) {
				return ai.Output{}, bad("Состояние мира превышает бюджет контекста. Увеличь OLLAMA_CONTEXT_LENGTH; ход не сохранён.")
			}
			return ai.Output{}, bad("Ollama не ответила корректно. Проверь модель и соединение; ход не сохранён.")
		}
		err = game.ValidateActions(&input.State, input.PlayerID, out.Actions)
		if err == nil && input.Opening {
			err = validateOpening(out.Actions)
		}
		if err == nil && strings.TrimSpace(out.Narrative) == "" {
			err = errors.New("нужно непустое повествование")
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
