package health

import (
	"strings"

	"github.com/EffNine/conductor/internal/database"
)

// DBStatusPersistence persists model reachability via the gateway SQLite/Postgres DB.
type DBStatusPersistence struct {
	db *database.Database
}

// NewDBStatusPersistence wraps a database connection as StatusPersistence.
func NewDBStatusPersistence(db *database.Database) *DBStatusPersistence {
	if db == nil {
		return nil
	}
	return &DBStatusPersistence{db: db}
}

// UpsertStatus implements StatusPersistence.
func (p *DBStatusPersistence) UpsertStatus(st ModelStatus) error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.UpsertModelStatus(&database.ModelStatusRecord{
		ModelID:          st.ModelID,
		Provider:         st.Provider,
		ProviderModelID:  st.ProviderModelID,
		Reachable:        st.Reachable,
		LatencyMs:        st.LatencyMs,
		LastError:        st.LastError,
		CheckedAt:        st.CheckedAt,
		ConsecutiveFails: st.ConsecutiveFails,
	})
}

// SaveFilterState implements StatusPersistence.
func (p *DBStatusPersistence) SaveFilterState(allReady bool, readyProviders []string) error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.SaveModelProbeMeta(allReady, readyProviders)
}

// RestoreModelStatusStore loads persisted probe results into the in-memory store.
// Returns the number of restored model rows.
func RestoreModelStatusStore(store *ModelStatusStore, db *database.Database) (int, error) {
	if store == nil || db == nil {
		return 0, nil
	}
	rows, err := db.LoadModelStatuses()
	if err != nil {
		return 0, err
	}
	meta, err := db.LoadModelProbeMeta()
	if err != nil {
		return 0, err
	}

	statuses := make([]ModelStatus, 0, len(rows))
	for _, row := range rows {
		statuses = append(statuses, ModelStatus{
			ModelID:          row.ModelID,
			Provider:         row.Provider,
			ProviderModelID:  row.ProviderModelID,
			Reachable:        row.Reachable,
			LatencyMs:        row.LatencyMs,
			LastError:        row.LastError,
			CheckedAt:        row.CheckedAt,
			ConsecutiveFails: row.ConsecutiveFails,
		})
	}

	allReady := false
	var readyProviders []string
	if meta != nil {
		allReady = meta.AllProvidersReady
		if meta.ReadyProviders != "" {
			readyProviders = strings.Split(meta.ReadyProviders, ",")
		}
	}
	store.Restore(statuses, allReady, readyProviders)
	return len(statuses), nil
}
