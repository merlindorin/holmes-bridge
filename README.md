# holmes-bridge

> Something breaks. [HolmesGPT](https://github.com/robusta-dev/holmesgpt) looks
> at your cluster, works out why, and this bridge puts the answer where you will
> actually see it.

Holmes ships integrations for GitHub, Jira, OpsGenie, PagerDuty and Alertmanager
— but not for [incident.io](https://incident.io). This is that missing piece,
plus a fake incident.io so you can build against it without declaring real
incidents.

## How it works

```
                    ┌──────────────────────────────┐
                    │          HolmesGPT           │
                    │  reads the live cluster and  │
                    │   says what actually broke   │
                    └───────────┬────┬─────────────┘
                                │    │
                  "what broke?" ▲    ▼ analysis
                                │    │
   incident         ┌───────────┴────┴─────────────┐        analysis posted
   declared   ─────▶│        holmes-bridge         │─────▶  on the incident
                    └──────────────────────────────┘
  signed webhook                                          where everyone is
                                                          already looking
```

An incident is declared. The bridge reads it — the incident, its alerts, its
update feed — asks HolmesGPT what broke, and writes the answer back onto the
incident. Nobody has to go looking anywhere else.

It paces itself, because an incident fires events constantly: one investigation
at a time per incident, a bounded number at once, and a cooldown afterwards so
the bridge cannot react to its own write-back.

## Try it

Holmes needs a real cluster to investigate. You bring the cluster; the tasks do
the rest.

```bash
kind create cluster --name holmes

cp .env.example .env      # then add your OpenRouter key
task holmes:install       # HolmesGPT into the cluster
task demo                 # mock + bridge + a genuinely broken workload
```

The demo deploys a service that crashloops because the database it needs has no
endpoints — real breakage, so Holmes has something real to find. It takes a
minute or two per investigation and calls a paid model.

`task --list` shows the rest. Every command explains itself with `--help`.

## Connecting incident.io

Each item lists its environment variable and its Helm value.

In incident.io:

- **API key** — Settings → API keys, allowed to view incidents, edit incidents
  and create incident updates. The bridge checks all three at startup and names
  any that are missing.
  `INCIDENTIO_API_KEY` · `secrets.incidentioApiKey` — and `INCIDENTIO_URL` ·
  `incidentio.url` to point somewhere other than api.incident.io
- **Webhook** — Settings → Webhooks, pointed at `/webhooks/incidentio`.
- **Subscriptions** — `public_incident.incident_created_v2` and
  `public_incident.incident_status_updated_v2`. Edit and alert events are left
  out on purpose: they fire constantly and rarely mean the picture changed.
  `TRIGGERS` · `incidentio.triggers`
- **Signing secret** — shown once, when the webhook is created. Every delivery
  is verified against it, and the bridge will not start without one.
  `WEBHOOK_SECRET` · `secrets.webhookSecret`

In the bridge:

- **HolmesGPT** — where it runs, and which model key from its `modelList` to
  investigate with.
  `HOLMES_URL` · `holmesUrl` — `HOLMES_MODEL` · `holmesModel`
- **A model provider key** — read by HolmesGPT, not the bridge.
  `secrets.openrouterApiKey`
- **Write-back** — `update` posts to the incident's update feed and its Slack
  channel, `timeline` pins the analysis to the timeline, `none` investigates and
  logs nothing back. Start on `none` against a real org.
  `WRITE_BACK` · `investigation.writeBack`
- **A way in** — an ingress, or the built-in reverse tunnel when incident.io
  cannot reach the bridge. The tunnel prints the URL to register at startup.
  `ingress.enabled` — or `EXPOSE` · `expose.enabled` with `secrets.holtToken`

Pacing has working defaults: `MAX_CONCURRENT`, `COOLDOWN`, `ATTEMPTS`,
`INVESTIGATION_TIMEOUT`, `QUEUE_TIMEOUT` and `MIN_SEVERITY_RANK`, all under
`investigation.*` in the chart.

## Shaping the answer

A system prompt decides what an analysis looks like — the sections, the length,
the rule that every claim cites a tool call and every returned URL is linked.
The built-in one is what you get by default.

To change it, set a Go `text/template`:

`SYSTEM_PROMPT` · `investigation.systemPrompt`

The template receives `.Source`, which is `incident` when a webhook or a manual
trigger started it, and `chat` when someone typed a question. A template that
will not parse or execute falls back to the default rather than failing the
investigation — a bad override should change the wording, not take the bridge
down mid-incident.

Every analysis ends with a numbered **Sources** list: the browsable URLs of
whatever the investigation actually opened — a Grafana dashboard, a trace, a log query.
HolmesGPT computes those but never shows them to the model, so the model cannot
cite them and must not invent them; the bridge recovers them from the tool
results instead, where they are known to be real. Each entry is also emitted as
a Markdown link definition, so a bare `[0]` anywhere in the prose resolves to
that URL, and each claim a source backs is prefixed with its marker:

```
- [0] p99 latency crossed 2s at 14:03

**Sources**

- [0] https://grafana.example.com/dashboards?query=checkout
```

The markers come from a second, tool-free model call: on the first pass the
model has not been shown any URL, so it cannot know a source list exists, let
alone how it is numbered. Turn that pass off with `CITE_SOURCES` ·
`investigation.citeSources` if the extra call is not worth it.

### Asking a question directly

`POST /chat` with `{"ask": "..."}` answers a free-form question through that
same prompt. The point is the prompt: asking HolmesGPT directly gets its stock
behaviour, while going through the bridge applies the structure and rules an
investigation gets, so the two answers are comparable.

Like the manual trigger, it takes no credential and is never published through
the tunnel — it spends on a model, so it stays on the local listener.

## Running it for real

There's a Helm chart in `helm/holmes-bridge`. Point it at your Holmes, give it
your incident.io credentials, and it deploys the bridge — with HolmesGPT
alongside as a subchart, unless you already run one. Everything above is a
chart value.

## The mock

A fake incident.io that speaks the real wire format: 37 operations, cursor
pagination, filters, idempotency keys, the same error envelope, and webhooks
signed the same way. Point any incident.io client at it and, for what it covers,
it cannot tell the difference.

An org is one YAML file — incidents, alerts, severities, people. The demo ships
one describing exactly the breakage running in the cluster, so the incident and
the reality agree and the investigation has real evidence to find.

## Development

```bash
task build     # both binaries
task test      # the suite
task lint      # golangci-lint
```

The incident.io API surface is generated, never hand-written: `task spec:refresh`
re-trims the upstream spec and regenerates. Review the diff — a changed wire type
is the whole point of tracking it.

Design decisions, the reasoning behind them, and the layout of the code are in
[AGENT.md](AGENT.md).

## Licence

[MIT](LICENSE.md)
