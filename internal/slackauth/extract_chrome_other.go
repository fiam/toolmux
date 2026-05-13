//go:build !darwin && !linux

package slackauth

import (
	"context"
	"errors"
)

// extractChrome on non-(macOS|Linux) is not implemented. Windows uses
// DPAPI + app-bound encryption (Chrome 127+) which is a separate decryption
// path; everything else is too niche to chase.
func extractChrome(_ context.Context, _ Options) ([]teamWithToken, string, error) {
	return nil, "", errors.New("engine 'chrome' is supported on macOS and Linux only")
}
