package linear

import (
	"context"
	"testing"

	"github.com/fiam/toolmux/internal/testutil/fakeupstream"
)

func TestClientViewerAndMutationsAgainstFakeUpstream(t *testing.T) {
	upstream := fakeupstream.New()
	defer upstream.Close()

	client := NewClient(
		"fake-access-token",
		WithEndpoint(upstream.URL+"/linear/graphql"),
		WithHTTPClient(upstream.Client()),
	)
	ctx := context.Background()

	viewer, err := client.Viewer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if viewer.ID == "" {
		t.Fatalf("expected viewer id, got %#v", viewer)
	}

	issues, err := client.ListIssues(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Identifier != "SUP-1" {
		t.Fatalf("unexpected issues: %#v", issues)
	}

	gotIssue, err := client.GetIssue(ctx, "issue-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotIssue.Identifier != "SUP-1" {
		t.Fatalf("unexpected issue: %#v", gotIssue)
	}

	issue, err := client.CreateIssue(ctx, CreateIssueInput{TeamID: "team-1", Title: "Test issue"})
	if err != nil {
		t.Fatal(err)
	}
	if issue.Identifier != "SUP-2" {
		t.Fatalf("unexpected created issue: %#v", issue)
	}

	comment, err := client.CreateComment(ctx, CreateCommentInput{IssueID: issue.ID, Body: "Looks good"})
	if err != nil {
		t.Fatal(err)
	}
	if comment.ID == "" || comment.Body == "" {
		t.Fatalf("unexpected comment: %#v", comment)
	}
}
