# AGENT.md

Guidance for Claude Code (claude.ai/code) when working in this repository.

## What this is

Two Go binaries in one module:

- `holmes-bridge` — receives incident.io webhooks, asks HolmesGPT to investigate,
  writes the analysis back onto the incident.
- `incidentio-mock` — a wire-compatible fake incident.io, so the bridge can be
  developed and tested without a real org.

HolmesGPT is **not** mocked. `task holmes:install` deploys the upstream
robusta/holmes chart into a local cluster (you create it with kind), backed by
OpenRouter. `task demo` runs the whole loop against it.

## Build & Development Commands

```bash
task test             # go test ./...
task test:race        # race detector
task build            # both binaries into ./bin
task generate         # regenerate the OpenAPI server + client
task spec:refresh     # re-trim the upstream spec, then regenerate
task golangci:lint

task holmes:install   # HolmesGPT into the current cluster, via OpenRouter
task holmes:status    # which models and toolsets came up
task demo             # mock + bridge + real Holmes + broken workload

go test -run TestName ./path/to/package
go tool golangci-lint run -c .golangci.yaml ./...
```

Environment names follow one rule: **unprefixed is production** (the same names
the bridge reads in a cluster), **`TEST_` is local-only** (the demo and the
helper scripts, read by no binary). `OPENROUTER_API_KEY` is production but
deployment-time — the bridge never reads it; the installer and the chart use it
to build the cluster Secret HolmesGPT reads. Adding a knob that only the demo
needs? Prefix it, or it will look like something a deployment must set.

Local configuration is layered: your shell beats `.env` (gitignored, holds
`OPENROUTER_API_KEY`) beats `.env.default` (committed). `hack/lib.sh` provides
`load_dotenv`, which the scripts source so direct invocation matches `task`.
Never put a secret in `.env.default` or `.env.example` — both are tracked.

`Taskfile.yaml` deliberately omits the `goreleaser` and `license` includes:
both resolve the project name through `gh repo view` in an eager `sh:` var,
which Task evaluates at parse time, so including them before this repo has a
GitHub origin breaks *every* task. Restore them once it is pushed.

## The generated wire contract

`api/incidentio/openapi.yaml` is **generated**, not hand-written. `hack/trim-spec.py`
downloads the live incident.io spec (145 operations, 1029 schemas) and trims it
to the 37 operations this repo implements, transitively closing over the schemas
they reference.

`api/incidentio/server.gen.go` carries **both halves**: the server interface the
mock implements, and the client the bridge calls a real incident.io with. That
single package is what stops the two drifting.

**To cover a new incident.io endpoint:**

1. Add its path to `KEEP` in `hack/trim-spec.py`.
2. `task spec:refresh` — re-trims and regenerates.
3. Implement the new method on `api/incidentio/v1.Server`; the build fails until
   you do, because the generated interface grew.

Never hand-edit `openapi.yaml` or `server.gen.go`.

## Key patterns

- **Domain types alias the generated wire types.** `incidents.Incident` is
  `incidentio.IncidentV2`. For a fake, the spec is the source of truth; a
  parallel model plus mappers would double the code and give the wire shape a
  second place to drift. Ports (repository interfaces) still live in the domain.
- **The mock's state is in memory**, seeded from YAML fixtures. `internal/infra/memory`
  is the only adapter; a Postgres one would slot in behind the same ports.
- **Fixtures are not wire objects.** `internal/infra/fixtures` expands a friendly
  YAML schema into full incident.io objects, resolving relative timestamps
  (`-40m`, `-3d`) against load time and validating cross-references at boot.
- **Errors use incident.io's envelope** (`internal/api`). Handlers call
  `c.Error(...)`; `middleware.ErrorHandler` renders it and stamps the request ID.
- **Both binaries share** `internal/globals` (HTTP + metric server flags),
  `internal/otel`, `internal/metrics`, `internal/middleware` and `api/private`.

## Things that will bite you

**Webhook signing is base64-keyed.** The HMAC key is the base64 *decoding* of the
secret after `whsec_`, not its literal bytes. Getting this wrong still verifies
against itself, so only the pinned Standard Webhooks vector in
`internal/domain/webhooks/signature_test.go` catches it. Do not remove that test.

**The bridge must not react to its own write-back.** Posting an analysis is a
change; incident.io emits an event for it; that event can re-trigger the
investigation that caused it, forever. Two guards:

- the per-incident cooldown in `internal/app/investigate` (`ErrCoolingDown`), and
- the mock emitting `incident_updated_v2` rather than `incident_status_updated_v2`
  when an update carries no status change.

`test/e2e.TestWriteBackDoesNotRetriggerItself` fails if either is removed.

**Ports follow one rule: local = `1` + the conventional port.** The mock is
18080, the bridge 18081, and a Holmes port-forward 15050 — 8080/8081 are the
most contested ports on a dev machine, and the two binaries must not collide
with each other either. The default comes from
`kong.Vars{"default_http_port": ...}` in each `main.go`, because
`globals.HTTPServer` is shared and a literal in the struct tag would apply to
both. The Helm charts use the same numbers, so there is exactly one port per
service to remember.

**Kong flag tags.** Field names like `IncidentIOURL` derive into `--incident-iourl`,
so flags carry explicit `name:` tags. Slice flags that take comma-containing
values (`--webhook`) need `sep:"none"`, or kong splits them into fragments.

**HolmesGPT has no `/api/investigate`.** Current releases expose only
`POST /api/chat`; the incident context goes into the prompt. See
`internal/app/investigate/prompt.go`.

**`/api/info` returns `models` (a list), not `model`.** It also carries
`toolsets_summary`. The bridge checks both at startup: a `HOLMES_MODEL` the
server does not serve fails every investigation, and zero enabled toolsets means
Holmes answers from the prompt alone — speculation rather than investigation.

**A synchronous investigation outlives the default HTTP write timeout.**
`POST /investigations/{id}` blocks for minutes; the 30s default would truncate
the response after the work had already completed and posted. `Serve.Run` raises
the write timeout to `--investigation-timeout + 30s` and logs it. Do not "tidy
that away".

**Fixture data must stay unmistakably fake.** The mock is wire-identical to
incident.io, so its records are indistinguishable from real ones unless they
say otherwise: references are `TEST-nnn` (`fixtures.ReferencePrefix`), incident
names carry `[TEST]`, emails sit on `.invalid`, and nothing links to
`app.incident.io`. `internal/infra/fixtures/safety_test.go` enforces it — those
tests exist because mock records reach Slack channel names, log lines and LLM
prompts, where a realistic-looking incident is something a person acts on by
mistake. Do not relax them to make a fixture read better.

**Notifications are fire-and-forget.** `investigate.Notifier` is an optional
port; `internal/infra/ntfy` implements it. A push must never change an
investigation's outcome — the analysis is on the incident either way — so
publish errors are logged and dropped. Only the analysis's Summary section is
pushed (`headline()`), and skips are silent.

**Concurrency is per incident.** `inflight` collapses duplicate triggers for one
incident, `lastRun` gives each its own cooldown, and `slots` bounds how many run
at once. The slot wait is bounded by `QueueTimeout` — without it, webhook-driven
investigations (whose context is never cancelled) queue indefinitely and a storm
becomes a backlog of stale analyses.

**There are two pipelines, sharing one engine.** `incidentio serve` is incident.io
in and incident.io out; `ntfy serve` is Alertmanager in and a notification out, with no
incident.io client at all (the service tolerates a nil one because only the
incident path uses it). Both go through `Service.guarded`, which owns the
per-subject claim, the cooldown, the slot budget, metrics and the notification —
they differ only in what they read and where the answer goes. Add a third
pipeline by writing a prompt builder and calling `guarded`, not by duplicating
that machinery.

**Alertmanager webhooks cannot be signed.** incident.io signs; Alertmanager
offers only `http_config.authorization`. So `--alertmanager-token` is the whole
of the authentication, and a reachable endpoint without one is open. Dedup keys
off Alertmanager's `groupKey`, which is stable across re-notifications.

**Each pipeline owns its subcommands.** `incidentio identity|incidents|show` and
`ntfy config|test` exist so a single integration can be checked without running
an investigation. They embed the same option groups the daemon uses, so what
they exercise is the same code path — `ntfy test` uses `Ntfy.Client()` rather
than the notifier, because the notifier swallows publish errors by design.

**Only the webhook is inbound.** The bridge calls the incident.io API,
HolmesGPT and ntfy outward; nothing about those needs exposing. `--expose`
exists solely so incident.io's webhooks can reach the bridge, which is why the
tunnel publishes only `/webhooks/incidentio` by default. `.env` and `--help` are
both grouped by direction to keep that clear.

**`--expose` needs subdomain routing.** The bridge can publish its webhook
endpoint through a holt reverse tunnel (`cmd/holmes-bridge/commands/expose/expose.go`,
built on `holt/pkg/expose`). incident.io sends a fixed set of headers, so a
header-routed hub can never address this peer — `webhookURL` rejects that at
startup rather than letting every delivery miss. The peer id is deliberately
fixed (`--expose-peer`, default `holmes-bridge`) because it doubles as the
hostname, and a generated one would change the webhook URL on every restart.

**The demo needs a cluster.** `task demo` port-forwards to the Holmes installed
by `task holmes:install` and applies `hack/demo-workload.yaml` — a `checkout-api`
that crashloops because `postgres-primary` has no endpoints. The
`checkout-crashloop` fixture describes that exact failure, so the incident and
the cluster agree and the investigation has real evidence. If you change one,
change the other.

## Testing

- `internal/domain/webhooks` — signature scheme, including the canonical vector.
- `internal/infra/memory` — cursor pagination (in-package: tests unexported code).
- `internal/infra/fixtures` — loading, validation, timestamp expansion.
- `internal/infra/webhooks` — delivery, retries, filtering, secret rotation.
- `internal/app/investigate` — orchestration, skip rules, cooldown, dedup.
- `test/e2e` — the whole loop over real HTTP: mock + bridge + stub Holmes.

`hack/stub-holmes.py` is a HolmesGPT stand-in that prints the prompt it receives.

## Commit Convention

[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):
`feat`, `fix`, `docs`, `chore`, `refactor`, `test`.

```bash
git commit -m "feat: investigate on alert firing as well as incident creation"
```

## Release Workflow

1. Commit (the pre-commit hook runs `task`).
2. Tag and push: `git tag v0.x.x && git push && git push --tags`.
3. Goreleaser builds both binaries and both images from CI.
