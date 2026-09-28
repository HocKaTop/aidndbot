package server

import (
	"dnd-bot/backend/internal/auth"
	"dnd-bot/backend/internal/game"
	"dnd-bot/backend/internal/store"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestHistoryPagesRemainStableAcrossNewTurns(t *testing.T) {
	ctx, s, httpServer := reviewServer(t)
	r := testCampaign(t, ctx, s, 1)
	other := testCampaign(t, ctx, s, 2)
	if _, err := s.Pool.Exec(ctx, "SELECT setval('game_event_sequence',9007199254740993,false)"); err != nil {
		t.Fatal(err)
	}
	// Same transaction timestamp: pagination must use sequence, never created_at.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	for i := 0; i < 205; i++ {
		if err = s.addEvent(ctx, q, r.ID, 1, "TEST", game.Result{Text: fmt.Sprintf("event %d", i)}); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 {
			if err = s.addEvent(ctx, q, other.ID, 2, "PRIVATE", game.Result{Text: "other room"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	request := func(user int64, suffix string, want int) historyPage {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", httpServer.URL+"/api/rooms/"+r.ID+"/events/page"+suffix, nil)
		if user > 0 {
			req.Header.Set("Authorization", "Bearer "+auth.Issue(auth.User{ID: user}, s.Config.SessionSecret, time.Now()))
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s: got %d want %d", suffix, res.StatusCode, want)
		}
		var page historyPage
		if want == 200 {
			if err = json.NewDecoder(res.Body).Decode(&page); err != nil {
				t.Fatal(err)
			}
		}
		return page
	}
	request(0, "", 401)
	request(2, "", 403)
	for _, suffix := range []string{"?before=", "?before=0", "?after=-1", "?after=abc", "?after=9223372036854775808", "?before=1&after=2", "?limit=0", "?limit=101", "?limit=bad"} {
		request(1, suffix, 400)
	}
	first := request(1, "", 200)
	if len(first.Events) != 50 || first.NextCursor == nil {
		t.Fatal("wrong initial page", first)
	}
	last := first.Events[len(first.Events)-1].Sequence
	for i := 0; i < 3; i++ {
		if err = s.addEvent(ctx, store.New(s.Pool), r.ID, 1, "NEW", game.Result{Text: "new turn"}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	page := first
	for {
		for i, event := range page.Events {
			seq, err := strconv.ParseInt(event.Sequence, 10, 64)
			if err != nil || seq < 9007199254740993 || event.Type != "TEST" || seen[event.ID] {
				t.Fatal("wrong event, rounded sequence or duplicate", event)
			}
			if i > 0 {
				prev, _ := strconv.ParseInt(page.Events[i-1].Sequence, 10, 64)
				if prev >= seq {
					t.Fatal("events out of order")
				}
			}
			seen[event.ID] = true
		}
		if page.NextCursor == nil {
			break
		}
		if *page.NextCursor != page.Events[0].Sequence {
			t.Fatal("wrong earlier cursor")
		}
		page = request(1, "?before="+*page.NextCursor+"&limit=17", 200)
	}
	if len(seen) != 205 {
		t.Fatal("history has gaps", len(seen))
	}
	newPage := request(1, "?after="+last+"&limit=2", 200)
	if len(newPage.Events) != 2 || newPage.NextCursor == nil || newPage.Events[0].Type != "NEW" {
		t.Fatal("new events missing", newPage)
	}
	end := request(1, "?after="+*newPage.NextCursor+"&limit=2", 200)
	if len(end.Events) != 1 || end.NextCursor != nil {
		t.Fatal("wrong final page", end)
	}
	empty := request(1, "?after="+end.Events[0].Sequence, 200)
	if empty.Events == nil || len(empty.Events) != 0 || empty.NextCursor != nil {
		t.Fatal("empty page contract", empty)
	}
	legacy, err := s.eventList(ctx, r.ID)
	if err != nil || len(legacy) != 100 {
		t.Fatal("legacy recent events changed", err)
	}
}
