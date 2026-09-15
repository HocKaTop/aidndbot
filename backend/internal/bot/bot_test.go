package bot

import (
	"context"
	"dnd-bot/backend/internal/auth"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func TestMessageParts(t *testing.T) {
	text := strings.Repeat("🧙 Привет! ", 1800)
	parts := splitText(text)
	if strings.Join(parts, "") != text {
		t.Fatal("text corrupted")
	}
	for _, part := range parts {
		if !utf8.ValidString(part) || len(utf16.Encode([]rune(part))) > 3500 {
			t.Fatal("invalid Telegram message")
		}
	}
}
func TestTextModeWithoutHTTPS(t *testing.T) {
	for _, url := range []string{"", "http://localhost:8080"} {
		b := Bot{AppURL: url}
		if b.response(1, "hello").ReplyMarkup != nil {
			t.Fatal("HTTP Mini App button included")
		}
	}
	b := Bot{AppURL: "https://game.example.com"}
	if b.response(1, "hello").ReplyMarkup == nil {
		t.Fatal("HTTPS button missing")
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPollingTextReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	handled, sent := 0, 0
	b := Bot{Token: "test-token", AppURL: "http://localhost:8080", Handle: func(ctx context.Context, u auth.User, text string) (string, error) {
		handled++
		if u.ID != 42 || text != "/start" {
			t.Fatal("wrong Telegram identity")
		}
		return "Можно играть в чате", nil
	}}
	b.Client = &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		var result any = true
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			result = map[string]string{"username": "test_bot"}
		case strings.HasSuffix(r.URL.Path, "getUpdates"):
			result = []any{map[string]any{"update_id": 1, "message": map[string]any{"text": "/start", "date": time.Now().Unix(), "from": map[string]any{"id": 42, "first_name": "Олег"}, "chat": map[string]any{"id": 42, "type": "private"}}}}
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			var message reply
			if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
				t.Fatal(err)
			}
			if message.ChatID != 42 || message.ReplyMarkup != nil || message.Text != "Можно играть в чате" {
				t.Fatal("invalid text reply", message)
			}
			sent++
			cancel()
		}
		body, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	b.Run(ctx)
	if handled != 1 || sent != 1 {
		t.Fatalf("handled=%d sent=%d", handled, sent)
	}
}

func TestChatsRunIndependentlyAndKeepMessageOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, otherDone, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var seen []string
	b := &Bot{Handle: func(ctx context.Context, u auth.User, text string) (string, error) {
		if u.ID == 1 && text == "first" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		mu.Lock()
		seen = append(seen, text)
		mu.Unlock()
		if u.ID == 2 {
			close(otherDone)
		}
		return text, nil
	}}
	b.Client = &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}
	w := newChatWorkers(ctx, b)
	enqueue := func(id int64, text string) {
		var u update
		data, _ := json.Marshal(map[string]any{"message": map[string]any{"from": map[string]any{"id": id}, "text": text}})
		if err := json.Unmarshal(data, &u); err != nil {
			t.Fatal(err)
		}
		w.enqueue(u)
	}
	enqueue(1, "first")
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first message did not start")
	}
	enqueue(1, "second")
	enqueue(2, "other room")
	select {
	case <-otherDone:
	case <-ctx.Done():
		t.Fatal("slow chat blocked another chat")
	}
	close(release)
	for {
		mu.Lock()
		done := len(seen) == 3
		mu.Unlock()
		if done {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("queued message lost")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	w.wait.Wait()
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, ",") != "other room,first,second" {
		t.Fatal("per-chat order broken", seen)
	}
}
