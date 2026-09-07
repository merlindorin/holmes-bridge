// Package catalog holds the ports backing the mock's catalog endpoints.
//
// HolmesGPT leans on the catalog to work out who owns a failing service, so the
// mock serves it even though the bridge never writes to it.
package catalog

import (
	"context"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

type (
	// Type is a catalog type definition, such as "Service" or "Team".
	Type = incidentio.CatalogTypeV2
	// Entry is one record of a catalog type.
	Entry = incidentio.CatalogEntryV2
)

// EntryFilter narrows an entry list query to a single catalog type.
type EntryFilter struct {
	CatalogTypeID string
	Identifier    string
}

// Repository serves catalog types and entries. The mock treats both as
// read-only fixture data.
type Repository interface {
	ListTypes(ctx context.Context) ([]Type, error)
	GetType(ctx context.Context, id string) (Type, error)
	ListEntries(ctx context.Context, f EntryFilter, p incidents.Page) (items []Entry, after string, total int, err error)
	GetEntry(ctx context.Context, id string) (Entry, error)
}
