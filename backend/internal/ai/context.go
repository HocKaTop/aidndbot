package ai

import (
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var ErrContextTooLarge = errors.New("critical world state exceeds model context budget")

const DefaultContextWindow = 16384
const OutputTokens = 1024
const OpeningOutputTokens = 1280
const NarrationOutputTokens = 768

const NarrativeMaxChars = 900
const OpeningNarrativeMaxChars = 1400

func outputTokens(in Input) int {
	if in.Results != nil {
		return NarrationOutputTokens
	}
	if in.Opening {
		return OpeningOutputTokens
	}
	return OutputTokens
}

func narrativeMaxChars(in Input) int {
	if in.Opening && in.Results == nil {
		return OpeningNarrativeMaxChars
	}
	return NarrativeMaxChars
}

// UTF-8 byte length is a conservative upper bound for byte-level tokenizers.
// Reserve tokens for the answer and protocol/schema overhead. Never silently
// drop characters, inventory or current world state to make a request fit.
func prepareInput(in Input, window int) (Input, error) {
	budget := window - outputTokens(in) - 1024
	// Keep memory from crowding out all recent events. Retain its newest facts.
	in.State.Summary = tailUTF8(in.State.Summary, max(0, budget/4))
	size := func() int { data, _ := json.Marshal(in); return len(data) + len(Prompt(in)) }
	for size() > budget && len(in.Recent) > 1 {
		in.Recent = in.Recent[1:]
	}
	for size() > budget && len(in.State.Summary) > 0 {
		in.State.Summary = tailUTF8(in.State.Summary, max(0, len(in.State.Summary)-(size()-budget)))
	}
	for size() > budget && len(in.State.GMNotes) > 0 {
		in.State.GMNotes = in.State.GMNotes[1:]
	}
	if size() > budget && len(in.Recent) > 0 {
		in.Recent = nil
	}
	if size() > budget {
		return Input{}, ErrContextTooLarge
	}
	return in, nil
}

func tailUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[len(s)-limit:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}
