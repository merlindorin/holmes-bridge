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

Three things, all set up in the incident.io dashboard.

**An API key**, from Settings → API keys, allowed to:

- view incidents
- edit incidents
- create incident updates

The bridge verifies the key on startup and names any permission that is
missing, so a key that is short one fails loudly rather than during your first
real incident.

**A webhook**, from Settings → Webhooks, pointed at the bridge's
`/webhooks/incidentio` route and subscribed to:

- `public_incident.incident_created_v2`
- `public_incident.incident_status_updated_v2`

That is "an incident opened" and "an incident changed status". Edit and alert
events are deliberately left out: they fire constantly and rarely mean the
picture has changed.

**The signing secret** that incident.io shows when the webhook is created.
Every delivery is checked against it, and the bridge will not start without
one — an unverified webhook endpoint is an open invitation to spend your model
budget.

If the bridge sits somewhere incident.io cannot reach, it can dial out and
publish only that one route through a reverse tunnel, printing the URL to
register at startup.

### Where the analysis goes

Three choices:

| Mode | What it does |
|------|--------------|
| **update** | Posts to the incident's update feed, which mirrors into its Slack channel. The useful default during a live incident. |
| **timeline** | Pins the analysis to the incident timeline instead. Quieter, and better suited to retrospective work. |
| **none** | Investigates and logs the result without touching the incident. |

Start on **none** against a real org. Read a few analyses, decide whether you
trust them, then let the bridge post.

## Running it for real

There's a Helm chart in `helm/holmes-bridge`. Point it at your Holmes, give it
your incident.io credentials, and it deploys the bridge — with HolmesGPT
alongside as a subchart, unless you already run one.

The chart wants the three values above, plus a key for whichever model
provider Holmes uses.

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
