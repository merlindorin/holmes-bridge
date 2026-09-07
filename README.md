# holmes-bridge

> Something breaks. [HolmesGPT](https://github.com/robusta-dev/holmesgpt) looks
> at your cluster, works out why, and this bridge puts the answer where you will
> actually see it.

Holmes ships integrations for GitHub, Jira, OpsGenie, PagerDuty and Alertmanager
— but not for [incident.io](https://incident.io). This is that missing piece,
plus a fake incident.io so you can build against it without declaring real
incidents.

## How it works

One engine, two ways in and two ways out. You pick a **connector**.

```
                    ┌──────────────────────────────┐
                    │          HolmesGPT           │
                    │  reads the live cluster and  │
                    │   says what actually broke   │
                    └───────────┬────┬─────────────┘
                                │    │
                  "what broke?" ▲    ▼ analysis
                                │    │
                    ┌───────────┴────┴─────────────┐
                    │        holmes-bridge         │
                    └────▲────────────────────▲────┘
                         │                    │
               incident.io connector   ntfy connector
```

### incident.io connector

An incident is declared. The bridge reads it, investigates, and writes the
analysis back onto the incident — so the answer is waiting in the channel where
everyone is already looking.

```
  incident declared   ──▶  bridge  ──▶  HolmesGPT  ──▶  update on the incident
  signed webhook
```

### ntfy connector

Alertmanager fires. The bridge investigates and pushes the conclusion to your
phone. No incident.io account, no incident declared, nothing written back.

```
  Alertmanager alert  ──▶  bridge  ──▶  HolmesGPT  ──▶  push to your phone
  bearer token
```

### Side by side

|                               | incident.io connector                     | ntfy connector                     |
|-------------------------------|-------------------------------------------|------------------------------------|
| **Triggered by**              | an incident opening or changing status    | an Alertmanager alert              |
| **Holmes reads**              | the incident, its alerts, its update feed | the alert's labels and annotations |
| **Answer lands on**           | the incident itself                       | your phone                         |
| **Caller proves itself with** | a signed webhook                          | a bearer token                     |
| **Deduplicates on**           | the incident                              | Alertmanager's alert group         |
| **Needs incident.io**         | yes                                       | no                                 |

Both share the same engine underneath: the same concurrency budget, the same
cooldown so an investigation cannot retrigger itself, the same prompt structure.
They differ only in what they read and where the answer goes.

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

## Running it for real

There's a Helm chart in `helm/holmes-bridge`. Choose the connector, point it at
your Holmes, give it credentials, and it deploys the matching pipeline.

Two things worth knowing before you point it at a real org:

- **Start read-only.** The bridge can be told to investigate and print without
  writing anything back. Do that first, read a few analyses, then let it post.
- **Nothing needs to reach you.** If the bridge sits somewhere incident.io
  cannot call — a laptop, a private cluster — it can dial out and publish just
  its webhook endpoint through a reverse tunnel.

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
