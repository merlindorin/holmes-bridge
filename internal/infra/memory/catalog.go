package memory

import (
	"context"

	"github.com/merlindorin/holmes-bridge/internal/domain/catalog"
	"github.com/merlindorin/holmes-bridge/internal/domain/incidents"
)

// CatalogRepository serves catalog types and entries.
type CatalogRepository struct{ store *Store }

// NewCatalogRepository adapts a Store to the catalog domain port.
func NewCatalogRepository(s *Store) *CatalogRepository { return &CatalogRepository{store: s} }

var _ catalog.Repository = (*CatalogRepository)(nil)

func (r *CatalogRepository) ListTypes(context.Context) ([]catalog.Type, error) {
	return r.store.catalogTypes.all(), nil
}

func (r *CatalogRepository) GetType(_ context.Context, id string) (catalog.Type, error) {
	t, ok := r.store.catalogTypes.get(id)
	if !ok {
		return catalog.Type{}, catalog.ErrTypeNotFound
	}

	return t, nil
}

func (r *CatalogRepository) ListEntries(
	_ context.Context, f catalog.EntryFilter, p incidents.Page,
) ([]catalog.Entry, string, int, error) {
	all := r.store.catalogEntries.all()

	matched := make([]catalog.Entry, 0, len(all))

	for _, e := range all {
		if f.CatalogTypeID != "" && e.CatalogTypeId != f.CatalogTypeID {
			continue
		}

		if f.Identifier != "" && e.Name != f.Identifier &&
			(e.ExternalId == nil || *e.ExternalId != f.Identifier) {
			continue
		}

		matched = append(matched, e)
	}

	page, after := paginate(matched, func(e catalog.Entry) string { return e.Id }, p)

	return page, after, len(matched), nil
}

func (r *CatalogRepository) GetEntry(_ context.Context, id string) (catalog.Entry, error) {
	e, ok := r.store.catalogEntries.get(id)
	if !ok {
		return catalog.Entry{}, catalog.ErrEntryNotFound
	}

	return e, nil
}
