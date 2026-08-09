package contracts

import "github.com/google/uuid"

// generateUUID returns a new UUID v4 string.
func generateUUID() string {
	return uuid.New().String()
}
