# Security Policy

OpenAssist runs AI agents on a server that can act on your behalf: they call connected services
(Gmail, Calendar, Telegram), reach the network and, through the Mac app, read and write files on
your computer. It also stores provider keys and connector tokens. We take reports about these
boundaries seriously.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report privately through GitHub's
[private vulnerability reporting](https://github.com/esmworks/OpenAssist/security/advisories/new)
(Security tab > Report a vulnerability). Include:

- the affected version (`core version` or the commit) and how it is deployed,
- a description of the issue and its impact,
- steps or a proof of concept to reproduce it.

We aim to acknowledge reports within 5 business days and will keep you updated while we work on a
fix. Once a fix is released we are happy to credit you in the advisory, unless you prefer to stay
anonymous.

## Supported versions

Security fixes are made on the `main` branch only. Please make sure you are on the latest commit
before reporting.

## Scope

In scope, for example:

- the approval gate being bypassed (a tool with side effects running without the approval its
  policy requires),
- the private API (`:8080`) being reachable without the bearer token, or the internal MCP surface
  (`INTERNAL_HTTP_ADDR`, loopback only) being reachable from outside the host,
- the public surface (`:8081`) exposing anything beyond webhooks and the OAuth callback,
- provider keys, connector tokens or `API_TOKEN` leaking into logs, API responses or the Mac app,
- the Mac runner reading or writing outside its configured roots, or deleting files permanently,
- Telegram messages from chats outside the allowlist being processed.

Out of scope:

- actions an agent takes with permissions the user explicitly granted (for example an "always
  allow" rule),
- prompt injection that only results in actions the user then approves,
- vulnerabilities in model providers, the pi engine or third-party services themselves,
- deployments that expose the private API to the internet against the setup guide.
