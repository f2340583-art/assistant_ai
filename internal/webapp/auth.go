// Package webapp serves the Telegram Mini App: a small dashboard+tasks web
// UI backed by the same data as the chat bot, embedded in the Go binary.
package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxInitDataAge rejects stale initData to prevent replay of a captured URL.
const maxInitDataAge = 24 * time.Hour

type tgUser struct {
	ID int64 `json:"id"`
}

// ValidateInitData verifies Telegram's signed initData string and returns
// the Telegram user ID it identifies, per Telegram's documented algorithm:
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func ValidateInitData(initData, botToken string) (int64, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return 0, fmt.Errorf("parse init data: %w", err)
	}

	hash := values.Get("hash")
	if hash == "" {
		return 0, fmt.Errorf("missing hash")
	}
	values.Del("hash")

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+values.Get(k))
	}
	dataCheckString := strings.Join(lines, "\n")

	secretKey := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	computed := hex.EncodeToString(hmacSHA256(secretKey, []byte(dataCheckString)))

	if !hmac.Equal([]byte(computed), []byte(hash)) {
		return 0, fmt.Errorf("invalid hash")
	}

	authDateUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid auth_date")
	}
	if time.Since(time.Unix(authDateUnix, 0)) > maxInitDataAge {
		return 0, fmt.Errorf("init data expired")
	}

	var user tgUser
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil {
		return 0, fmt.Errorf("parse user: %w", err)
	}
	if user.ID == 0 {
		return 0, fmt.Errorf("missing user id")
	}

	return user.ID, nil
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
