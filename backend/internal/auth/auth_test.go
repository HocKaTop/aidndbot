package auth

import (
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTelegramValidation(t *testing.T) {
	now := time.Unix(1800000000, 0)
	sign := func(date int64) string {
		v := url.Values{"auth_date": {strconv.FormatInt(date, 10)}, "user": {`{"id":42,"first_name":"Anna"}`}, "query_id": {"hello"}}
		var lines []string
		for k := range v {
			lines = append(lines, k+"="+v.Get(k))
		}
		sort.Strings(lines)
		v.Set("hash", hex.EncodeToString(mac(mac([]byte("WebAppData"), "test-token"), strings.Join(lines, "\n"))))
		return v.Encode()
	}
	valid := sign(now.Unix())
	u, e := Validate(valid, "test-token", now)
	if e != nil || u.ID != 42 {
		t.Fatal(u, e)
	}
	for _, raw := range []string{strings.Replace(valid, "Anna", "Evil", 1), sign(now.Add(-2 * time.Hour).Unix()), sign(now.Add(time.Minute).Unix()), valid + "&user=x", "%zz"} {
		if _, e := Validate(raw, "test-token", now); e == nil {
			t.Fatal("accepted invalid data")
		}
	}
	if _, e := Validate(valid, "wrong-token", now); e == nil {
		t.Fatal("wrong key accepted")
	}
}
func TestSession(t *testing.T) {
	now := time.Now()
	token := Issue(User{ID: 42}, "secret", now)
	if _, e := Verify(token, "secret", now); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		token, key string
		now        time.Time
	}{{token, "wrong", now}, {token + "x", "secret", now}, {token, "secret", now.Add(13 * time.Hour)}} {
		if _, e := Verify(tc.token, tc.key, tc.now); e == nil {
			t.Fatal("invalid session accepted")
		}
	}
}
