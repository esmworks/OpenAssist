# Contributing to OpenAssist

Thanks for your interest in improving OpenAssist. This guide covers how to set up the project, what
we expect from a pull request, and the rules the codebase follows. The layout and the development
commands are in [`README.md`](README.md); design decisions are recorded in
[`docs/design/`](docs/design/).

## Before you start

- For bugs, open an issue with steps to reproduce, the expected and actual behavior, and the
  OpenAssist version (`core version`, or the About screen in the Mac app).
- For larger changes (new features, new tools, new dependencies, database schema changes), open an
  issue first so the approach can be agreed on before you write the code.
- Security issues: do **not** open a public issue. Follow [`SECURITY.md`](SECURITY.md).

## Development setup

Requirements: Go (the version in `go.mod`), PostgreSQL for integration tests, `sqlc` 1.31.1 and
`golangci-lint`. The Mac app needs Xcode / Swift 6 on macOS 14+.

```bash
cp .env.example .env
make build
make test
DATABASE_URL=postgres://... make test-integration
```

For the Mac app:

```bash
cd mac && swift build && swift test
```

## Pull request checklist

A pull request is ready for review when:

- `go build ./...`, `go vet ./...`, `go test ./...` and `make lint` pass.
- `make sqlc-check` passes: after changing `db/queries/` or `db/migrations/`, run `make generate`
  and commit the generated code in `internal/store/db`. Never edit it by hand.
- Integration tests pass against a real Postgres (`make test-integration`).
- If you touched `mac/`, `swift test` passes locally (CI runs on Linux and does not build Swift).
- New behavior has tests; bug fixes come with a test that fails without the fix.
- The change is focused: one topic per pull request.

## Code rules

- Go code is formatted with `gofmt`; lint rules are in `.golangci.yml`.
- Comments explain *why*, briefly. Existing comments are in Turkish; new comments may be in Turkish
  or English.
- Configuration comes only from typed environment variables (`internal/config`); validate new
  settings in `Validate()`.
- Every tool with side effects goes through the approval gate (`internal/policy`,
  `internal/orchestrator`); do not add a path around it.
- Never log or return secrets (provider keys, connector tokens, `API_TOKEN`); sealed values stay
  sealed with `MASTER_KEY`.
- Ask in an issue before adding a new dependency.

## Developer Certificate of Origin

OpenAssist uses the [Developer Certificate of Origin](https://developercertificate.org/) (DCO). By
contributing you certify that you wrote the change or otherwise have the right to submit it under
the project's license. Sign off every commit:

```bash
git commit -s -m "fix: ..."
```

This adds a `Signed-off-by: Your Name <you@example.com>` line to the commit message.

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
