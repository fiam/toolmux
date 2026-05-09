package client

import "github.com/fiam/toolmux/internal/providers/notion"

func NormalizeID(value string) (string, error) {
	return notion.NormalizeID(value)
}
