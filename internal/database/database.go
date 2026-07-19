package database

import (
	"fmt"
	"time"

	"github.com/novexa/gateway/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database wraps the GORM database connection
type Database struct {
	DB *gorm.DB
}

// UsageRecord represents a single API request usage record
type UsageRecord struct {
	ID               string    `gorm:"primaryKey;type:text" json:"id"`
	RequestID        string    `gorm:"type:text;index" json:"request_id"`
	ModelID          string    `gorm:"type:text;index" json:"model_id"`
	ProviderModelID  string    `gorm:"type:text" json:"provider_model_id"`
	Provider         string    `gorm:"type:text;index" json:"provider"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	Requests         int       `json:"requests"`
	DurationMs       int64     `json:"duration_ms"`
	InputChars       int       `json:"input_chars"`
	OutputChars      int       `json:"output_chars"`
	LatencyMs        int64     `json:"latency_ms"`
	EstimatedCostUSD *float64  `json:"estimated_cost_usd"`
	CostSource       string    `gorm:"type:text" json:"cost_source"`
	StatusCode       int       `json:"status_code"`
	IsStream         bool      `json:"is_stream"`
	ErrorMessage     *string   `json:"error_message,omitempty"`
	CreatedAt        time.Time `gorm:"index" json:"created_at"`
}

// RequestLog represents an audit log entry
type RequestLog struct {
	ID           string    `gorm:"primaryKey;type:text" json:"id"`
	RequestID    string    `gorm:"type:text;uniqueIndex" json:"request_id"`
	Method       string    `gorm:"type:text" json:"method"`
	Path         string    `gorm:"type:text" json:"path"`
	StatusCode   int       `json:"status_code"`
	ClientIP     string    `gorm:"type:text" json:"client_ip"`
	UserAgent    string    `gorm:"type:text" json:"user_agent"`
	ErrorMessage *string   `gorm:"type:text" json:"error_message,omitempty"`
	Provider     string    `gorm:"type:text" json:"provider"`
	Model        string    `gorm:"type:text" json:"model"`
	LatencyMs    int64     `json:"latency_ms"`
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

// ProviderHealthRecord represents a provider health check record
type ProviderHealthRecord struct {
	ID        string    `gorm:"primaryKey;type:text" json:"id"`
	Provider  string    `gorm:"type:text;index" json:"provider"`
	IsHealthy bool      `json:"is_healthy"`
	LatencyMs int64     `json:"latency_ms"`
	LastError *string   `gorm:"type:text" json:"last_error,omitempty"`
	CheckedAt time.Time `gorm:"index" json:"checked_at"`
}

// CostRate represents the cost rate for a model/provider combination
type CostRate struct {
	ID                  string     `gorm:"primaryKey;type:text" json:"id"`
	Model               string     `gorm:"type:text;index" json:"model"`
	Provider            string     `gorm:"type:text;index" json:"provider"`
	PromptCostPer1K     float64    `json:"prompt_cost_per_1k"`
	CompletionCostPer1K float64    `json:"completion_cost_per_1k"`
	EffectiveFrom       time.Time  `json:"effective_from"`
	EffectiveTo         *time.Time `json:"effective_to,omitempty"`
}

// Connect creates a new database connection
func Connect(cfg *config.DatabaseConfig) (*Database, error) {
	var dialector gorm.Dialector

	switch cfg.Driver {
	case "sqlite":
		dialector = sqlite.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	logLevel := logger.Silent
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying db: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// Enable WAL mode for SQLite
	if cfg.Driver == "sqlite" {
		pragmas := []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA synchronous=NORMAL",
			"PRAGMA busy_timeout=5000",
		}
		for _, pragma := range pragmas {
			if _, err := sqlDB.Exec(pragma); err != nil {
				return nil, fmt.Errorf("failed to apply %q: %w", pragma, err)
			}
		}
	}

	return &Database{DB: db}, nil
}

// Migrate runs database migrations
func (d *Database) Migrate() error {
	return d.DB.AutoMigrate(
		&UsageRecord{},
		&RequestLog{},
		&ProviderHealthRecord{},
		&CostRate{},
	)
}

// Close closes the database connection
func (d *Database) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
