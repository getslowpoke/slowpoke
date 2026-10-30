package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const settingsKey = "mail:smtp"

// Store keeps the SMTP account set from the panel, for whoever installs Slowpoke without
// touching the server's environment. SLOWPOKE_SMTP_* still wins when it is set.
type Store struct{ DB *store.DB }

type saved struct {
	Provider  string `json:"provider,omitempty"`
	Host      string `json:"host"`
	Port      string `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	APIKey    string `json:"api_key,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Region    string `json:"region,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	From      string `json:"from"`
}

func (s Store) Get(ctx context.Context) (Account, error) {
	var raw string
	err := s.DB.Q().Row(ctx, "SELECT value FROM app_settings WHERE key = ?", settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, nil
	}
	if err != nil {
		return Account{}, err
	}
	var v saved
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Account{}, err
	}
	return Account{Provider: v.Provider, Host: v.Host, Port: v.Port, Username: v.Username, Password: v.Password,
		APIKey: v.APIKey, Domain: v.Domain, Region: v.Region, AccountID: v.AccountID, From: v.From}, nil
}

func (s Store) Save(ctx context.Context, a Account) error {
	raw, _ := json.Marshal(saved{Provider: a.Provider, Host: a.Host, Port: a.Port, Username: a.Username, Password: a.Password,
		APIKey: a.APIKey, Domain: a.Domain, Region: a.Region, AccountID: a.AccountID, From: a.From})
	_, err := s.DB.Q().Exec(ctx, "INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", settingsKey, string(raw))
	return err
}

func (s Store) Clear(ctx context.Context) error {
	_, err := s.DB.Q().Exec(ctx, "DELETE FROM app_settings WHERE key = ?", settingsKey)
	return err
}

// Explain sorts a sending failure into what the person can do about it: the panel turns the
// code into a sentence, the raw error stays next to it for whoever wants the detail.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	e := strings.ToLower(err.Error())
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(e, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("unencrypted connection", "tls", "x509", "certificate", "handshake", "starttls"):
		return "tls"
	case has("mail: login", "535", "534", "authentication", "username and password", "answered 401", "answered 403", "unauthorized", "invalid api key", "api key"):
		return "auth"
	case has("sender denied", "550 5.7", "553", "sender", "not authorized to send", "not verified", "domain is not", "from address", "from field"):
		return "sender"
	case has("connection refused", "no such host", "i/o timeout", "deadline exceeded", "network is unreachable", "dial tcp"):
		return "connect"
	}
	return "other"
}
