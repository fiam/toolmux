package cli

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

func manageMCPRemoteCatalogInteractive(cmd *cobra.Command, opts *options, scope mcpProfileScopeOptions, syncEnabled bool) error {
	if !interactiveCommand(cmd, opts) {
		return fmt.Errorf("--manage requires table output with interactive stdin, stdout, and stderr")
	}
	entries, err := mcpRemoteCatalogEntries(cmd.Root(), opts)
	if err != nil {
		return err
	}
	selected := make([]string, 0, len(entries))
	options := make([]huh.Option[string], 0, len(entries))
	for _, entry := range entries {
		if entry.Status == "alias_required" || entry.Status == "unavailable" {
			continue
		}
		var title string
		switch {
		case entry.Registered && entry.Tools != nil:
			title = fmt.Sprintf("%s as %s (%d tools)", entry.Name, strings.Join(entry.RegisteredNames, ", "), *entry.Tools)
		case entry.Registered:
			title = fmt.Sprintf("%s as %s", entry.Name, strings.Join(entry.RegisteredNames, ", "))
		default:
			title = entry.Name + " (available)"
		}
		option := huh.NewOption(title, entry.Name)
		if entry.Registered {
			selected = append(selected, entry.Name)
			option = option.Selected(true)
		}
		options = append(options, option)
	}
	if len(options) == 0 {
		return fmt.Errorf("no manageable built-in MCP servers are available")
	}
	height := min(len(options)+4, 12)
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Manage remote MCP servers").
			Description("Selected servers will be registered; unchecked registered servers will be removed.").
			Options(options...).
			Value(&selected).
			Height(height).
			Filterable(false),
	)).
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.ErrOrStderr()).
		WithWidth(terminalWidth(cmd.ErrOrStderr())).
		WithHeight(height + 7)
	if err := form.RunWithContext(commandContext(cmd)); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "no MCP catalog changes selected")
			return nil
		}
		return err
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}
	var enable []string
	var disable []string
	for _, entry := range entries {
		if entry.Status == "alias_required" || entry.Status == "unavailable" {
			continue
		}
		if selectedSet[entry.Name] && !entry.Registered {
			enable = append(enable, entry.Name)
		}
		if !selectedSet[entry.Name] && entry.Registered {
			disable = append(disable, entry.Name)
		}
	}
	if len(enable) == 0 && len(disable) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no MCP catalog changes selected")
		return nil
	}
	return applyMCPRemoteCatalogChanges(cmd, opts, scope, enable, disable, syncEnabled)
}

func applyMCPRemoteCatalogChanges(cmd *cobra.Command, opts *options, scope mcpProfileScopeOptions, enable, disable []string, syncEnabled bool) error {
	enableSpecs, err := parseMCPRemoteCatalogEnableSpecs(enable)
	if err != nil {
		return err
	}
	disableNames, err := cleanMCPRemoteCatalogNames(disable)
	if err != nil {
		return err
	}
	if len(enableSpecs) == 0 && len(disableNames) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no MCP catalog changes selected")
		return nil
	}
	builtins := mcpBuiltinRemoteServers()
	registeredEntries, err := effectiveMCPRemoteServerEntries("")
	if err != nil {
		return err
	}
	registered := make(map[string]bool, len(registeredEntries))
	for _, entry := range registeredEntries {
		registered[entry.Name] = true
	}
	var enabled []mcpRemoteServerEntry
	if len(enableSpecs) > 0 {
		configPath, scopeName, err := mcpProfileWritePath(scope)
		if err != nil {
			return err
		}
		config, err := readToolmuxConfigFile(configPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if config.MCP.Servers == nil {
			config.MCP.Servers = map[string]mcpRemoteServer{}
		}
		changed := false
		for _, spec := range enableSpecs {
			server, ok := builtins[spec.CatalogName]
			if !ok {
				return fmt.Errorf("unknown built-in MCP server %q", spec.CatalogName)
			}
			if rootNativeCommandHasName(cmd.Root(), spec.RegisteredName) {
				if spec.CatalogName == spec.RegisteredName {
					return fmt.Errorf("MCP server name %q conflicts with an existing Toolmux command; use `toolmux mcp catalog --enable %s=<new-name>`", spec.RegisteredName, spec.CatalogName)
				}
				return fmt.Errorf("MCP server name %q conflicts with an existing Toolmux command; choose a different name", spec.RegisteredName)
			}
			if registered[spec.RegisteredName] {
				continue
			}
			server = normalizeMCPRemoteServer(server)
			config.MCP.Servers[spec.RegisteredName] = server
			registered[spec.RegisteredName] = true
			changed = true
			enabled = append(enabled, mcpRemoteServerEntry{
				Name:   spec.RegisteredName,
				Scope:  scopeName,
				Scopes: []string{scopeName},
				Path:   configPath,
				Server: server,
			})
		}
		if changed {
			config.Version = 1
			if err := writeToolmuxConfigFile(configPath, config); err != nil {
				return err
			}
			for _, entry := range enabled {
				catalogName := entry.Name
				for _, spec := range enableSpecs {
					if spec.RegisteredName == entry.Name {
						catalogName = spec.CatalogName
						break
					}
				}
				if catalogName == entry.Name {
					fmt.Fprintf(cmd.OutOrStdout(), "enabled %s MCP server %s in %s\n", entry.Scope, entry.Name, configPath)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "enabled %s MCP server %s as %s in %s\n", entry.Scope, catalogName, entry.Name, configPath)
				}
				writeMCPRemoteDefaultArgumentSuggestions(cmd.OutOrStdout(), entry.Name, entry.Server)
			}
		}
	}
	if len(disableNames) > 0 {
		removed, err := removeMCPRemoteCatalogServers(disableNames, opts.mcpCacheDir)
		if err != nil {
			return err
		}
		for _, name := range removed {
			fmt.Fprintf(cmd.OutOrStdout(), "disabled MCP server %s\n", name)
		}
	}
	if syncEnabled {
		for _, entry := range enabled {
			cache, authRequired, err := syncMCPRemoteCacheExplicit(cmd, opts, entry, []string{entry.Name}, nil)
			if err != nil {
				return err
			}
			if err := writeMCPRemoteAuthRequired(entry, authRequired); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "synced MCP server %s: %d tools\n", entry.Name, len(cache.Tools))
		}
	}
	return nil
}

func parseMCPRemoteCatalogEnableSpecs(values []string) ([]mcpRemoteCatalogEnable, error) {
	seen := map[string]bool{}
	var specs []mcpRemoteCatalogEnable
	for _, raw := range values {
		for part := range strings.SplitSeq(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			catalogName, registeredName, hasAlias := strings.Cut(part, "=")
			catalogName, err := cleanMCPRemoteName(catalogName)
			if err != nil {
				return nil, err
			}
			if hasAlias {
				registeredName, err = cleanMCPRemoteName(registeredName)
				if err != nil {
					return nil, err
				}
			} else {
				registeredName = catalogName
			}
			key := catalogName + "=" + registeredName
			if !seen[key] {
				seen[key] = true
				specs = append(specs, mcpRemoteCatalogEnable{CatalogName: catalogName, RegisteredName: registeredName})
			}
		}
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].CatalogName == specs[j].CatalogName {
			return specs[i].RegisteredName < specs[j].RegisteredName
		}
		return specs[i].CatalogName < specs[j].CatalogName
	})
	return specs, nil
}

func cleanMCPRemoteCatalogNames(values []string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, raw := range values {
		for part := range strings.SplitSeq(raw, ",") {
			name, err := cleanMCPRemoteName(part)
			if err != nil {
				return nil, err
			}
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func removeMCPRemoteCatalogServers(names []string, cacheDir string) ([]string, error) {
	remove := make(map[string]bool, len(names))
	builtins := mcpBuiltinRemoteServers()
	for _, name := range names {
		remove[name] = true
	}
	globalPath, err := globalToolmuxConfigPath()
	if err != nil {
		return nil, err
	}
	sources, err := loadMCPProfileSources("", globalPath)
	if err != nil {
		return nil, err
	}
	removed := map[string]bool{}
	for _, source := range sources {
		changed := false
		for requestedName := range remove {
			if builtin, ok := builtins[requestedName]; ok {
				for registeredName, server := range source.config.MCP.Servers {
					if registeredName == requestedName || sameMCPRemoteServer(server, builtin) {
						delete(source.config.MCP.Servers, registeredName)
						changed = true
						removed[registeredName] = true
					}
				}
				continue
			}
			if _, ok := source.config.MCP.Servers[requestedName]; ok {
				delete(source.config.MCP.Servers, requestedName)
				changed = true
				removed[requestedName] = true
			}
		}
		if changed {
			if err := writeToolmuxConfigFile(source.Path, source.config); err != nil {
				return nil, err
			}
		}
	}
	var ordered []string
	for _, name := range names {
		if removed[name] {
			_ = removeMCPRemoteCache(cacheDir, name)
			ordered = append(ordered, name)
		}
	}
	for name := range removed {
		if !slices.Contains(ordered, name) {
			_ = removeMCPRemoteCache(cacheDir, name)
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	return ordered, nil
}

func mcpRemoteCatalogCommandModifies(parts []string) bool {
	for _, part := range parts {
		if part == "--manage" || part == "--enable" || part == "--disable" ||
			strings.HasPrefix(part, "--manage=") ||
			strings.HasPrefix(part, "--enable=") || strings.HasPrefix(part, "--disable=") {
			return true
		}
	}
	return false
}
