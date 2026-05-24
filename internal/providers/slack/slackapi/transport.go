package slackapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c Client) get(ctx context.Context, method string, values url.Values, out any) error {
	endpoint := APIURLFromBase(c.BaseURL, method)
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, val := range vals {
			query.Add(key, val)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	return doSlack(req, c.HTTPClient, out)
}

func (c Client) postForm(ctx context.Context, method string, values url.Values, out any) error {
	if values == nil {
		values = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, APIURLFromBase(c.BaseURL, method), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.authorize(req)
	return doSlack(req, c.HTTPClient, out)
}

func (c Client) authorize(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.AccessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie := strings.TrimSpace(c.Cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
}

func doSlack(req *http.Request, client *http.Client, out any) error {
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("slack API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("slack API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return err
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&envelope); err != nil {
		return err
	}
	if !envelope.OK {
		return fmt.Errorf("slack API failed: %s", firstNonEmpty(envelope.Error, "unknown_error"))
	}
	return nil
}

func postOAuthToken(ctx context.Context, client *http.Client, options OAuthOptions, values url.Values) (OAuthTokenResponse, error) {
	tokenURL := strings.TrimSpace(options.TokenURL)
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	clientID := strings.TrimSpace(options.ClientID)
	clientSecret := strings.TrimSpace(options.ClientSecret)
	if clientID != "" || clientSecret != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OAuthTokenResponse{}, fmt.Errorf("slack OAuth token endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out OAuthTokenResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return OAuthTokenResponse{}, err
	}
	if !out.OK {
		return OAuthTokenResponse{}, fmt.Errorf("slack OAuth failed: %s", firstNonEmpty(out.Error, "unknown_error"))
	}
	return out, nil
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
