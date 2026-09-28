package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
)

var (
	ErrUnavailable      = errors.New("ollama unavailable")
	ErrTimeout          = errors.New("ollama timeout")
	ErrModelUnavailable = errors.New("ollama model or endpoint not found")
	ErrBusy             = errors.New("ollama busy")
	ErrRejected         = errors.New("ollama rejected request")
	ErrInvalidResponse  = errors.New("invalid model response")
)

func requestError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func invalidResponse(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidResponse, reason)
}
