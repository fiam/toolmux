package client

import "github.com/fiam/supacli/internal/providers/notion"

func NormalizeID(value string) (string, error) {
	return notion.NormalizeID(value)
}
