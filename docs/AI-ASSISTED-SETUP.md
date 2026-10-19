# Set up GoRun with an AI assistant

The [README](../README.md#start-with-your-ai-assistant) includes a tool-neutral text block you can copy into an assistant. This guide supplies the product facts that support it. No particular model, editor, agent framework or paid provider is required.

## What the assistant needs

Installation requires terminal access on the computer where GoRun will run. A chat-only assistant can give instructions; a cloud coding workspace installs into that workspace, not automatically onto your laptop.

Release installation supports macOS and Linux on Intel/AMD x86-64 and ARM64. It uses ordinary `curl`, `tar` and SHA-256 tools, and does not require Go, Docker or administrator access. The quick start needs version 0.4.0 or newer.

## Preserve what is already there

The installer puts the executable in `~/.local/bin/gorun`. The README starts a local site in `~/.local/share/gorun/site`, so repeating its block uses the same data. A manually configured installation may use a different directory; identify that before choosing a path.

Existing site data, accounts, themes and credentials stay in place. Deleting a data directory is not a password-recovery technique. The read-only `gorun doctor --data <existing-directory>` reports diagnostics without initializing or repairing a site. [Local setup and password recovery](local-start.md).

## Install and start

Use the complete first command block in the official README. It downloads the installer, verifies the release archive before executing its binary, sets up command discovery, and starts the site. Failed downloads or verification stop installation and preserve an existing executable. The default shell's startup file is updated without replacing its existing content.

The local URL is printed using the explicit IPv4 loopback address, and the browser opens the local admin. If the default port is occupied, check which service owns it; an unused port can be selected with `--addr :0`. There is no need to terminate another application to run GoRun.

The default LAN URL is for public, read-only preview. Administration, authentication and writes stay local. `--local-only` disables LAN access. This development command does not replace a [production deployment](PRODUCTION.md).

## Leave credentials with the user

The first administrator's strong random password appears once in the local terminal. It does not belong in chat messages, screenshots, exported logs, URLs or shared files. The user signs in and chooses a new password, email, site name and language in the browser. Existing accounts are not recreated or reset.

An assistant does not need an administrator password or API key to confirm that the public page and sign-in page respond. Connected content management through MCP is a separate, explicitly configured step using a scoped agent key. [MCP and permissions](MCP.md).

## Confirm the result

A successful setup has an installed executable, a responding local public page, a working sign-in page, and the expected data directory. Ctrl+C stops the server and preserves data. Restarting with the same data directory keeps the accounts and does not display the generated password again. Report those results and the local URL without reporting secrets.

This is a setup guide and copyable request, not a packaged Agent Skill or an automatic MCP connection.
