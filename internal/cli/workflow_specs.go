package cli

import (
	"github.com/fiam/toolmux/internal/actions"
	"github.com/fiam/toolmux/internal/policy"
)

func workflowInitSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.init", "init",
		actions.Use("workflow init <name>"),
		actions.Short("Create workflow"),
		actions.RBAC("workflow", actions.VerbCreate, actions.EffectNone, actions.EffectWrite),
	)
}

func workflowListSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.list", "list",
		actions.Use("workflow list"),
		actions.Short("List workflows"),
		actions.RBAC("workflow", actions.VerbList, actions.EffectNone, actions.EffectRead),
	)
}

func workflowShowSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.show", "show",
		actions.Use("workflow show <name>"),
		actions.Short("Show workflow"),
		actions.RBAC("workflow", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}

func workflowRenderSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.render", "render",
		actions.Use("workflow render <name>"),
		actions.Short("Render workflow prompt"),
		actions.RBAC("workflow", actions.VerbRead, actions.EffectNone, actions.EffectRead),
	)
}

func workflowRunSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.run", "run",
		actions.Use("workflow run <name>"),
		actions.Short("Run workflow"),
		actions.RBAC("workflow", actions.VerbRun, actions.EffectWrite, actions.EffectWrite),
	)
}

func workflowConfigSetDefaultAgentSpec() policy.CommandSpec {
	return actions.Command("toolmux.workflow.config.default_agent", "default-agent",
		actions.Use("workflow config set default-agent [agent]"),
		actions.Short("Set default workflow agent"),
		actions.RBAC("workflow_config", actions.VerbUpdate, actions.EffectNone, actions.EffectWrite),
	)
}
