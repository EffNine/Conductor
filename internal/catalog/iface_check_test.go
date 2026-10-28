package catalog_test

import (
	"testing"

	"github.com/EffNine/conductor/internal/catalog"
	"github.com/EffNine/conductor/internal/health"
)

func TestModelStatusStore_SatisfiesRoutingReachabilityFilter(t *testing.T) {
	// Compile-time assertion: ModelStatusStore implements the catalog's
	// RoutingReachabilityFilter so ListReachable uses the routing-aware view.
	var _ catalog.RoutingReachabilityFilter = (*health.ModelStatusStore)(nil)
}
