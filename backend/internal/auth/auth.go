package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"firstName"`
	Username  string `json:"username"`
}

var ErrInvalid = errors.New("invalid or expired authentication")

func mac(key []byte, text string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(text))
	return h.Sum(nil)
}
func Validate(raw, botToken string, now time.Time) (User, error) {
	var u User
	if len(raw) > 16384 || botToken == "" {
		return u, ErrInvalid
	}
	v, err := url.ParseQuery(raw)
	if err != nil {
		return u, ErrInvalid
	}
	var lines []string
	for k, vals := range v {
		if len(vals) != 1 {
			return u, ErrInvalid
		}
		if k != "hash" {
			lines = append(lines, k+"="+vals[0])
		}
	}
	sort.Strings(lines)
	supplied, err := hex.DecodeString(v.Get("hash"))
	if err != nil || !hmac.Equal(supplied, mac(mac([]byte("WebAppData"), botToken), strings.Join(lines, "\n"))) {
		return u, ErrInvalid
	}
	timestamp, err := strconv.ParseInt(v.Get("auth_date"), 10, 64)
	if err != nil {
		return u, ErrInvalid
	}
	age := now.Sub(time.Unix(timestamp, 0))
	if age < -30*time.Second || age > time.Hour {
		return u, ErrInvalid
	}
	var tg struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	}
	if json.Unmarshal([]byte(v.Get("user")), &tg) != nil || tg.ID <= 0 {
		return u, ErrInvalid
	}
	return User{tg.ID, tg.FirstName, tg.Username}, nil
}

type session struct {
	User    User  `json:"user"`
	Expires int64 `json:"expires"`
}

func Issue(u User, key string, now time.Time) string {
	b, _ := json.Marshal(session{u, now.Add(12 * time.Hour).Unix()})
	data := base64.RawURLEncoding.EncodeToString(b)
	return data + "." + base64.RawURLEncoding.EncodeToString(mac([]byte(key), data))
}
func Verify(token, key string, now time.Time) (User, error) {
	var s session
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return s.User, ErrInvalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || !hmac.Equal(sig, mac([]byte(key), parts[0])) {
		return s.User, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(b, &s) != nil || s.User.ID <= 0 || s.Expires <= now.Unix() {
		return User{}, ErrInvalid
	}
	return s.User, nil
}
