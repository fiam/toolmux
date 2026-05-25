package googleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
)

func postOAuthToken(ctx context.Context, client *http.Client, options OAuthOptions, values url.Values) (OAuthTokenResponse, error) {
	tokenURL := firstNonEmpty(options.TokenURL, DefaultTokenURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultResponseLimit))
	if err != nil {
		return OAuthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return OAuthTokenResponse{}, fmt.Errorf("google OAuth token endpoint returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out OAuthTokenResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return OAuthTokenResponse{}, err
	}
	if out.Error != "" {
		return OAuthTokenResponse{}, fmt.Errorf("google OAuth failed: %s", firstNonEmpty(out.ErrorDescription, out.Error))
	}
	return out, nil
}

func (c Client) get(ctx context.Context, suffix string, values url.Values, out any) error {
	return c.getURL(ctx, apiURL(c.BaseURL, suffix), values, out)
}

func (c Client) getDocs(ctx context.Context, suffix string, values url.Values, out any) error {
	return c.getURL(ctx, docsAPIURL(c, suffix), values, out)
}

func (c Client) getURL(ctx context.Context, rawURL string, values url.Values, out any) error {
	reqURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	return doGoogle(req, c.HTTPClient, out)
}

func (c Client) getBytes(ctx context.Context, suffix string, values url.Values) ([]byte, error) {
	reqURL, err := url.Parse(apiURL(c.BaseURL, suffix))
	if err != nil {
		return nil, err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	req.Header.Set("Accept", "*/*")
	return doGoogleBytes(req, c.HTTPClient)
}

func (c Client) postJSON(ctx context.Context, suffix string, body any, out any) error {
	return c.postJSONQuery(ctx, suffix, nil, body, out)
}

func (c Client) postDocsJSON(ctx context.Context, suffix string, body any, out any) error {
	return c.postJSONURL(ctx, docsAPIURL(c, suffix), nil, body, out)
}

func (c Client) postJSONQuery(ctx context.Context, suffix string, values url.Values, body any, out any) error {
	return c.postJSONURL(ctx, apiURL(c.BaseURL, suffix), values, body, out)
}

func (c Client) postJSONURL(ctx context.Context, rawURL string, values url.Values, body any, out any) error {
	return c.jsonURL(ctx, http.MethodPost, rawURL, values, body, out)
}

func (c Client) patchJSONQuery(ctx context.Context, suffix string, values url.Values, body any, out any) error {
	return c.jsonURL(ctx, http.MethodPatch, apiURL(c.BaseURL, suffix), values, body, out)
}

func (c Client) jsonURL(ctx context.Context, method string, rawURL string, values url.Values, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	reqURL, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	return doGoogle(req, c.HTTPClient, out)
}

func (c Client) postMultipartQuery(ctx context.Context, suffix string, values url.Values, metadata any, mediaType string, media []byte, out any) error {
	return c.multipartQuery(ctx, http.MethodPost, suffix, values, metadata, mediaType, media, out)
}

func (c Client) patchMultipartQuery(ctx context.Context, suffix string, values url.Values, metadata any, mediaType string, media []byte, out any) error {
	return c.multipartQuery(ctx, http.MethodPatch, suffix, values, metadata, mediaType, media, out)
}

func (c Client) multipartQuery(ctx context.Context, method string, suffix string, values url.Values, metadata any, mediaType string, media []byte, out any) error {
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	metadataHeader := textproto.MIMEHeader{}
	metadataHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metadataPart, err := writer.CreatePart(metadataHeader)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(metadataPart).Encode(metadata); err != nil {
		return err
	}
	mediaHeader := textproto.MIMEHeader{}
	mediaHeader.Set("Content-Type", firstNonEmpty(mediaType, "application/octet-stream"))
	mediaPart, err := writer.CreatePart(mediaHeader)
	if err != nil {
		return err
	}
	if _, err := mediaPart.Write(media); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	reqURL, err := url.Parse(apiURL(c.BaseURL, suffix))
	if err != nil {
		return err
	}
	query := reqURL.Query()
	for key, vals := range values {
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	reqURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), &requestBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	c.authorize(req)
	return doGoogle(req, c.HTTPClient, out)
}

func (c Client) authorize(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(c.AccessToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func doGoogle(req *http.Request, client *http.Client, out any) error {
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultResponseLimit))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("google API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("google API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return err
	}
	return nil
}

func doGoogleBytes(req *http.Request, client *http.Client) ([]byte, error) {
	resp, err := httpClient(client).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultResponseLimit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("google API rate limited request; retry after %s", resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("google API returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func apiURL(baseURL, suffix string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + "/" + strings.TrimLeft(suffix, "/")
	}
	parsed.Path = path.Join(parsed.Path, strings.TrimLeft(suffix, "/"))
	return parsed.String()
}

func docsAPIURL(client Client, suffix string) string {
	baseURL := strings.TrimSpace(client.DocsBaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(client.BaseURL)
		if baseURL == "" || strings.TrimRight(baseURL, "/") == strings.TrimRight(DefaultAPIBaseURL, "/") {
			baseURL = DefaultDocsAPIBaseURL
		}
	}
	return apiURL(baseURL, suffix)
}

func httpClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return http.DefaultClient
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
