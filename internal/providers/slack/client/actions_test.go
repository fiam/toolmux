package client

import (
	"strings"
	"testing"

	"github.com/fiam/toolmux/internal/credentials"
	"github.com/fiam/toolmux/internal/output"
)

func TestNormalizeSearchQueryExpandsFromMe(t *testing.T) {
	t.Parallel()
	tokens := credentials.OAuthTokens{Extra: map[string]string{"user_id": "U123456"}}

	got := normalizeSearchQuery("from:me foo bar", tokens)
	if got != "from:<@U123456> foo bar" {
		t.Fatalf("unexpected normalized query %q", got)
	}
}

func TestConversationTableUsesUserIDForIMs(t *testing.T) {
	t.Parallel()
	rendered := renderTable(ListConversationsResponse{
		Channels: []Conversation{{
			ID:   "D123456",
			User: "U123456",
			IsIM: true,
		}},
	}.Table(output.Options{}))

	if !strings.Contains(rendered, "U123456") || !strings.Contains(rendered, "im") {
		t.Fatalf("expected IM table to include user id and type, got %q", rendered)
	}
}

func renderTable(table output.Table) string {
	var builder strings.Builder
	output.RenderTable(&builder, output.Options{}, table)
	return builder.String()
}
