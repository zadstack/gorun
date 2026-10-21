# Shared agent role templates

Open **Agent role templates** from the opposite-side Agents drawer, or visit `/admin/agents/templates` in the advanced workspace. Team members with content editing permission can inspect seven built-in examples in English or Arabic:

- Researcher: source discipline and research handoff.
- Bilingual editor: faithful Arabic/English drafting and editorial review.
- SEO reviewer: supplied-page audit and safe metadata recommendations.
- Support knowledge assistant: grounded answers and escalation.
- Operations reader: read-only diagnosis and operator handoff.
- Campaign planner: campaign structure and approval handoff.
- Orchestrator: step plans and role routing without automatic execution.

Each role combines five bounded complementary skills and a role-specific suggested setup: sources for research, supplied research/glossary for editing, provided pages/read-only sources for SEO, approved knowledge for support, sanitized logs/read-only checks for operations, and reviewed drafts with human Metricool handoff for campaigns. These hints never select or authorize a connection. Each example explains its goal, inputs, private text output, skill instructions and a sample handoff. The catalog contains no company resources, credentials, private endpoints or preselected profile IDs. These roles are teaching recipes, not deployed clients, new runtimes or an unattended team. A person supplies approved sources and transfers or connects reviewed output to the next task. Read the instructions before adopting an example for your organization.

The role/category colors distinguish examples, not availability. Connection badges describe saved historical results only. Detailed execution boundaries remain inspectable in the native **Execution details** disclosure.

## Prepare a configuration

Applying an example additionally requires settings and agent-key management permission. Select an **existing active agent key owned by you**, then open the configuration draft. This read-only step replaces the fields displayed in the existing Skills & MCP editor with the template skills, clears its MCP selections and managed tool policy, and retains the current optimistic bundle version. Leaving the page cancels without changes.

Review and edit every instruction. Select only the actual MCP references you intend to bind. **Save bundle** uses the existing CSRF, recent-authentication, ownership, aggregate-size and configuration-version checks. Saving replaces the previous bundle and retires older review/run authority through the existing bundle guards. It never creates a key, grants new permissions, selects a model or enables managed tools. The selected agent's model assignment remains unchanged; use **Review model / external-client setup** to choose through the existing model editor.

A manager can see permitted saved AI/MCP configuration labels and their historical test results. The list is available configuration, not a live readiness check and not proof of an agent being connected. A missing list means setup is needed, not that an example has silently obtained access. GoRun profiles are shared site configuration gated by settings permission; another user's agent key is never offered. Ordinary editors see static recipes without saved profile names or metadata.

## Execution boundaries

Saved skills are applied to the actual canvas prompt and appear in the existing review before execution. An external-mode agent still needs its authenticated external client to poll, retrieve reviewed context and append a private result. A managed text model uses its existing configured transport.

MCP reference discovery alone does not authorize tool calls. The bounded managed tool loop supports named OpenAI-compatible managed text models, manual runs only, a separately reviewed exact allowlist and individual owner approval for each exact call. See [managed MCP tools](MANAGED-MCP-TOOLS.md) and [agent bundles](AGENT-BUNDLES.md). Anthropic profiles and external clients must not be presented as having this same managed tool loop. No role template enables scheduling, inbound automation, publication, Metricool posting, SSH, deployment or arbitrary server control. S3 configuration is storage, not an automatic agent file-access grant.

The examples produce suggestions and private drafts. Humans remain responsible for source accuracy, approval and any publishing or operational changes.
