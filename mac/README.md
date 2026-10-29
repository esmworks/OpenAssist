# mac — SwiftUI panel + Mac runner

A single bundle does two jobs:

1. **Panel (thin client):** bots, chat (SSE stream), Approval Center, Activity,
   Tasks, routines, settings. Data lives on the server; only a view cache is kept
   here.
2. **Runner:** opens an **outbound** WebSocket to core (no port is opened on the Mac,
   K7), serves file tools (`read_file`, `write_file`, `list_dir`,
   `trash_item` — no permanent delete), and never goes outside the local policy roots.
   While the app is closed, the bots on the server keep running; Mac tools are visible
   only while connected (K2).

## Layers

```
UI/            design layer (SwiftUI built one-to-one from the claude.ai/design prototype)
UI/DesignStore view state + actions — gets its data from LiveStore
UI/LiveStore   server bridge: sync loops, SSE stream, mapping to design types
APIClient      thin client for the core private API (:8080)
Models         Codable counterparts of the Go DTOs (unknown fields are not errors)
Runner/        WS protocol, file tools, LocalPolicy (root confinement + symlink escape protection)
```

Design types (`DAgent`, `DMsg`, `DTask`…) are produced from server types by **pure**
functions in `LiveStore`; this mapping is tested
(`LiveMappingTests`) — a wrong mapping means a wrong approval.

## Running

```bash
swift build -c release && .build/release/OpenAssistMac
```

The server address is entered under **Settings › Connections** (Ayarlar › Bağlantılar;
default `http://127.0.0.1:8080`); the same screen shows the server and runner
connection status. On a remote server the private API is not exposed — use Tailscale
or an SSH tunnel.

```bash
swift test    # 24 tests (policy, file tools, wire protocol, mapping)
```

Type safety on the Swift side: `Codable` + typed enums; the server contract is derived
from the normalized event vocabulary on the Go side (K18). CI does not run Swift
(the runners are Linux) — a local `swift test` is mandatory.

## Packaging (.app)

```bash
./Scripts/make-app.sh          # .build/OpenAssist.app
```

A bare `swift build` output is an **unbundled process** to macOS; three things don't
work: notifications (`UNUserNotificationCenter` requires a bundle identifier),
launch at login (`SMAppService`), and the correct name/icon in the Dock. Their
counterparts in Settings appear disabled in an unbundled run and say why — showing
them as enabled while they silently do nothing would be a lie.

## Known limits (v1)

- The **Extensions** (Eklentiler) screen is a catalog preview; there is no install
  flow. Real connectors are connected from chat (K13: "connect Gmail") and appear
  under Settings › Connections.
- The **Engines** (Motorlar) screen lists the CLIs on the server with their status
  (`GET /api/engines`): whether installed, which version, which identity it runs with,
  how many bots use it. The probe is cached on the server for 5 min; "Refresh"
  refreshes it without waiting. Per-bot **engine + model** selection is under Settings ›
  Bots; its options also come from this endpoint — the client does NOT embed a list,
  because a model name the engine does not recognize breaks every run (K18).
  v1 has a single engine (Claude Code); a second adapter comes in v1.5.
- **There is no auto-update and no language selection** — their rows in Settings were
  removed (no updater, the UI is single-language). A switch that does nothing is
  worse than no switch.
- Team chat is v2 (the server carries `kind=team`; there are no multi-bot threads).
- Signing/notarization awaits an Apple Developer account; `make-app.sh`
  signs ad hoc — it works on this Mac but is not enough for distribution.

## Sources of settings

The screen does not mix three separate sources of truth:

| Field | Source | Note |
|---|---|---|
| Menu bar, notification switches, theme | UserDefaults | a missing key falls back to its default (the rest are not reset) |
| Launch at startup | `SMAppService` | the SYSTEM is asked for the state; if it is disabled from the system, the switch turns off too |
| Daily quota, quiet hours | `/api/policy` | the veto is enforced by the orchestrator (K17) |
| Engine + model | `/api/bots`, `/api/engines` | per bot (K18) |
| Version, bundle, macOS | `Bundle` / `ProcessInfo` | the About screen does not hard-code values |
