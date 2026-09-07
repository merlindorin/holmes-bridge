package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/merlindorin/holmes-bridge/api/incidentio"
	"github.com/merlindorin/holmes-bridge/internal/api"
	"github.com/merlindorin/holmes-bridge/internal/domain/catalog"
)

func (s *Server) CatalogV2ListTypes(c *gin.Context) {
	types, err := s.catalog.ListTypes(c.Request.Context())
	if err != nil {
		_ = c.Error(api.Internal("failed to list catalog types"))
		return
	}

	c.JSON(http.StatusOK, incidentio.CatalogListTypesResultV2{CatalogTypes: types})
}

func (s *Server) CatalogV2ShowType(c *gin.Context, id string) {
	catalogType, err := s.catalog.GetType(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "catalog type", id, err, catalog.ErrTypeNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.CatalogShowTypeResultV2{CatalogType: catalogType})
}

func (s *Server) CatalogV2ListEntries(c *gin.Context, params incidentio.CatalogV2ListEntriesParams) {
	ctx := c.Request.Context()

	// The response embeds the type, so an unknown one is a 404 rather than an
	// empty list: the caller asked about something that does not exist.
	catalogType, err := s.catalog.GetType(ctx, params.CatalogTypeId)
	if err != nil {
		s.notFound(c, "catalog type", params.CatalogTypeId, err, catalog.ErrTypeNotFound)
		return
	}

	p := page(params.After, params.PageSize)

	entries, after, _, err := s.catalog.ListEntries(ctx,
		catalog.EntryFilter{CatalogTypeID: params.CatalogTypeId}, p)
	if err != nil {
		_ = c.Error(api.Internal("failed to list catalog entries"))
		return
	}

	c.JSON(http.StatusOK, incidentio.CatalogListEntriesResultV2{
		CatalogType:    catalogType,
		CatalogEntries: entries,
		PaginationMeta: metaV2(p, after),
	})
}

func (s *Server) CatalogV2ShowEntry(c *gin.Context, id string) {
	entry, err := s.catalog.GetEntry(c.Request.Context(), id)
	if err != nil {
		s.notFound(c, "catalog entry", id, err, catalog.ErrEntryNotFound)
		return
	}

	c.JSON(http.StatusOK, incidentio.CatalogShowEntryResultV2{CatalogEntry: entry})
}
