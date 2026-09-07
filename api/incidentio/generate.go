// Package incidentio is the incident.io wire contract, generated from a trimmed
// copy of the official OpenAPI specification.
//
// It carries both halves of the contract: the server interface the mock
// implements, and the client the bridge calls a real incident.io with. Sharing
// one generated package is what guarantees the two cannot drift apart.
package incidentio

//go:generate go tool oapi-codegen -config apigen.yaml openapi.yaml
