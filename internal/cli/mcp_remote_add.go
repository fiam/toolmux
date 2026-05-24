package cli

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers"
)

func toolboxAddCommand(opts *options) *cobra.Command {
	var scope mcpProfileScopeOptions
	var nameFlag string
	var transport string
	var stdio bool
	var noSync bool
	var verboseHTTP bool
	var native nativeToolboxAddOptions
	cmd := &cobra.Command{
		Use:   "add <toolbox-or-url|command> [args...]",
		Short: "Add a toolbox",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])
			commandArgs := append([]string(nil), args[1:]...)
			add := mcpRemoteToolboxAddOptions{
				Scope:       scope,
				Name:        nameFlag,
				Transport:   transport,
				Stdio:       stdio,
				CommandArgs: commandArgs,
				NoSync:      noSync,
				VerboseHTTP: verboseHTTP,
			}
			if !add.ExplicitStdio() && len(commandArgs) == 0 {
				if handled, err := addNativeToolbox(cmd, opts, target, native, args); handled || err != nil {
					return err
				}
			}
			return addMCPRemoteToolbox(cmd, opts, target, add, args)
		},
	}
	cmd.Flags().StringVar(&nameFlag, "name", "", "registered toolbox name")
	cmd.Flags().StringVar(&transport, "transport", "", "MCP transport: streamable-http or stdio")
	cmd.Flags().BoolVar(&stdio, "stdio", false, "register a command-backed MCP server over stdio")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "register without immediately syncing tools")
	cmd.Flags().BoolVarP(&verboseHTTP, "verbose", "v", false, "print raw remote MCP HTTP requests and responses to stderr")
	addNativeToolboxAddFlags(cmd, &native)
	addMCPProfileScopeFlags(cmd, &scope)
	return cmd
}

type mcpRemoteToolboxAddOptions struct {
	Scope       mcpProfileScopeOptions
	Name        string
	Transport   string
	Stdio       bool
	CommandArgs []string
	NoSync      bool
	VerboseHTTP bool
}

func (add mcpRemoteToolboxAddOptions) ExplicitStdio() bool {
	return add.Stdio || strings.TrimSpace(add.Transport) == mcpRemoteTransportStdio
}

type nativeToolboxAddOptions struct {
	Account         string
	Auth            string
	Token           string
	TokenEnv        string
	TokenFile       string
	Cookie          string
	CookieEnv       string
	CookieFile      string
	FromBrowser     string
	TeamID          string
	Workspace       string
	ClientID        string
	ClientSecret    string
	ClientSecretEnv string
	AuthURL         string
	TokenURL        string
	Scopes          []string
	UserScopes      []string
	TokenSource     string
	RedirectPort    int
	TimeoutSeconds  int
}

func addNativeToolboxAddFlags(cmd *cobra.Command, opts *nativeToolboxAddOptions) {
	cmd.Flags().StringVar(&opts.Account, "account", "default", "native provider account name")
	cmd.Flags().StringVar(&opts.Auth, "auth", "", "native provider auth mode: broker, browser, oauth, token, or token-cookie")
	cmd.Flags().StringVar(&opts.Token, "token", "", "provider access token to store")
	cmd.Flags().StringVar(&opts.TokenEnv, "token-env", "", "environment variable containing the provider access token")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "file containing the provider access token")
	cmd.Flags().StringVar(&opts.Cookie, "cookie", "", "provider cookie header to store with the token")
	cmd.Flags().StringVar(&opts.CookieEnv, "cookie-env", "", "environment variable containing the provider cookie header")
	cmd.Flags().StringVar(&opts.CookieFile, "cookie-file", "", "file containing the provider cookie header")
	cmd.Flags().StringVar(&opts.FromBrowser, "from-browser", "", "native provider browser auth source, such as webview or chrome")
	cmd.Flags().StringVar(&opts.TeamID, "team-id", "", "provider team or workspace ID to store as metadata")
	cmd.Flags().StringVar(&opts.Workspace, "workspace", "", "provider workspace name, Slack subdomain, or metadata; required for Slack browser auth")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", "", "OAuth client ID")
	cmd.Flags().StringVar(&opts.ClientSecret, "client-secret", "", "OAuth client secret")
	cmd.Flags().StringVar(&opts.ClientSecretEnv, "client-secret-env", "", "environment variable containing the OAuth client secret")
	cmd.Flags().StringVar(&opts.AuthURL, "auth-url", "", "OAuth authorization endpoint override")
	cmd.Flags().StringVar(&opts.TokenURL, "token-url", "", "OAuth token endpoint override")
	cmd.Flags().StringSliceVar(&opts.Scopes, "scope", nil, "OAuth scopes to request; comma-separated or repeatable")
	cmd.Flags().StringSliceVar(&opts.UserScopes, "user-scope", nil, "OAuth user scopes to request; comma-separated or repeatable")
	cmd.Flags().StringVar(&opts.TokenSource, "token-source", "auto", "OAuth token source to store: auto, bot, or user")
	cmd.Flags().IntVar(&opts.RedirectPort, "redirect-port", 0, "loopback OAuth callback port, or 0 for a random port")
	cmd.Flags().IntVar(&opts.TimeoutSeconds, "timeout-seconds", 120, "seconds to wait for OAuth completion")
}

func addNativeToolbox(cmd *cobra.Command, opts *options, target string, native nativeToolboxAddOptions, args []string) (bool, error) {
	provider, ok := providers.Lookup(target)
	if !ok || provider.AddHandler == nil {
		return false, nil
	}
	if err := authorize(cmd, opts, toolboxAddSpec(), args); err != nil {
		return true, err
	}
	store, err := opts.credentials()
	if err != nil {
		return true, err
	}
	execCtx := actionExecutionContext(commandContext(cmd), opts, store, provider)
	execCtx.Interactive = interactiveCommand(cmd, opts)
	if execCtx.OpenBrowser == nil && execCtx.Interactive {
		execCtx.OpenBrowser = openURL
	}
	execCtx.Progress = newConnectUI(cmd, opts)
	execCtx.SelectString = selectString(cmd)
	execCtx.SelectInteger = selectInteger(cmd)
	result, err := provider.AddHandler(execCtx, actions.Invocation{
		Spec:  toolboxAddSpec(),
		Args:  append([]string(nil), args...),
		Flags: nativeToolboxAddFlagValues(native),
	})
	if err != nil {
		return true, err
	}
	return true, writeActionResult(cmd, opts, execCtx, result)
}

func addMCPRemoteToolbox(cmd *cobra.Command, opts *options, target string, add mcpRemoteToolboxAddOptions, args []string) error {
	name, server, err := resolveToolboxAddTarget(target, add.Name, add.Transport, add.Stdio, add.CommandArgs)
	if err != nil {
		return err
	}
	configPath, scopeName, err := mcpProfileWritePath(add.Scope)
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
	if _, exists := config.MCP.Servers[name]; exists {
		return fmt.Errorf("MCP server %q is already registered in %s", name, configPath)
	}
	if _, exists, err := lookupMCPRemoteServer(name, ""); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("MCP server %q is already registered; use `toolmux mcp rename %s <new-name>` first", name, name)
	}
	if err := ensureMCPRemoteNameAvailable(cmd.Root(), name); err != nil {
		return err
	}
	if err := authorize(cmd, opts, toolboxAddSpec(), args); err != nil {
		return err
	}
	server = normalizeMCPRemoteServer(server)
	if err := validateMCPRemoteServer(server); err != nil {
		return err
	}
	if server.Transport == mcpRemoteTransportStdio {
		server.AuthRequired = new(false)
	}
	register := func() error {
		config.Version = 1
		config.MCP.Servers[name] = server
		if err := writeToolmuxConfigFile(configPath, config); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "registered %s toolbox %s in %s\n", scopeName, name, configPath)
		writeMCPRemoteDefaultArgumentSuggestions(cmd.OutOrStdout(), name, server)
		return nil
	}
	if add.NoSync {
		return register()
	}
	entry := mcpRemoteServerEntry{
		Name:   name,
		Scope:  scopeName,
		Scopes: []string{scopeName},
		Path:   configPath,
		Server: server,
	}
	trace := newMCPRemoteHTTPTrace(cmd.ErrOrStderr(), add.VerboseHTTP)
	cache, authRequired, err := syncMCPRemoteCacheAfterAdd(cmd, opts, entry, args, trace)
	if err != nil {
		return fmt.Errorf("initial sync failed for MCP server %s: %w", name, err)
	}
	server.AuthRequired = new(authRequired)
	if err := register(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "synced toolbox %s: %d tools\n", name, len(cache.Tools))
	return nil
}

func nativeToolboxAddFlagValues(opts nativeToolboxAddOptions) map[string]any {
	return map[string]any{
		"account":           opts.Account,
		"auth":              opts.Auth,
		"token":             opts.Token,
		"token-env":         opts.TokenEnv,
		"token-file":        opts.TokenFile,
		"cookie":            opts.Cookie,
		"cookie-env":        opts.CookieEnv,
		"cookie-file":       opts.CookieFile,
		"from-browser":      opts.FromBrowser,
		"team-id":           opts.TeamID,
		"workspace":         opts.Workspace,
		"client-id":         opts.ClientID,
		"client-secret":     opts.ClientSecret,
		"client-secret-env": opts.ClientSecretEnv,
		"auth-url":          opts.AuthURL,
		"token-url":         opts.TokenURL,
		"scope":             append([]string(nil), opts.Scopes...),
		"user-scope":        append([]string(nil), opts.UserScopes...),
		"token-source":      opts.TokenSource,
		"redirect-port":     opts.RedirectPort,
		"timeout-seconds":   opts.TimeoutSeconds,
	}
}

func resolveToolboxAddTarget(target, nameFlag, transportFlag string, stdio bool, commandArgs []string) (string, mcpRemoteServer, error) {
	if strings.TrimSpace(target) == "" {
		return "", mcpRemoteServer{}, fmt.Errorf("toolbox name or URL is required")
	}
	transport := strings.TrimSpace(transportFlag)
	if stdio {
		if transport != "" && transport != mcpRemoteTransportStdio {
			return "", mcpRemoteServer{}, fmt.Errorf("--stdio conflicts with --transport %s", transport)
		}
		transport = mcpRemoteTransportStdio
	}
	allArgs := append([]string{target}, commandArgs...)
	if transport == mcpRemoteTransportStdio {
		return resolveMCPStdioAddTarget(nameFlag, allArgs)
	}
	if isMCPRemoteURLArgument(target) {
		if len(commandArgs) > 0 {
			return "", mcpRemoteServer{}, fmt.Errorf("MCP URL adds do not accept extra command arguments; pass --stdio to register a command")
		}
		name, err := cleanToolboxAddName(nameFlag, target)
		if err != nil {
			return "", mcpRemoteServer{}, err
		}
		if transport == "" {
			transport = mcpRemoteTransportStreamableHTTP
		}
		if err := validateMCPRemoteURL(target); err != nil {
			return "", mcpRemoteServer{}, err
		}
		if err := validateMCPRemoteTransport(transport); err != nil {
			return "", mcpRemoteServer{}, err
		}
		return name, mcpRemoteServer{URL: strings.TrimSpace(target), Transport: transport}, nil
	}
	catalogName, err := cleanMCPRemoteName(target)
	if err == nil {
		if _, ok := mcpBuiltinRemoteServers()[catalogName]; ok && len(commandArgs) > 0 {
			return "", mcpRemoteServer{}, fmt.Errorf("toolbox %q is a known catalog entry; pass --stdio to register a command with this name", catalogName)
		}
		if provider, ok := providers.Lookup(catalogName); ok && provider.AddHandler != nil && len(commandArgs) > 0 {
			return "", mcpRemoteServer{}, fmt.Errorf("toolbox %q is a native provider; pass --stdio to register a command with this name", catalogName)
		}
	} else if len(commandArgs) == 0 {
		return resolveMCPStdioAddTarget(nameFlag, allArgs)
	}
	if len(commandArgs) > 0 {
		return resolveMCPStdioAddTarget(nameFlag, allArgs)
	}
	builtin, ok := mcpBuiltinRemoteServers()[catalogName]
	if !ok {
		return resolveMCPStdioAddTarget(nameFlag, allArgs)
	}
	name := catalogName
	if strings.TrimSpace(nameFlag) != "" {
		name, err = cleanMCPRemoteName(nameFlag)
		if err != nil {
			return "", mcpRemoteServer{}, err
		}
	}
	if transport == "" {
		transport = builtin.Transport
	}
	if transport == "" {
		transport = mcpRemoteTransportStreamableHTTP
	}
	if err := validateMCPRemoteTransport(transport); err != nil {
		return "", mcpRemoteServer{}, err
	}
	builtin.Transport = transport
	if err := validateMCPRemoteURL(builtin.URL); err != nil {
		return "", mcpRemoteServer{}, err
	}
	return name, builtin, nil
}

func resolveMCPStdioAddTarget(nameFlag string, argv []string) (string, mcpRemoteServer, error) {
	command, commandArgv, err := cleanMCPRemoteCommand(argv)
	if err != nil {
		return "", mcpRemoteServer{}, err
	}
	name := strings.TrimSpace(nameFlag)
	if name == "" {
		name, err = defaultMCPRemoteNameFromCommand(command, commandArgv)
		if err != nil {
			return "", mcpRemoteServer{}, err
		}
	}
	cleanedName, err := cleanMCPRemoteName(name)
	if err != nil {
		return "", mcpRemoteServer{}, err
	}
	return cleanedName, mcpRemoteServer{
		Command:   command,
		Args:      commandArgv,
		Transport: mcpRemoteTransportStdio,
	}, nil
}

func cleanMCPRemoteCommand(argv []string) (string, []string, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", nil, fmt.Errorf("stdio MCP transport requires a command after --")
	}
	cleaned := append([]string(nil), argv...)
	cleaned[0] = strings.TrimSpace(cleaned[0])
	for _, arg := range cleaned {
		if strings.ContainsRune(arg, 0) {
			return "", nil, fmt.Errorf("stdio MCP command arguments cannot contain NUL bytes")
		}
	}
	return cleaned[0], append([]string(nil), cleaned[1:]...), nil
}

func defaultMCPRemoteNameFromCommand(command string, args []string) (string, error) {
	candidate := mcpRemoteCommandNameCandidate(command, args)
	name := mcpRemoteNameFromPackageLikeValue(candidate)
	if name == "" {
		return "", fmt.Errorf("could not derive a toolbox name from command %q; pass --name", command)
	}
	if _, err := cleanMCPRemoteName(name); err != nil {
		return "", fmt.Errorf("could not derive a valid toolbox name from command %q; pass --name", command)
	}
	return name, nil
}

func mcpRemoteCommandNameCandidate(command string, args []string) string {
	base := strings.TrimSuffix(filepath.Base(command), ".exe")
	switch base {
	case "npx", "bunx", "uvx":
		if value := firstMCPRemotePackageArg(args); value != "" {
			return value
		}
	case "npm", "pnpm", "yarn", "bun":
		remaining := args
		if len(remaining) > 0 {
			switch remaining[0] {
			case "exec", "dlx", "x":
				remaining = remaining[1:]
			}
		}
		if value := firstMCPRemotePackageArg(remaining); value != "" {
			return value
		}
	case "docker", "podman":
		if value := mcpRemoteContainerRunImage(args); value != "" {
			return value
		}
	}
	return base
}

func firstMCPRemotePackageArg(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if (arg == "-p" || arg == "--package") && i+1 < len(args) {
				return args[i+1]
			}
			if !strings.Contains(arg, "=") && mcpRemoteOptionLikelyHasValue(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

func mcpRemoteContainerRunImage(args []string) string {
	runIndex := slices.Index(args, "run")
	if runIndex < 0 {
		return ""
	}
	for i := runIndex + 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && mcpRemoteOptionLikelyHasValue(arg) && i+1 < len(args) {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

func mcpRemoteOptionLikelyHasValue(option string) bool {
	switch option {
	case "-e", "--env", "--env-file", "--label", "--label-file",
		"-p", "--publish", "--expose", "-v", "--volume", "--mount",
		"--name", "--network", "-w", "--workdir", "-u", "--user",
		"--platform", "--entrypoint", "--add-host", "--hostname",
		"--pull", "--package", "--cache", "--registry", "--tag":
		return true
	default:
		return false
	}
}

func mcpRemoteNameFromPackageLikeValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "docker.io/")
	if at := strings.Index(value, "@sha256:"); at >= 0 {
		value = value[:at]
	}
	if colon := strings.LastIndex(value, ":"); colon > strings.LastIndex(value, "/") {
		value = value[:colon]
	}
	value = strings.Trim(value, "/")
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	value = strings.TrimPrefix(value, "@")
	value = strings.TrimSuffix(value, ".git")
	replacements := []struct {
		old string
		new string
	}{
		{"mcp-server-", ""},
		{"server-", ""},
		{"-mcp-server", ""},
		{"_mcp_server", ""},
		{".mcp-server", ""},
		{"-mcp", ""},
		{"_mcp", ""},
		{".mcp", ""},
	}
	for _, replacement := range replacements {
		value = strings.TrimPrefix(value, replacement.old)
		value = strings.TrimSuffix(value, replacement.old)
		value = strings.ReplaceAll(value, replacement.old, replacement.new)
	}
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, value)
	return strings.Trim(value, "-_")
}

func cleanToolboxAddName(nameFlag, rawURL string) (string, error) {
	if strings.TrimSpace(nameFlag) != "" {
		return cleanMCPRemoteName(nameFlag)
	}
	name, err := defaultMCPRemoteNameFromURL(rawURL)
	if err != nil {
		return "", err
	}
	return cleanMCPRemoteName(name)
}
