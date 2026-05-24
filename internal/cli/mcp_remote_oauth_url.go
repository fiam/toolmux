package cli

import (
	"fmt"
	"net/url"
	"strings"
)

func canonicalMCPRemoteResourceURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("resource URI must include scheme and host")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String(), nil
}

func originMCPRemoteResourceURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return canonicalMCPRemoteResourceURI(parsed.String())
}

func wellKnownOAuthMetadataURL(resourceOrIssuer, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(resourceOrIssuer))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("metadata issuer/resource must include scheme and host")
	}
	pathPart := parsed.EscapedPath()
	if pathPart == "/" {
		pathPart = ""
	}
	if pathPart != "" && !strings.HasPrefix(pathPart, "/") {
		pathPart = "/" + pathPart
	}
	parsed.Path = "/.well-known/" + suffix + pathPart
	parsed.RawPath = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func wellKnownOpenIDAppendedMetadataURL(issuer string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(issuer))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("metadata issuer must include scheme and host")
	}
	pathPart := strings.TrimRight(parsed.EscapedPath(), "/")
	parsed.Path = pathPart + "/.well-known/openid-configuration"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
