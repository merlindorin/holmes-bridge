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

## Running it for real

There's a Helm chart in `helm/holmes-bridge`. Point it at your Holmes, give it
your incident.io credentials, and it deploys the bridge — with HolmesGPT
alongside as a subchart, unless you already run one.

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
