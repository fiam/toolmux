package slackapi

import (
	"net/url"
	"path"
	"strings"
)

func CleanScopes(values []string) []string {
	seen := map[string]bool{}
	var scopes []string
	for _, value := range values {
		for part := range strings.FieldsSeq(strings.ReplaceAll(value, ",", " ")) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			scopes = append(scopes, part)
		}
	}
	return scopes
}

func SplitScopes(value string) []string {
	return CleanScopes([]string{value})
}

func APIURLFromBase(baseURL, method string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + "/" + method
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + method
	return parsed.String()
}

func APIBaseURLFromTeamURL(teamURL string) string {
	teamURL = strings.TrimSpace(teamURL)
	if teamURL == "" {
		return ""
	}
	parsed, err := url.Parse(teamURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = path.Join(strings.TrimRight(parsed.Path, "/"), "api")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func OAuthURLFromAPIBase(baseURL, suffix string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	if prefix, ok := strings.CutSuffix(strings.TrimRight(parsed.Path, "/"), "/api"); ok {
		parsed.Path = prefix
	}
	parsed.Path = path.Join(parsed.Path, suffix)
	return parsed.String()
}
