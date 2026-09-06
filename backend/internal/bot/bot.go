package bot

import (
	"bytes"
	"context"
	"dnd-bot/backend/internal/auth"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"
)

type Bot struct {
	Token, AppURL string
	Client        *http.Client
	Handle        func(context.Context, auth.User, string) (string, error)
}
type reply struct {
	ChatID      int64     `json:"chat_id"`
	Text        string    `json:"text"`
	ReplyMarkup *keyboard `json:"reply_markup,omitempty"`
}
type keyboard struct {
	Rows [][]button `json:"inline_keyboard"`
}
type button struct {
	Text   string `json:"text"`
	WebApp webapp `json:"web_app"`
}
type webapp struct {
	URL string `json:"url"`
}
type update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		Date int64  `json:"date"`
		From struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
			IsBot     bool   `json:"is_bot"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

func (b *Bot) call(ctx context.Context, method string, data any, out any) error {
	body, _ := json.Marshal(data)
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+b.Token+"/"+method, bytes.NewReader(body))
	if e != nil {
		return fmt.Errorf("telegram request creation failed")
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := b.Client.Do(req)
	if e != nil {
		return fmt.Errorf("telegram %s network failure", method)
	}
	defer res.Body.Close()
	var envelope struct {
		OK        bool            `json:"ok"`
		Result    json.RawMessage `json:"result"`
		ErrorCode int             `json:"error_code"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&envelope); e != nil {
		return fmt.Errorf("telegram %s invalid response", method)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s error %d", method, envelope.ErrorCode)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (b *Bot) Run(ctx context.Context) {
	if b.Client == nil {
		b.Client = &http.Client{Timeout: 40 * time.Second}
	}
	// Offline messages are not replayed as game actions after a restart.
	started := time.Now().Unix()
	var offset int64
	var me struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, "getMe", struct{}{}, &me); err != nil {
		slog.Warn("telegram identity check failed", "error", err)
	} else {
		slog.Info("telegram bot connected", "username", me.Username)
	}
	slog.Info("telegram polling started")
	for ctx.Err() == nil {
		var updates []update
		err := b.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &updates)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("telegram polling failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.ID + 1
			m := u.Message
			if m == nil || m.Chat.Type != "private" || m.From.IsBot || m.From.ID != m.Chat.ID || m.Date < started || strings.TrimSpace(m.Text) == "" {
				continue
			}
			text := "Напиши /help, чтобы увидеть команды."
			if b.Handle != nil {
				operation, cancel := context.WithTimeout(ctx, 150*time.Second)
				_ = b.call(operation, "sendChatAction", map[string]any{"chat_id": m.Chat.ID, "action": "typing"}, nil)
				var err error
				text, err = b.Handle(operation, auth.User{ID: m.From.ID, FirstName: m.From.FirstName, Username: m.From.Username}, m.Text)
				cancel()
				if err != nil {
					slog.Warn("telegram command failed")
					text = "Не удалось выполнить действие. Проверь /state перед повтором."
				}
			}
			for _, part := range splitText(text) {
				if err = b.call(ctx, "sendMessage", b.response(m.Chat.ID, part), nil); err != nil {
					slog.Warn("telegram reply failed", "error", err)
					break
				}
			}
		}
	}
}
func (b *Bot) response(chatID int64, text string) reply {
	out := reply{ChatID: chatID, Text: text}
	u, err := url.Parse(b.AppURL)
	if err == nil && u.Scheme == "https" && u.Host != "" {
		out.ReplyMarkup = &keyboard{Rows: [][]button{{{Text: "Открыть Nocturna", WebApp: webapp{URL: b.AppURL}}}}}
	}
	return out
}

// Telegram counts message length in UTF-16 units. Preserve complete Unicode runes.
func splitText(text string) []string {
	var out []string
	start, units := 0, 0
	for i, r := range text {
		n := utf16.RuneLen(r)
		if units+n > 3500 {
			out = append(out, text[start:i])
			start = i
			units = 0
		}
		units += n
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}
