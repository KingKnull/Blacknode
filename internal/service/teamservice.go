package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type TeamConnection struct {
	URL      string `json:"url"`
	HasToken bool   `json:"hasToken"`
}

// TeamService proxies the selected runner using a vault-sealed token. Tokens
// never need to be placed in browser URLs or sent with terminal input events.
type TeamService struct{ settings *SettingsService }

func NewTeamService(settings *SettingsService) *TeamService { return &TeamService{settings: settings} }
func (s *TeamService) Connection(ctx context.Context) (TeamConnection, error) {
	url, cipher, _, err := s.settings.settings.GetValueWithSecret("team.runner.connection")
	if err != nil {
		return TeamConnection{}, err
	}
	return TeamConnection{URL: url, HasToken: len(cipher) > 0}, nil
}
func teamRequest(ctx context.Context, endpoint, token, method, resource, body string) (string, error) {
	u, err := validateEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resource, "/api/") || strings.ContainsAny(resource, "\r\n\\") || strings.Contains(resource, "..") || len(resource) > 4096 || len(body) > 2<<20 {
		return "", errors.New("invalid runner request")
	}
	if method != "GET" && method != "POST" && method != "DELETE" {
		return "", errors.New("unsupported method")
	}
	uri := strings.TrimRight(u.String(), "/") + resource
	req, err := http.NewRequestWithContext(ctx, method, uri, bytes.NewBufferString(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := readBounded(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if failure.Error == "" {
			failure.Error = response.Status
		}
		return "", fmt.Errorf("runner: %s", failure.Error)
	}
	if !json.Valid(data) {
		return "", errors.New("runner returned invalid JSON")
	}
	return string(data), nil
}
func (s *TeamService) SaveConnection(ctx context.Context, endpoint, token string) error {
	if len(token) < 32 || len(token) > 256 {
		return errors.New("runner access token required")
	}
	if _, err := teamRequest(ctx, endpoint, token, "GET", "/api/me", ""); err != nil {
		return err
	}
	cipher, nonce, err := s.settings.vault.Encrypt([]byte(token))
	if err != nil {
		return err
	}
	return s.settings.settings.SetValueWithSecret("team.runner.connection", endpoint, cipher, nonce)
}
func (s *TeamService) Disconnect(ctx context.Context) error {
	return s.settings.settings.Delete("team.runner.connection")
}
func (s *TeamService) Request(ctx context.Context, method, resource, body string) (string, error) {
	endpoint, cipher, nonce, err := s.settings.settings.GetValueWithSecret("team.runner.connection")
	if err != nil {
		return "", err
	}
	if len(cipher) == 0 {
		return "", errors.New("connect to a runner first")
	}
	token, err := s.settings.vault.Decrypt(cipher, nonce)
	if err != nil {
		return "", err
	}
	defer func() {
		for i := range token {
			token[i] = 0
		}
	}()
	return teamRequest(ctx, endpoint, string(token), method, resource, body)
}
