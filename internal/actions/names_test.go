package actions

import "testing"

func TestCLIName(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"auth_test":         "auth-test",
		"cloudId":           "cloud-id",
		"HTTPServerURL":     "http-server-url",
		"find-structure":    "find-structure",
		"notion-get-users":  "notion-get-users",
		"already__trimmed":  "already-trimmed",
		" unsupported.name": "unsupported.name",
	}
	for input, want := range tests {
		if got := CLIName(input); got != want {
			t.Errorf("CLIName(%q) = %q, want %q", input, got, want)
		}
	}
}
