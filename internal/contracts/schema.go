// Package contracts defines the stable internal contracts shared across every
// subsystem of Conductor. All types in this package are immutable after Build()
// and carry schema-version metadata for forward compatibility.
package contracts

import (
	"fmt"
	"time"
)

// CurrentSchemaVersion is the schema version for all contracts in this package.
const CurrentSchemaVersion = "v2.2-c"

// SchemaMetadata is embedded in every contract to enable version-aware
// deserialization and forward-compatibility checks.
type SchemaMetadata struct {
	SchemaVersion string    `json:"schema_version"`
	ContractID    string    `json:"contract_id"`
	Timestamp     time.Time `json:"timestamp"`
}

// NewSchemaMetadata creates schema metadata for a contract.
func NewSchemaMetadata(contractID string) SchemaMetadata {
	return SchemaMetadata{
		SchemaVersion: CurrentSchemaVersion,
		ContractID:    contractID,
		Timestamp:     time.Now().UTC(),
	}
}

// ValidateSchema checks that the schema version is current and the contract
// ID is non-empty. Returns an error if validation fails.
func (m SchemaMetadata) Validate() error {
	if m.ContractID == "" {
		return fmt.Errorf("contracts: contract_id is empty")
	}
	if m.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("contracts: unsupported schema version %q (current: %s)", m.SchemaVersion, CurrentSchemaVersion)
	}
	return nil
}
