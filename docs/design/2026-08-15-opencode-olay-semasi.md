# opencode event schema: from assumption to measurement (2026-08-15)

Scope: K18 (engine boundary), Appendix B.2-1c (lossless event passthrough). This document
records why the opencode adapter never worked, what the real schema is, and
how to measure the schema WITHOUT A KEY.

## 1. The bug: the schema was made up

The adapter expected the output of `opencode run --format json` to look like this:

```
{"type":"session","session_id":"…"}   {"type":"text_delta","text":"…"}
{"type":"done","ok":true,"stop_reason":"…","text":"…","tokens_in":N}
```

**None of these** exist in the real output. The schema's source was an assumption; the unit
tests were written with the same assumption, so they were green. In production, since the
`done` line never arrived, **even successful turns** failed with "opencode exited without
producing a result event".

Second layer: the error message printed only **stderr**. opencode writes its errors
to stdout as JSON and leaves stderr empty — the line the user saw was
`(wait: exit status 1; stderr: )`. The cause was in our hands and we were throwing it away.

## 2. The real schema (1.18.18, measured from the binary)

Every line is a **part update**; `sessionID` is carried on every line
(camelCase). There is **no** end-of-turn line — what ends the turn is the process exit.

```
{"type":"step_start" ,"sessionID":"ses_…","part":{…,"type":"step-start"}}
{"type":"text"       ,"sessionID":"ses_…","part":{"id":"prt_…","type":"text","text":"Merhaba"}}
{"type":"tool_use"   ,"sessionID":"ses_…","part":{"type":"tool","tool":"read","callID":"call_1",
                       "state":{"status":"completed","input":{…},"output":"…","title":"…"}}}
{"type":"step_finish","sessionID":"ses_…","part":{"reason":"stop","tokens":{"input":5,"output":3},"cost":0}}
{"type":"error"      ,"sessionID":"ses_…","error":{"name":"APIError","data":{"message":"Invalid API key."}}}
```

Impact on the adapter:

- **The turn summary is built at EOF** (`parser.sonuc()`); the adapter publishes the
  `turn.completed` event, so the event vocabulary stays the same as other engines.
- **Numbers are summed**: a turn that uses tools produces several `step_finish` lines,
  each reporting its own tokens/cost.
- **A tool may arrive already finished in a single line**: from that line both `tool.started`
  (with input) and `tool.completed` are produced, otherwise the UI does not see what was called.
- **Text parts are tracked by ID**: if the same part is updated and arrives
  again, only the appended portion becomes a delta (text is not counted twice).
- `stop` → `success` in the shared vocabulary (same rule as the codex adapter).
- An error line fails the turn and **carries the message**; when no event could be produced,
  the error text now also includes the **last stdout line** alongside stderr.

## 3. Measurement method: fake provider (no key, no network)

No subscription key was needed to learn the schema. opencode's **global**
config (`~/.config/opencode/opencode.json`) MERGES with `OPENCODE_CONFIG`
(does not override — measured), so we can write the provider into the global file and
redirect without changing the adapter at all:

```json
{"provider":{"openai":{"options":{"baseURL":"http://127.0.0.1:PORT/v1"},
 "models":{"test-model":{"name":"Test"}}}}}
```

The local server speaks the OpenAI **Responses API** stream (not Chat Completions:
the default openai provider uses Responses, and an incompatible response gives the
"Received a Chat Completions stream…" error). This harness is permanent in
`canli_integration_test.go`: real binary + fake model with three turns
(plain text, two steps with a tool, 401 error path). It runs with `-tags integration`
and is skipped if opencode is not installed. Also verified in a container:

```
GOOS=linux GOARCH=arm64 go test -c -tags integration -o /tmp/oc.test ./internal/engine/opencode
docker run --rm --user 1000:1000 -e HOME=/home/node -v /tmp/oc.test:/oc.test:ro \
  --entrypoint /oc.test <imaj> -test.run TestCanli -test.v
```

This is the opencode leg of the **K18 conformance suite** that sits in the roadmap as
"v1.5, outside CI": the golden event sequences are now taken from the real binary.

## 4. Side findings

- **There is no `--system` flag.** The system prompt is written to a temporary config as an `agent`
  definition (`mode: "primary"`) and selected with `--agent openassist-run`.
  On an unknown flag, yargs prints the usage text and exits 1.
- **A new session without a title makes an extra model request** (measured: 2
  requests → 1 with `--title`). The title is now a shortened form of the prompt;
  this also removes the empty call to a model that is not in the subscription.
- The model list is read from the binary during probing (`opencode models`); there is no
  embedded list — on an engine whose catalog changes, a hand-written list was going stale
  silently.
