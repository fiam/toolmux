package client

import (
	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/providers/slack"
)

const ProviderName actions.ProviderName = slack.ProviderName

const (
	CapabilityReadConversations = slack.CapabilityReadConversations
	CapabilityReadGroups        = slack.CapabilityReadGroups
	CapabilityReadIMs           = slack.CapabilityReadIMs
	CapabilityReadMPIMs         = slack.CapabilityReadMPIMs
	CapabilitySearch            = slack.CapabilitySearch
	CapabilityWriteChat         = slack.CapabilityWriteChat
)

const (
	ActionConversationsList actions.LocalName = "conversations.list"
	ActionMessageSend       actions.LocalName = "message.send"
	ActionSearchMessages    actions.LocalName = "search"
)

const (
	ResourceConversation actions.ResourceName = "conversation"
	ResourceMessage      actions.ResourceName = "message"
	ResourceWorkspace    actions.ResourceName = "workspace"
)

func CommandTree() actions.Spec {
	return group("slack", "Operate Slack conversations and messages",
		group("conversations", "Read Slack conversations",
			spec(ActionConversationsList, "ls", ResourceConversation, actions.VerbList, actions.EffectRead, nil, conversationScopes(), actions.Use("ls"), actions.Short("List Slack conversations"), intFlag("limit", 100, "maximum conversations"), stringFlag("types", "public_channel,private_channel,mpim,im", "conversation types: public_channel, private_channel, mpim, im"), boolFlag("include-archived", false, "include archived conversations"), stringFlag("team", "", "team id for org-wide tokens")),
		),
		group("message", "Send Slack messages",
			spec(ActionMessageSend, "send", ResourceMessage, actions.VerbSend, actions.EffectWrite, []string{"message-send"}, []string{CapabilityWriteChat}, actions.Use("send --channel <id-or-name> [text]"), actions.Short("Send a Slack message"), actions.MinArgs(0), stringFlag("channel", "", "channel id or name"), stringFlag("text", "", "message text"), stringFlag("thread", "", "thread timestamp to reply to"), boolFlag("mrkdwn", true, "enable Slack mrkdwn parsing"), boolFlag("dry-run", false, "show request without sending the message")),
		),
		spec(ActionSearchMessages, "search", ResourceWorkspace, actions.VerbSearch, actions.EffectRead, nil, []string{CapabilitySearch}, actions.Use("search [query]"), actions.Short("Search Slack messages"), actions.MinArgs(0), stringFlag("query", "", "Slack search query"), intFlag("limit", 20, "maximum search results"), stringFlag("sort", "timestamp", "sort: timestamp, score"), stringFlag("direction", "desc", "sort direction: asc, desc"), boolFlag("highlight", false, "include Slack highlight markers in results")),
	)
}

func DefaultCapabilities() []string {
	return slack.DefaultCapabilities()
}

func conversationScopes() []string {
	return []string{
		CapabilityReadConversations,
		CapabilityReadGroups,
		CapabilityReadIMs,
		CapabilityReadMPIMs,
	}
}

func spec(name actions.LocalName, segment string, resource actions.ResourceName, verb actions.Verb, effect actions.Effect, risk, scopes []string, extra ...actions.Option) actions.Spec {
	opts := []actions.Option{actions.RBAC(resource, verb, effect)}
	if len(risk) > 0 {
		opts = append(opts, actions.Risks(risk...))
	}
	if len(scopes) > 0 {
		opts = append(opts, actions.Scopes(scopes...))
	}
	opts = append(opts, extra...)
	return actions.Command(name, segment, opts...)
}

func group(segment, short string, children ...actions.Spec) actions.Spec {
	return actions.Group(segment, actions.Use(segment), actions.Short(short), actions.Children(children...))
}

func boolFlag(name string, defaultValue bool, usage string) actions.Option {
	return actions.BoolFlag(name, defaultValue, usage)
}

func intFlag(name string, defaultValue int, usage string) actions.Option {
	return actions.IntFlag(name, defaultValue, usage)
}

func stringFlag(name, defaultValue, usage string) actions.Option {
	return actions.StringFlag(name, defaultValue, usage)
}
