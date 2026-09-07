// Package metrics holds the OpenTelemetry instruments both binaries report.
package metrics

import (
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/merlindorin/holmes-bridge"

// Metrics holds every instrument the two services record against.
//
// One struct covers both binaries: the mock leaves the bridge's instruments
// untouched and vice versa, which is cheaper than maintaining two near-identical
// constructors.
type Metrics struct {
	// HTTP golden signals, recorded by the metrics middleware.
	HTTPRequests metric.Int64Counter
	HTTPDuration metric.Float64Histogram
	HTTPInFlight metric.Int64UpDownCounter

	// Webhooks emitted by the mock.
	WebhooksEmitted   metric.Int64Counter
	WebhooksDelivered metric.Int64Counter
	WebhooksFailed    metric.Int64Counter

	// Webhooks received by the bridge.
	WebhooksReceived metric.Int64Counter
	WebhooksRejected metric.Int64Counter

	// Investigations run by the bridge.
	InvestigationsStarted   metric.Int64Counter
	InvestigationsSucceeded metric.Int64Counter
	InvestigationsFailed    metric.Int64Counter
	InvestigationDuration   metric.Float64Histogram
	InvestigationsInFlight  metric.Int64UpDownCounter

	// Calls out to HolmesGPT and incident.io.
	HolmesRequests    metric.Int64Counter
	HolmesDuration    metric.Float64Histogram
	IncidentIORequest metric.Int64Counter
}

// New builds every instrument against the global meter provider.
func New() (*Metrics, error) {
	meter := otel.Meter(meterName)

	var (
		m   Metrics
		err error
	)

	// Each instrument is built through a closure that latches the first error,
	// so a failure surfaces once rather than after twenty repetitions of the
	// same if-err-return.
	counter := func(name, desc string) metric.Int64Counter {
		c, cErr := meter.Int64Counter(name, metric.WithDescription(desc))
		if cErr != nil && err == nil {
			err = fmt.Errorf("failed to create counter %s: %w", name, cErr)
		}

		return c
	}

	updown := func(name, desc string) metric.Int64UpDownCounter {
		c, cErr := meter.Int64UpDownCounter(name, metric.WithDescription(desc))
		if cErr != nil && err == nil {
			err = fmt.Errorf("failed to create up-down counter %s: %w", name, cErr)
		}

		return c
	}

	histogram := func(name, desc string) metric.Float64Histogram {
		h, hErr := meter.Float64Histogram(name,
			metric.WithDescription(desc), metric.WithUnit("s"))
		if hErr != nil && err == nil {
			err = fmt.Errorf("failed to create histogram %s: %w", name, hErr)
		}

		return h
	}

	m.HTTPRequests = counter("http.server.requests", "HTTP requests served, by route and status")
	m.HTTPDuration = histogram("http.server.duration", "HTTP request duration")
	m.HTTPInFlight = updown("http.server.in_flight", "HTTP requests currently being served")

	m.WebhooksEmitted = counter("webhooks.emitted", "Webhook events queued for delivery")
	m.WebhooksDelivered = counter("webhooks.delivered", "Webhook deliveries accepted by a subscriber")
	m.WebhooksFailed = counter("webhooks.failed", "Webhook deliveries abandoned after retries")

	m.WebhooksReceived = counter("webhooks.received", "Webhook deliveries accepted by the bridge")
	m.WebhooksRejected = counter("webhooks.rejected", "Webhook deliveries rejected by the bridge")

	m.InvestigationsStarted = counter("investigations.started", "Investigations dispatched to HolmesGPT")
	m.InvestigationsSucceeded = counter("investigations.succeeded", "Investigations written back to incident.io")
	m.InvestigationsFailed = counter("investigations.failed", "Investigations that could not be completed")
	m.InvestigationDuration = histogram("investigations.duration", "End-to-end investigation duration")
	m.InvestigationsInFlight = updown("investigations.in_flight", "Investigations currently running")

	m.HolmesRequests = counter("holmes.requests", "Calls made to the HolmesGPT API")
	m.HolmesDuration = histogram("holmes.duration", "HolmesGPT request duration")
	m.IncidentIORequest = counter("incidentio.requests", "Calls made to the incident.io API")

	if err != nil {
		return nil, err
	}

	return &m, nil
}
