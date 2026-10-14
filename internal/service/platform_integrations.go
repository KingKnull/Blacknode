package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
	"github.com/google/uuid"
)

type Integration struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	HasToken bool   `json:"hasToken"`
}

func validateEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("enter an endpoint without credentials, query or fragment")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	local := host == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("use HTTPS, or HTTP on localhost")
	}
	return u, nil
}

func (s *PlatformService) SaveIntegration(ctx context.Context, config Integration, token string, clearToken bool, revision int64) (store.OperationDocument, error) {
	if config.Name == "" || len(config.Name) > 120 {
		return store.OperationDocument{}, errors.New("integration name required")
	}
	switch config.Kind {
	case "prometheus", "loki", "github", "gitlab":
	default:
		return store.OperationDocument{}, errors.New("unsupported integration")
	}
	if _, err := validateEndpoint(config.URL); err != nil {
		return store.OperationDocument{}, err
	}
	if config.ID == "" {
		config.ID = uuid.NewString()
	}
	if revision < 0 { return store.OperationDocument{}, errors.New("invalid revision") }
	key := "platform.token." + config.ID
	secrets := []store.OperationSecret{}
	config.HasToken, _ = s.settings.settings.HasSecret(key)
	if token != "" {
		cipher, nonce, err := s.settings.vault.Encrypt([]byte(token))
		if err != nil {
			return store.OperationDocument{}, err
		}
		secrets = append(secrets, store.OperationSecret{Key: key, Cipher: cipher, Nonce: nonce})
		config.HasToken = true
	} else if clearToken {
		secrets = append(secrets, store.OperationSecret{Key: key})
		config.HasToken = false
	}
	return s.store.PutWithSecrets("integration", config.ID, revision, config, secrets)
}

func (s *PlatformService) integrationRequest(ctx context.Context, config Integration, method, resource string, query url.Values, body []byte) ([]byte, error) {
	u, err := validateEndpoint(config.URL)
	if err != nil {
		return nil, err
	}
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/") + "/" + strings.TrimLeft(resource, "/")
	u.Path,err = url.PathUnescape(u.RawPath)
	if err!=nil{return nil,err}
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	cipher, nonce, err := s.settings.settings.GetSecret("platform.token." + config.ID)
	if err != nil {
		return nil, err
	}
	if len(cipher) > 0 {
		token, err := s.settings.vault.Decrypt(cipher, nonce)
		if err != nil {
			return nil, err
		}
		if config.Kind == "gitlab" {
			request.Header.Set("PRIVATE-TOKEN", string(token))
		} else {
			request.Header.Set("Authorization", "Bearer "+string(token))
		}
		for i := range token {
			token[i] = 0
		}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := readBounded(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, redactEvidence(string(data[:min(len(data), 1000)])))
	}
	return data, nil
}

func (s *PlatformService) QueryObservability(ctx context.Context, integrationID, query string, startMS, endMS int64, stepSeconds int) (string, error) {
	if query == "" || len(query) > 16384 {
		return "", errors.New("query required")
	}
	if startMS <= 0 || endMS <= startMS || endMS-startMS > 14*24*60*60*1000 || endMS > time.Now().Add(time.Minute).UnixMilli() {
		return "", errors.New("choose a past time range of at most 14 days")
	}
	var config Integration
	if err := s.store.Read("integration", integrationID, &config); err != nil {
		return "", err
	}
	params := url.Values{"query": {query}}
	resource := ""
	switch config.Kind {
	case "prometheus":
		resource = "api/v1/query_range"
		stepSeconds = max(stepSeconds, int((endMS-startMS)/1000/2000)+1)
		params.Set("start", strconv.FormatFloat(float64(startMS)/1000, 'f', 3, 64))
		params.Set("end", strconv.FormatFloat(float64(endMS)/1000, 'f', 3, 64))
		params.Set("step", strconv.Itoa(max(1, stepSeconds)))
	case "loki":
		resource = "loki/api/v1/query_range"
		params.Set("start", strconv.FormatInt(startMS*1000000, 10))
		params.Set("end", strconv.FormatInt(endMS*1000000, 10))
		params.Set("limit", "1000")
		params.Set("direction", "forward")
	default:
		return "", errors.New("select a Prometheus or Loki integration")
	}
	data, err := s.integrationRequest(ctx, config, "GET", resource, params, nil)
	if err != nil {
		return "", err
	}
	var result any
	if err = json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	pretty, _ := json.MarshalIndent(result, "", "  ")
	return redactEvidence(string(pretty)), nil
}
