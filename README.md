# holmes-bridge

> Bridge [incident.io](https://incident.io) to [HolmesGPT](https://github.com/robusta-dev/holmesgpt):
> when an incident is declared, ask Holmes to investigate, and post the analysis
> back onto the incident.
>
> Ships with a wire-compatible incident.io mock, so the whole loop runs on a laptop.

## Table of Contents

* [Why](#why)
* [What's in here](#whats-in-here)
* [Quickstart](#quickstart)
* [Configuration](#configuration)
* [Installing HolmesGPT](#installing-holmesgpt)
  * [Models](#models)
  * [Toolsets](#toolsets)
* [Two pipelines](#two-pipelines)
* [holmes-bridge](#holmes-bridge)
    * [Configuration](#configuration)
    * [Triggers and the feedback loop](#triggers-and-the-feedback-loop)
    * [Pointing it at a real incident.io](#pointing-it-at-a-real-incidentio)
* [incidentio-mock](#incidentio-mock)
    * [Covered API surface](#covered-api-surface)
    * [Scenarios](#scenarios)
    * [Control plane](#control-plane)
    * [Webhooks](#webhooks)
* [Development](#development)
* [Architecture](#architecture)

## Why

HolmesGPT investigates alerts and writes up what it found. It ships source plugins for GitHub, Jira, OpsGenie, PagerDuty
and Alertmanager — but **not incident.io**. This repo is that missing integration, plus the fake incident.io you need to
develop it without declaring real incidents.

## What's in here

| Binary            | What it does                                                                           |
|-------------------|----------------------------------------------------------------------------------------|
| `holmes-bridge`   | Receives incident.io webhooks, asks HolmesGPT to investigate, writes the analysis back |
| `incidentio-mock` | A fake incident.io: 37 real API operations, YAML fixtures, signed webhooks             |

Both are wired against **one generated package** (`api/incidentio`), trimmed from
the [official incident.io OpenAPI spec][spec]. The mock implements the server half;
the bridge calls the client half. They cannot drift apart.

[spec]: https://api.incident.io/v1/openapiV3.json

## Quickstart

The loop needs a real HolmesGPT, and Holmes needs a cluster to investigate. You
supply the cluster; `task holmes:install` does the rest.

```bash
kind create cluster --name holmes

cp .env.example .env                       # then put your key in it:
#   OPENROUTER_API_KEY=sk-or-...           # https://openrouter.ai/keys

task holmes:install                        # installs Holmes, wired to OpenRouter
task holmes:status                         # which models and toolsets came up

task demo                                  # mock + bridge + port-forward + broken workload
```

`task demo` also applies `hack/demo-workload.yaml`: a `checkout-api` deployment
that crashloops because the `postgres-primary` Service it needs has no
endpoints. That is what gives Holmes something real to find — the mock's
`checkout-crashloop` scenario describes exactly that incident.

Then, in another shell:

```bash
# Investigate the incident that is already open. This calls a real model, so it
# takes a minute or two and costs a few cents.
INC=$(curl -s http://127.0.0.1:18080/v2/incidents \
  | jq -r '.incidents[] | select(.reference=="TEST-201") | .id')
curl -s -X POST http://127.0.0.1:18081/investigations/$INC | jq -r '.analysis'

# Or declare a new incident and let the webhook drive it
curl -s -X POST http://127.0.0.1:18080/v2/incidents \
  -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"demo-1","name":"Checkout API will not start",
       "visibility":"public","severity_id":"SEV1","incident_status_id":"triage"}' \
  | jq -r '.incident.reference'

# What is actually broken, for comparison with what Holmes concluded
kubectl get pods,endpoints -n checkout-demo
```

A run against `claude-sonnet` takes 60–90 seconds and makes 10–20 tool calls
against the cluster. Tear down with `task demo:workload:delete` and
`task holmes:uninstall`; the kind cluster is yours to delete.

## Configuration

Local settings live in `.env`, which is gitignored. Copy the template and fill
in your OpenRouter key:

```bash
cp .env.example .env
```

Three layers, highest precedence first:

| | File | Tracked | For |
|---|---|---|---|
| 1 | *your shell* | – | One-off overrides: `HOLMES_MODEL=gpt-4o task demo` |
| 2 | `.env` | no | Your keys and local overrides |
| 3 | `.env.default` | yes | Committed defaults everyone shares |

Both `task` and the scripts in `hack/` read these, so `task holmes:install` and
`./hack/install-holmes.sh` behave identically. `.env` is excluded from
`.gitignore` and `.dockerignore`, so a key cannot reach a commit or an image.

### Which way the traffic goes

This decides whether a setting needs a network path in, and it is the only
reason the tunnel exists:

| | Direction | Needs exposing? |
|---|---|---|
| incident.io API | bridge → incident.io | no |
| HolmesGPT | bridge → Holmes | no |
| ntfy | bridge → ntfy | no |
| OpenRouter | Holmes → OpenRouter | no |
| **incident.io webhooks** | **incident.io → bridge** | **yes** — see [holt](#exposing-the-webhook-with-holt) |

`.env` is grouped the same way.

### Ports

One rule: **the local port is `1` + the conventional one**, because 8080 and
8081 are the most contested ports on a development machine.

| Service | Port | Override |
|---|---|---|
| `incidentio-mock` | `18080` | `--http-port` / `HTTP_PORT` / `MOCK_PORT` |
| `holmes-bridge` | `18081` | `--http-port` / `HTTP_PORT` / `BRIDGE_PORT` |
| HolmesGPT (port-forward) | `15050` | `HOLMES_PORT` |

The Helm charts use the same numbers, so there is one port per service and no
second set to remember. If something else already holds a port, the bind error
names it and tells you how to find the process.

The one value you must set is `OPENROUTER_API_KEY`. Everything else has a
working default; see `.env.example` for the full list, including the
`INCIDENTIO_API_KEY` / `WEBHOOK_SECRET` pair you will need when you point the
bridge at your real org.

## Installing HolmesGPT

`task holmes:install` installs the upstream [robusta/holmes][holmes-chart] chart
into whatever cluster `kubectl` points at, using `hack/holmes-values.yaml`.

[holmes-chart]: https://github.com/robusta-dev/holmesgpt/tree/master/helm/holmes

It creates a `holmes-openrouter` Secret from `$OPENROUTER_API_KEY` and wires it
in through LiteLLM's native OpenRouter provider — the `openrouter/` model prefix
— which lets Holmes work out each model's context window on its own. (The
OpenAI-compatible route cannot, and needs `OVERRIDE_MAX_CONTENT_SIZE` set by
hand.) The Secret is re-applied on every run, so rotating a key is just another
`task holmes:install`.

All of these can go in `.env`:

| Variable | Default | Description |
|---|---|---|
| `OPENROUTER_API_KEY` | – | **Required.** From <https://openrouter.ai/keys> |
| `HOLMES_NAMESPACE` | `holmes` | Namespace to install into |
| `HOLMES_RELEASE` | `holmes` | Helm release name |
| `HOLMES_VALUES` | `hack/holmes-values.yaml` | Values file |
| `HOLMES_CHART_VERSION` | latest | Pin a chart version |
| `ALLOW_REMOTE_CLUSTER` | `0` | Permit a non-local kubectl context |

The chart grants Holmes **cluster-wide read access**, so the installer refuses
any context that does not look local (`kind-*`, `minikube`, `docker-desktop`,
`orbstack`, `rancher-desktop`, `colima`) unless you set `ALLOW_REMOTE_CLUSTER=1`.

### Models

`hack/holmes-values.yaml` defines three OpenRouter models. The **key** is what
you pass as `HOLMES_MODEL`, not the `model:` string under it:

```yaml
modelList:
  claude-sonnet:                                     # <- HOLMES_MODEL=claude-sonnet
    api_key: "{{ env.OPENROUTER_API_KEY }}"
    model: openrouter/anthropic/claude-sonnet-4.5
  gpt-4o:
    model: openrouter/openai/gpt-4o
  gemini-pro:
    model: openrouter/google/gemini-2.5-pro
```

Add any model from <https://openrouter.ai/models> with the `openrouter/` prefix,
then re-run `task holmes:install`. The bridge checks at startup that the model
it is configured with is one Holmes actually serves, and logs an error if not —
otherwise the mismatch only surfaces when a real incident is waiting on it.

### Toolsets

Toolsets are what make Holmes an investigator rather than a chatbot: without
them it answers from the prompt alone. The values file enables the ones that
work in a bare kind cluster (`kubernetes/core`, `kubernetes/logs`, `bash`) and
disables the ones that need infrastructure you do not have (`prometheus/metrics`,
`robusta`, `internet`).

`task holmes:status` reports what came up. A handful of *failed* toolsets is
normal — they are integrations like OpenShift or ArgoCD probing for tools that
are not installed. What matters is that `kubernetes/core` and `kubernetes/logs`
are enabled.

Other Holmes tasks: `task holmes:logs`, `task holmes:port-forward`,
`task holmes:uninstall`.

## Two pipelines

The bridge runs one of two flows. They share the investigation engine — the same
concurrency, deduplication, cooldown and prompt structure — and differ in where
the trigger comes from and where the answer goes.

```
serve        incident.io webhook ──▶ HolmesGPT ──▶ incident update on the incident
ntfy serve   Alertmanager webhook ──▶ HolmesGPT ──▶ push notification
```

| | `serve` | `ntfy serve` |
|---|---|---|
| Trigger | incident.io webhook (signed) | Alertmanager webhook (bearer token) |
| Context | incident + its alerts + update feed | alert labels and annotations |
| Output | incident update or timeline item | ntfy notification |
| Needs incident.io | yes | **no** |
| Deduplicates on | incident ID | Alertmanager `groupKey` |
| Exposable with holt | yes | yes |

`ntfy serve` is the shorter path: no incident.io account, no incident declared,
nothing written back. Prometheus fires, HolmesGPT investigates, your phone buzzes
with the reason.

## holmes-bridge

```
incident.io ──webhook──▶ holmes-bridge ──POST /api/chat──▶ HolmesGPT
     ▲                        │
     └────── incident update ─┘
```

1. An incident.io webhook arrives and its signature is verified.
2. The bridge acknowledges immediately (incident.io retries slow endpoints) and investigates in the background.
3. It loads the incident, its alerts and its update feed, and assembles them into a prompt.
4. HolmesGPT investigates against your cluster and returns an analysis.
5. The analysis is posted back as an incident update — which mirrors into the incident's Slack channel.

### Configuration

Every flag has an environment variable.

| Flag                            | Environment                   | Default                   | Description                                           |
|---------------------------------|-------------------------------|---------------------------|-------------------------------------------------------|
| `--incidentio-url`              | `INCIDENTIO_URL`              | `https://api.incident.io` | incident.io API base URL                              |
| `--incidentio-api-key`          | `INCIDENTIO_API_KEY`          | –                         | incident.io API key                                   |
| `--holmes-url`                  | `HOLMES_URL`                  | `http://localhost:15050`   | HolmesGPT server                                      |
| `--holmes-api-key`              | `HOLMES_API_KEY`              | –                         | Bearer token, if Holmes is behind auth                |
| `--holmes-model`                | `HOLMES_MODEL`                | –                         | Model override; empty uses the server default         |
| `--webhook-secret`              | `WEBHOOK_SECRET`              | –                         | incident.io signing secret. Repeatable, for rotation  |
| `--trigger`                     | `TRIGGERS`                    | created + status changed  | Events that start an investigation. Repeatable        |
| `--write-back`                  | `WRITE_BACK`                  | `update`                  | `update`, `timeline`, or `none`                       |
| `--min-severity-rank`           | `MIN_SEVERITY_RANK`           | `0`                       | Skip incidents below this severity rank               |
| `--max-concurrent`              | `MAX_CONCURRENT`              | `2`                       | Simultaneous investigations                           |
| `--cooldown`                    | `COOLDOWN`                    | `30m`                     | Leave an incident alone this long after investigating |
| `--attempts`                    | `ATTEMPTS`                    | `2`                       | Attempts before giving up                             |
| `--investigation-timeout`       | `INVESTIGATION_TIMEOUT`       | `10m`                     | Bound on one investigation                            |
| `--skip-signature-verification` | `SKIP_SIGNATURE_VERIFICATION` | `false`                   | **Local development only**                            |

Investigate a single incident from the command line, without running a server — the fastest way to iterate on the
prompt:

```bash
holmes-bridge investigate INC_ID --dry-run   # prints the analysis, writes nothing
```

### Triggers and the feedback loop

The bridge triggers on `public_incident.incident_created_v2` and
`public_incident.incident_status_updated_v2` by default. Edits and alert events are excluded deliberately: they fire
constantly and rarely mean the picture has changed.

**Writing an analysis back is itself a change, and incident.io emits an event for it.** Left alone, that event
re-triggers the investigation that caused it, which writes back again — a loop that bills an LLM forever. Two things
prevent it:

- A **per-incident cooldown** (`--cooldown`, 30m by default). Any event for an incident inside that window is ignored.
  This is the guarantee.
- Posting an update with no status change emits `incident_updated_v2`, not
  `incident_status_updated_v2`, so it is not a default trigger anyway.

### Push notifications with ntfy

Get the conclusion on your phone instead of watching logs. The bridge POSTs to
ntfy and ntfy never calls back, so this needs no inbound path and nothing to do
with the tunnel settings — it works the same on a laptop behind NAT.

```bash
holmes-bridge serve --ntfy-topic my-incidents --ntfy-token tk_...
```

Most ntfy servers require a token to publish; public ntfy.sh also accepts
anonymous posts. **A token only works against the server that issued it** — a
self-hosted token sent to ntfy.sh is rejected with a 401 that reads like a bad
token rather than the wrong address, so set `--ntfy-server` alongside it.

The bridge warns at startup about both mistakes — a topic with no credentials,
and a token still pointed at the public default — rather than leaving you to
discover them when the first investigation finishes.

| Flag | Environment | Default | Description |
|---|---|---|---|
| `--ntfy-topic` | `NTFY_TOPIC` | – | Topic to publish to. Empty disables notifications |
| `--ntfy-server` | `NTFY_SERVER` | `https://ntfy.sh` | ntfy server |
| `--ntfy-token` | `NTFY_TOKEN` | – | Access token (`tk_...`) |
| `--ntfy-user` / `--ntfy-password` | `NTFY_USER` / `NTFY_PASSWORD` | – | Basic auth, for servers using it |
| `--ntfy-on-failure` | `NTFY_ON_FAILURE` | `true` | Also push when an investigation fails |

What arrives:

```
TEST-201 · Checkout is completely down
The checkout-api deployment cannot start because postgres-primary
has no backing pods.

_14 tool calls. AI-generated: verify before acting._
```

Tapping it opens the incident, via the permalink. A failed investigation is
pushed at high priority with a 🚨 tag, because a bridge that has quietly stopped
working is the thing you most want to hear about.

Three deliberate choices:

- **Only the Summary section is pushed**, not the whole analysis — a lock screen
  is not where you read Evidence and Next Steps. Those stay on the incident.
- **Skipped investigations are silent.** A closed or test incident spends nothing
  and learns nothing; pushing it would be noise.
- **A failed push never fails an investigation.** The analysis is already on the
  incident either way, so a publish error is logged and dropped.

> The topic is also a read secret: anyone who knows it can subscribe to your
> notifications, and incident summaries are not nothing. Use a long random topic,
> or a server that requires auth to subscribe as well as publish.

### Many incidents at once

Each incident is independent work: its own in-flight claim, its own cooldown,
its own HolmesGPT conversation, its own analysis. Two triggers for the *same*
incident collapse into one investigation; two different incidents proceed in
parallel.

`--max-concurrent` (default 2) bounds how many run at once, because each one
costs model spend and puts load on the cluster it is inspecting. Beyond that
they queue.

The queue is bounded by `--queue-timeout` (default 5m), and that bound matters.
A webhook-driven investigation carries a context that is never cancelled, so
without it a storm just accumulates: with 2 slots and ~90s per investigation,
the twentieth incident would wait a quarter of an hour and then spend on an
analysis of something that had long moved on. Instead it is dropped, with a
warning naming the incident. Raise `--max-concurrent` if you want more
throughput; set `--queue-timeout 0` to wait however long it takes.

### Investigating on other events

Anything whose payload references an incident can be a trigger — actions and
follow-ups carry `incident_id`:

```bash
holmes-bridge serve \
  --trigger public_incident.incident_created_v2 \
  --trigger public_incident.incident_status_updated_v2 \
  --trigger public_incident.action_created_v1
```

Two things to know:

- **`--trigger` replaces the defaults, it does not add to them.** List every
  event you want, including the two defaults. The effective set is logged at
  startup.
- **The cooldown will suppress most of them.** Actions and follow-ups arrive
  while an incident is already open — which is exactly when the cooldown from
  the first investigation is running. Adding an action two minutes after the
  incident was declared is skipped, silently apart from a debug line. The bridge
  says so at startup when you configure such a trigger. Lower `--cooldown` if
  you want each one investigated, knowing that also weakens the loop guard.

Alert events (`public_alert.*`) cannot be triggers: `AlertV2` carries no
incident reference, so there is nothing to investigate. Alerts become incidents
via incident.io alert routes.

Set `--cooldown 0` only if you have another mechanism; there is a regression test
(`TestWriteBackDoesNotRetriggerItself`) that fails if the guard is removed.

### Exposing the webhook with holt

**This is the only inbound path the bridge has.** Everything else it talks to —
the incident.io API, HolmesGPT, ntfy — it calls *outward*, so none of them need
exposing. Only incident.io's webhooks arrive unsolicited, which is what the
tunnel is for.

incident.io has to reach the bridge, which is awkward when it runs on a laptop
or in a cluster with no inbound path. [holt](https://github.com/openotters/holt)
solves that the right way round: the bridge dials **out** to a hub and serves
its handler back down that connection.

```bash
holmes-bridge serve --expose --webhook-secret whsec_...
```

It enrolls with the hub from your `~/.holt/config.yaml`, then prints the address
to register:

```
  ┌─ holt tunnel ────────────────────────────────────────────────
  │
  │  Register this URL in incident.io (Settings → Webhooks):
  │
  │      https://holmes-bridge.example.com/webhooks/incidentio
  │
  │  Subscribe it to:
  │      public_incident.incident_created_v2
  │      public_incident.incident_status_updated_v2
  │
  │  Then copy the signing secret into --webhook-secret.
  │
  └──────────────────────────────────────────────────────────────
```

| Flag | Environment | Default | Description |
|---|---|---|---|
| `--expose` | `EXPOSE` | `false` | Publish through holt and print the URL |
| `--expose-peer` | `EXPOSE_PEER` | `holmes-bridge` | Peer id, which doubles as the hostname |
| `--expose-profile` | `EXPOSE_PROFILE` | file default | Profile from `~/.holt/config.yaml` |
| `--expose-config` | `EXPOSE_CONFIG` | `~/.holt/config.yaml` | Config file |
| `--expose-admin-url` | `EXPOSE_ADMIN_URL` | profile's | Enroll against a specific hub |

Two things worth knowing:

- **The hub must route by subdomain.** incident.io sends a fixed set of headers
  and cannot add one, so a header-routed hub can never deliver to this peer. The
  bridge checks at startup and refuses with that explanation rather than letting
  every delivery silently miss. Run the hub with `--proxy-routing subdomain`
  (or `both`) and `--proxy-domain`.
- **The peer id is the hostname**, so it is fixed at `holmes-bridge` by default.
  A generated id would change on every restart and you would be re-registering
  the webhook each time. For an identity that survives redeploys, set
  `HOLT_TOKEN` and holt skips enrolment entirely.

Test the endpoint without waiting for a real incident:

```bash
task webhook:send -- https://holmes-bridge.example.com
```

That signs the delivery the way incident.io does, so a 401 back means your
`--webhook-secret` does not match — not that you got the curl wrong.

By default the tunnel carries **only** the signature-verified webhook route.
`POST /investigations/{id}` takes no credential, so publishing it would let any
caller start investigations; `--expose-all` opts into that for development, with
a warning.

The local listener stays up alongside the tunnel — probes and `/metrics` are
scraped from inside, not through it. Signature verification is unaffected: a
forged delivery through the tunnel is still rejected with a 401.

### Pointing it at a real incident.io

You have incident.io and Slack admin, so:

1. **API key** — Settings → API keys. It needs *View incidents*, *Edit incidents*
   and *Create incident updates*. Set `INCIDENTIO_API_KEY`.
2. **Webhook** — Settings → Webhooks. Point it at
   `https://your-bridge/webhooks/incidentio`, subscribe to *Incident created* and *Incident status updated*, and copy
   the signing secret into `WEBHOOK_SECRET`.
3. **Slack** — nothing to configure. Incident updates already mirror into the incident channel, so the analysis lands
   where responders are.

Start with `--write-back none` against production: the bridge runs the full investigation and logs the analysis without
touching any incident. Move to
`update` once the answers look useful.

## incidentio-mock

A fake incident.io that speaks the real wire format. Point any incident.io client at it — including this bridge — and it
cannot tell the difference for the operations it covers.

```bash
incidentio-mock serve \
  --webhook-secret whsec_... \
  --webhook "name=bridge,url=http://localhost:18081/webhooks/incidentio"
```

| Flag               | Environment      | Default                              | Description                                               |
|--------------------|------------------|--------------------------------------|-----------------------------------------------------------|
| `--fixtures-dir`   | `FIXTURES_DIR`   | `fixtures/scenarios`                 | Directory of scenario YAML                                |
| `--scenario`       | `SCENARIO`       | sole scenario, or `checkout-latency` | Scenario to load at boot                                  |
| `--api-key`        | `API_KEYS`       | –                                    | Accepted bearer token. Repeatable. Empty accepts anything |
| `--webhook-secret` | `WEBHOOK_SECRET` | a dev default                        | Secret used to sign outbound webhooks                     |
| `--webhook`        | `WEBHOOKS`       | –                                    | Subscriber spec. Repeatable                               |

### Covered API surface

37 operations, generated from the real spec:

| Group                                                            | Operations                                  |
|------------------------------------------------------------------|---------------------------------------------|
| Incidents V2                                                     | list, show, create, edit                    |
| Incident Updates V2                                              | list, create                                |
| Incident Timeline Items V2                                       | list, create, update                        |
| Alerts V2                                                        | list, show, resolve, list incident alerts   |
| Alert Events V2                                                  | create via HTTP source (with deduplication) |
| Actions V3                                                       | list, show, create, update, delete          |
| Follow-ups V3                                                    | list, show, create, update, delete          |
| Catalog V2                                                       | list/show types, list/show entries          |
| Severities V1, Incident Statuses V1, Incident Roles V2, Users V2 | list, show                                  |
| Utilities V1                                                     | identity                                    |

Cursor pagination, `field[operator]=value` filters, idempotency keys and incident.io's error envelope all behave as they
do upstream. Anything not covered returns a 404 in the same envelope.

To cover more, add the path to `KEEP` in `hack/trim-spec.py`, run
`task spec:refresh`, and implement the new handler.

### Scenarios

A scenario is one YAML file describing a whole organisation. The schema is deliberately *not* the wire schema — an
incident is a name, a status and a severity, and the loader expands that into the full object:

```yaml
name: checkout-latency

severities:
  - { id: SEV1, name: Critical, rank: 3 }
statuses:
  - { id: triage, name: Triage, category: triage, rank: 1 }

alerts:
  - id: ALT-LATENCY
    title: "checkout-api p99 latency above 2s for 10m"
    status: firing
    created_at: "-42m"          # relative to load time, so it stays fresh

incidents:
  - id: INC-CHECKOUT
    reference: TEST-142
    name: Checkout latency spike
    status: investigating
    severity: SEV1
    created_at: "-40m"
    roles: { lead: USR-MERLIN }
    alerts: [ ALT-LATENCY ]
    updates:
      - { message: "Raising to SEV1.", status: investigating, created_at: "-30m" }
```

Timestamps are RFC3339 or a relative offset (`-15m`, `-2h30m`, `-3d`), resolved when the fixture loads — a scenario
written months ago still presents a forty-minute-old incident. Cross-references are validated at boot, so a typo'd
severity fails loudly instead of producing a half-populated incident.

`fixtures/scenarios/checkout-latency.yaml` ships as a worked example: a live SEV1 with three firing alerts, two
responders, an update feed and a suspicious deploy on the timeline.

### Control plane

Under `/_mock`, a prefix the real API does not use:

| Endpoint                            | Purpose                                               |
|-------------------------------------|-------------------------------------------------------|
| `GET /_mock/status`                 | Active scenario and record counts                     |
| `GET /_mock/scenarios`              | Available scenarios                                   |
| `POST /_mock/reset`                 | Reload the active scenario, or `{"scenario":"other"}` |
| `POST /_mock/webhooks/fire`         | Emit any event on demand                              |
| `GET /_mock/webhooks/deliveries`    | Delivery attempt log                                  |
| `GET /_mock/webhooks/subscriptions` | Configured subscribers and the event vocabulary       |

`POST /_mock/reset` between test cases; `POST /_mock/webhooks/fire` to drive a consumer without provoking the state
change that would normally cause the event:

```bash
curl -X POST http://localhost:18080/_mock/webhooks/fire \
  -H 'Content-Type: application/json' \
  -d '{"event_type":"public_incident.incident_created_v2","resource_id":"INC-CHECKOUT"}'
```

### Webhooks

The mock signs deliveries exactly as incident.io does — the [Standard Webhooks](https://www.standardwebhooks.com/)
scheme, as implemented by Svix:

```
webhook-id:         msg_01J...
webhook-timestamp:  1788716229
webhook-signature:  v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=
```

The signature is `HMAC-SHA256(base64_decode(secret), "{id}.{timestamp}.{body}")`. Note the **base64 decode** — keying on
the secret's literal bytes round-trips against itself but is rejected by every real verifier. The canonical Standard
Webhooks test vector is pinned in `signature_test.go` to keep that honest.

Deliveries retry with exponential backoff on 5xx and transport failures, and are not retried on 4xx. Multiple secrets
can be configured for rotation.

## Development

```bash
task test                  # go test ./...
task test:race             # under the race detector
task build                 # both binaries into ./bin
task generate              # regenerate the OpenAPI server and client
task spec:refresh          # re-trim the spec from upstream, then regenerate
task golangci:lint         # lint

task holmes:install        # install HolmesGPT into the current cluster
task holmes:status         # models and toolsets
task holmes:logs           # tail Holmes
task holmes:port-forward   # Holmes on localhost:15050
task holmes:uninstall

task demo                  # the whole loop against your cluster
task demo:workload         # just the broken workload
task demo:workload:delete
```

`task spec:refresh` re-downloads the live incident.io spec and re-trims it. Review the diff to
`api/incidentio/openapi.yaml` before committing — a changed wire type is exactly what you want to notice.

> The `goreleaser` and `license` includes are commented out of `Taskfile.yaml`:
> both resolve the project name through `gh repo view` in an eager `sh:` var,
> which Task evaluates at parse time, so including them before this repo has a
> GitHub origin breaks *every* task. Restore both lines once it is pushed.

### Commits

[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):
`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`.

## Architecture

Clean architecture, in the shape of `sshark-api`:

```
api/
  incidentio/   generated wire contract — server (mock) and client (bridge)
    v1/         the mock's handler implementations
  bridge/v1/    the bridge's webhook receiver and manual trigger
  control/v1/   the mock's /_mock control plane
  private/      liveness and readiness, shared by both binaries
cmd/
  incidentio-mock/   kong CLI, globals, serve
  holmes-bridge/     kong CLI, globals, serve + investigate
internal/
  domain/       ports and entities (incidents, alerts, catalog, webhooks)
  infra/        adapters (memory store, YAML fixtures, HTTP clients, delivery)
  app/          orchestration (investigate)
  globals/      HTTP and metric server flags
  metrics/      OpenTelemetry instruments
  middleware/   auth, error envelope, metrics
hack/           spec trimmer, stub HolmesGPT, demo script
fixtures/       scenario YAML
test/e2e/       the whole loop over real HTTP
```

The domain packages alias the generated wire types rather than defining a parallel model. For a fake, the spec *is* the
source of truth: a second set of structs plus mappers would double the code and give the wire shape a second place to
drift. The repository interfaces still live in the domain, so a Postgres adapter would replace the in-memory one without
touching a handler.

### Observability

Both binaries export OpenTelemetry metrics on `/metrics` (Prometheus exporter):
HTTP golden signals, webhook emission and delivery, webhook receipt and rejection, investigation counts and duration,
and HolmesGPT call latency.

`/liveness` reports process health only. `/readiness` on the bridge additionally checks HolmesGPT — with it down, the
bridge can accept a webhook but cannot do the one thing it exists for.
