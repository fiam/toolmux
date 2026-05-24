package cli

import (
	"context"
	"errors"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
)

func selectString(cmd *cobra.Command) func(context.Context, actions.SelectStringRequest) (string, bool, error) {
	return func(ctx context.Context, request actions.SelectStringRequest) (string, bool, error) {
		if len(request.Options) == 0 {
			return "", false, nil
		}
		selected := request.Options[0].Value
		options := make([]huh.Option[string], 0, len(request.Options))
		for _, option := range request.Options {
			options = append(options, huh.NewOption(option.Label, option.Value))
		}
		height := request.Height
		if height <= 0 {
			height = min(len(options)+4, 12)
		}
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title(request.Title).
				Description(request.Description).
				Options(options...).
				Value(&selected).
				Height(height).
				Filtering(request.Filtering),
		)).
			WithTheme(huh.ThemeCharm()).
			WithInput(cmd.InOrStdin()).
			WithOutput(cmd.ErrOrStderr()).
			WithWidth(terminalWidth(cmd.ErrOrStderr())).
			WithHeight(height + 5)
		if err := form.RunWithContext(ctx); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return "", false, nil
			}
			return "", false, err
		}
		return selected, true, nil
	}
}

func selectInteger(cmd *cobra.Command) func(context.Context, actions.SelectIntegerRequest) (int, bool, error) {
	return func(ctx context.Context, request actions.SelectIntegerRequest) (int, bool, error) {
		if len(request.Options) == 0 {
			return 0, false, nil
		}
		selected := request.Options[0].Value
		options := make([]huh.Option[int], 0, len(request.Options))
		for _, option := range request.Options {
			options = append(options, huh.NewOption(option.Label, option.Value))
		}
		height := request.Height
		if height <= 0 {
			height = min(len(options)+4, 14)
		}
		form := huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().
				Title(request.Title).
				Description(request.Description).
				Options(options...).
				Value(&selected).
				Height(height).
				Filtering(request.Filtering),
		)).
			WithTheme(huh.ThemeCharm()).
			WithInput(cmd.InOrStdin()).
			WithOutput(cmd.ErrOrStderr()).
			WithWidth(terminalWidth(cmd.ErrOrStderr())).
			WithHeight(height + 5)
		if err := form.RunWithContext(ctx); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return 0, false, nil
			}
			return 0, false, err
		}
		return selected, true, nil
	}
}
