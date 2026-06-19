package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"walnut-billing/internal/domain"

	"gorm.io/gorm"
)

type Mode string

const (
	ModeAuto      Mode = "auto"
	ModeVersioned Mode = "versioned"
	ModeDisabled  Mode = "disabled"
)

type Options struct {
	Mode Mode
}

type Migration struct {
	Version string
	Name    string
	Up      func(context.Context, *gorm.DB) error
}

type SchemaMigration struct {
	Version   string    `gorm:"primaryKey;size:32"`
	Name      string    `gorm:"size:200;not null"`
	Checksum  string    `gorm:"size:64;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

func (SchemaMigration) TableName() string { return "schema_migrations" }

func Run(ctx context.Context, db *gorm.DB, options Options) error {
	runner := Runner{db: db, migrations: DefaultMigrations()}
	return runner.Run(ctx, options)
}

type Runner struct {
	db         *gorm.DB
	migrations []Migration
}

func NewRunner(db *gorm.DB, migrations []Migration) Runner {
	return Runner{db: db, migrations: append([]Migration(nil), migrations...)}
}

func (r Runner) Run(ctx context.Context, options Options) error {
	if r.db == nil {
		return fmt.Errorf("database migration runner requires db")
	}
	switch options.Mode {
	case "", ModeAuto:
		return autoMigrate(ctx, r.db)
	case ModeVersioned:
		return r.runVersioned(ctx)
	case ModeDisabled:
		return nil
	default:
		return fmt.Errorf("unsupported database migration mode %q", options.Mode)
	}
}

func (r Runner) AppliedVersions(ctx context.Context) (map[string]SchemaMigration, error) {
	if err := r.db.WithContext(ctx).AutoMigrate(&SchemaMigration{}); err != nil {
		return nil, err
	}
	var applied []SchemaMigration
	if err := r.db.WithContext(ctx).Find(&applied).Error; err != nil {
		return nil, err
	}
	result := make(map[string]SchemaMigration, len(applied))
	for _, migration := range applied {
		result[migration.Version] = migration
	}
	return result, nil
}

func (r Runner) runVersioned(ctx context.Context) error {
	migrations, err := normalizeMigrations(r.migrations)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("prepare schema migration metadata: %w", err)
	}
	for _, migration := range migrations {
		if err := r.applyOne(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

func (r Runner) applyOne(ctx context.Context, migration Migration) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing SchemaMigration
		err := tx.Where("version = ?", migration.Version).First(&existing).Error
		if err == nil {
			if existing.Checksum != migrationChecksum(migration) {
				return fmt.Errorf("schema migration %s checksum mismatch", migration.Version)
			}
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read schema migration %s: %w", migration.Version, err)
		}
		if migration.Up == nil {
			return fmt.Errorf("schema migration %s has no up function", migration.Version)
		}
		if err := migration.Up(ctx, tx); err != nil {
			return fmt.Errorf("apply schema migration %s %s: %w", migration.Version, migration.Name, err)
		}
		record := SchemaMigration{
			Version:   migration.Version,
			Name:      migration.Name,
			Checksum:  migrationChecksum(migration),
			AppliedAt: time.Now().UTC(),
		}
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("record schema migration %s: %w", migration.Version, err)
		}
		return nil
	})
}

func DefaultMigrations() []Migration {
	return []Migration{
		{
			Version: "202606190001",
			Name:    "baseline_control_plane_schema",
			Up: func(ctx context.Context, db *gorm.DB) error {
				return autoMigrate(ctx, db)
			},
		},
	}
}

func autoMigrate(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(schemaModels()...); err != nil {
		return fmt.Errorf("auto migrate schema: %w", err)
	}
	return nil
}

func schemaModels() []any {
	return []any{
		&domain.License{},
		&domain.Order{},
		&domain.Product{},
		&domain.AuditEntry{},
		&domain.User{},
		&domain.RegistrationRequest{},
		&domain.EntitlementGrant{},
		&domain.UserDevice{},
		&domain.TrialGrant{},
		&domain.AccessLoginChallenge{},
		&domain.CreditAccount{},
		&domain.CreditBucket{},
		&domain.CreditReservation{},
		&domain.CreditTransaction{},
		&domain.PaymentEventInbox{},
		&domain.FulfillmentExecution{},
		&domain.PaymentRiskFlag{},
		&domain.SubscriptionCancellation{},
		&domain.CloudProject{},
		&domain.CloudManifest{},
		&domain.CloudObject{},
	}
}

func normalizeMigrations(migrations []Migration) ([]Migration, error) {
	result := append([]Migration(nil), migrations...)
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	seen := make(map[string]struct{}, len(result))
	for _, migration := range result {
		if migration.Version == "" || migration.Name == "" {
			return nil, fmt.Errorf("schema migrations require version and name")
		}
		if _, ok := seen[migration.Version]; ok {
			return nil, fmt.Errorf("duplicate schema migration version %s", migration.Version)
		}
		seen[migration.Version] = struct{}{}
	}
	return result, nil
}

func migrationChecksum(migration Migration) string {
	sum := sha256.Sum256([]byte(migration.Version + ":" + migration.Name))
	return hex.EncodeToString(sum[:])
}
