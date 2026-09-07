package memory

import "github.com/merlindorin/holmes-bridge/internal/domain/incidents"

// paginate applies incident.io's cursor pagination to an ID-ordered slice.
//
// The `after` cursor is the ID of the last record the caller already has, so a
// page is "the next page_size records whose ID sorts above it". The returned
// cursor is empty on the final page, which is how clients know to stop.
//
//nolint:nonamedreturns // naming the results documents which cursor is which
func paginate[T any](items []T, key func(T) string, p incidents.Page) (page []T, after string) {
	p = p.Normalise()

	start := 0

	if p.After != "" {
		// Records are ID-ordered, so the first ID above the cursor starts the
		// page. An unknown cursor yields an empty page rather than an error,
		// matching how the real API treats a cursor whose record was deleted.
		start = len(items)

		for i, item := range items {
			if key(item) > p.After {
				start = i
				break
			}
		}
	}

	end := min(start+p.PageSize, len(items))

	page = items[start:end]

	// Only advertise a cursor when there is genuinely another page behind it.
	if end < len(items) && len(page) > 0 {
		after = key(page[len(page)-1])
	}

	return page, after
}
