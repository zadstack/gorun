# Agent models

Open **AI Agents → API keys**, create an agent identity, then choose **Choose agent model**. Model choices belong to each agent, not the entire site.

Select a named AI connection and click **Load available models**. Discovery uses that site's saved credential server-side; the credential never appears in the selector. Saving checks current catalog membership and rejects stale agent or connection settings. Opening the page does not contact the provider.

## Execution modes

- **Run inside GoRun (text)**: a reviewed canvas run uses the selected connection and model. Results become new private notes. This does not activate the site's global writing provider or publish content.
- **External agent (MCP)**: GoRun hands the request to the named external agent. A selected model is a preference returned by `canvases.context`; the external client's configuration determines the actual model. Without a preference, existing external-agent behavior is preserved.

Managed execution currently generates text. Images and video use Studio/MCP provider nodes rather than a managed text agent.

## Canvas permissions

An agent needs `canvas.read`, `canvas.append`, `canvas.run`, and `content.view` scopes. The canvas owner separately grants read, append and run access to the connected input nodes. The owner must retain the matching role permissions and `content.edit`. An agent does not need permission to edit articles to add a canvas result.

Review the connected source and prompt before starting a run. Changing an agent's mode, connection or model requires renewed review and schedule consent. GoRun checks live key, ownership, grants, roles and settings at enqueue, dispatch and append. Revocation prevents the result from being appended; it cannot retract a request already sent to a provider.

Saving an agent does not run it or grant access to all canvases. Unconfigured or unavailable models fail explicitly. Catalog availability alone does not prove generation capability or remaining credit.

## Site identity

The admin sidebar uses the site's **General settings → title and logo**. Logos come from the site's own native media records. A missing or invalid image falls back to the title. Each site's identity remains independent.

## Verification cleanup API

A native owner session can `DELETE /admin/canvases/{id}` with a CSRF header and JSON `{"version": <current version>}`. The endpoint is for text-only boards: it rejects active or waiting runs and boards containing private media. Deletion checks live ownership, edit permission and version atomically, removes associated canvas metadata, and retains an audit event. It neither deletes articles referenced by the board nor cancels work at an external provider.

Testing a saved connection updates its probe status without changing its configuration version or invalidating existing agent assignments. Editing or removing the connection still invalidates those assignments and pending consent.
