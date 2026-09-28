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
	"sync"
	"time"
	"unicode/utf16"
)

type Bot struct {
	Token, AppURL string
	Client        *http.Client
	Handle        func(context.Context, auth.User, string) (string, error)
}
type APIError struct{ Code int }

func (e *APIError) Error() string   { return fmt.Sprintf("telegram API error %d", e.Code) }
func (e *APIError) Permanent() bool { return e.Code == 400 || e.Code == 403 }

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
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 40 * time.Second}
	}
	res, e := client.Do(req)
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
		return &APIError{Code: envelope.ErrorCode}
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (b *Bot) Run(ctx context.Context) {
	workers := newChatWorkers(ctx, b)
	defer workers.wait.Wait()
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
			if u.ID < offset {
				continue
			}
			offset = u.ID + 1
			m := u.Message
			if m == nil || m.Chat.Type != "private" || m.From.IsBot || m.From.ID != m.Chat.ID || m.Date < started || strings.TrimSpace(m.Text) == "" {
				continue
			}
			workers.enqueue(u)
		}
	}
}

func (b *Bot) Send(ctx context.Context, user int64, text string) error {
	for _, part := range splitText(text) {
		if err := b.call(ctx, "sendMessage", b.response(user, part), nil); err != nil {
			return err
		}
	}
	return nil
}

// Each chat preserves message order; a slow room never blocks the polling loop.
type chatWorkers struct {
	mu       sync.Mutex
	wait     sync.WaitGroup
	chats    map[int64]chan update
	ctx      context.Context
	bot      *Bot
	rejected chan int64
}

func newChatWorkers(ctx context.Context, b *Bot) *chatWorkers {
	w := &chatWorkers{chats: map[int64]chan update{}, ctx: ctx, bot: b, rejected: make(chan int64, 32)}
	w.wait.Add(1)
	go func() {
		defer w.wait.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case user := <-w.rejected:
				_ = b.Send(ctx, user, "Слишком много сообщений в очереди. Дождись ответа и повтори действие.")
			}
		}
	}()
	return w
}

func (w *chatWorkers) enqueue(u update) {
	w.mu.Lock()
	defer w.mu.Unlock()
	id := u.Message.From.ID
	queue := w.chats[id]
	if queue == nil && len(w.chats) < 128 {
		queue = make(chan update, 16)
		w.chats[id] = queue
		w.wait.Add(1)
		go w.run(id, queue)
	}
	select {
	case queue <- u:
	default:
		select {
		case w.rejected <- id:
		default:
		}
	}
}

func (w *chatWorkers) run(id int64, queue chan update) {
	defer w.wait.Done()
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-timer.C:
			w.mu.Lock()
			if len(queue) == 0 {
				delete(w.chats, id)
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			timer.Reset(time.Minute)
		case u := <-queue:
			operation, cancel := context.WithTimeout(w.ctx, 150*time.Second)
			_ = w.bot.call(operation, "sendChatAction", map[string]any{"chat_id": id, "action": "typing"}, nil)
			text := "Напиши /help, чтобы увидеть команды."
			if w.bot.Handle != nil {
				m := u.Message
				var err error
				text, err = w.bot.Handle(operation, auth.User{ID: id, FirstName: m.From.FirstName, Username: m.From.Username}, m.Text)
				if err != nil {
					slog.Warn("telegram command failed")
					text = "Не удалось выполнить действие. Проверь /state перед повтором."
				}
			}
			cancel()
			if err := w.bot.Send(w.ctx, id, text); err != nil {
				slog.Warn("telegram reply failed", "error", err)
			}
			timer.Reset(time.Minute)
		}
	}
}
func (b *Bot) response(chatID int64, text string) reply {
	out := reply{ChatID: chatID, Text: text}
	u, err := url.Parse(b.AppURL)
	if err == nil && u.Scheme == "https" && u.Host != "" {
		out.ReplyMarkup = &keyboard{Rows: [][]button{{{Text: "Открыть мини-приложение", WebApp: webapp{URL: b.AppURL}}}}}
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
