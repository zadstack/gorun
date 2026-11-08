# GoRun Coolify service template

This directory contains a proposed Coolify service template and its matching
documentation. It is not an entry in Coolify's official service library.

## Install now

In Coolify, create a **Docker Compose Empty** resource and paste
[`gorun.yaml`](coolify/templates/compose/gorun.yaml). Set your HTTPS domain
with container port `8080`, verify the proxy network, then deploy.
Open `/admin/setup` and enter `SERVICE_HEX_32_GORUNSETUP` from the resource's
environment variables to create the first administrator.

The image is pinned to `ghcr.io/zadstack/gorun:0.19.0`. No source build or
external database is required. Follow the [deployment guide](../coolify.md)
for proxy configuration, backups and stop-then-start upgrades.
The existing [pasteable Compose file](../coolify-compose.yml) remains available
with its configurable image tag; this submission copy uses a fixed version as
required by Coolify.

## Upstream submission status

Checked on 2026-11-08: the [Coolify contribution guide](https://coolify.io/docs/contribute/service)
says external contributions are temporarily paused, with no reopening date,
and requires at least 1,000 GitHub stars for a new service. GoRun currently has
0 stars. Do not submit these files until contributions reopen and the service
meets the eligibility rule. Recheck the upstream guide before submitting.

When eligible, copy the contents of `coolify/` into the Coolify repository and
`coolify-docs/` into the Coolify documentation repository. Use the current
`release/v4.x` target branch specified by upstream for the template PR. Link the
matching documentation PR. Do not edit generated service listings manually.
Both SVG files are copies of GoRun's existing
[brand mark](../../assets/branding/gorun-mark.svg); keep them synchronized if
that mark changes. Update the pinned image and docs together for new releases.

## Validation before submission

Static checks alone do not verify Coolify's parser or routing. Complete these
checks on a fresh Docker Compose Empty resource before opening a PR:

- Confirm generated URL, base64 key and setup token appear in environment variables.
- Verify the trusted proxy CIDR, HTTPS routing and `/healthz` response.
- Finish `/admin/setup`, then sign in as the first administrator.
- Upload media and redeploy with the same volume and key; confirm both the
  account and media survive.
- Confirm the old container stops before its replacement starts.
- Build the documentation in the upstream docs repository and inspect its CMS
  service card, logo and service page.

These deployment and upstream docs-build checks have not been performed for
this proposal. Local validation and registry checks are reported separately.
