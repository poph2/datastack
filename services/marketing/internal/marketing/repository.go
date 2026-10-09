package marketing

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrCampaignNotFound means no live campaign has the requested
	// (utm_source, utm_campaign) pair.
	ErrCampaignNotFound = errors.New("campaign not found")
	// ErrAttributionNotFound means no live attribution has the requested ID.
	ErrAttributionNotFound = errors.New("attribution not found")
	// ErrAlreadyConverted means the attribution is already linked to a
	// different order.
	ErrAlreadyConverted = errors.New("attribution already converted to a different order")
)

// Conversion is what the Order Service reports when a checkout is paid.
type Conversion struct {
	OrderID     uint64
	OrderAmount decimal.Decimal
}

// ConvertResult is the attribution after a conversion, and whether this call
// converted it. FirstConversion is false for an idempotent re-send, so a
// retried conversion isn't counted twice.
type ConvertResult struct {
	Attribution     Attribution
	FirstConversion bool
}

// Repository reads and writes campaigns and attributions in the Marketing
// Service database.
type Repository struct {
	db *gorm.DB
}

// NewRepository returns a Repository backed by db.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// ListCampaigns returns every live campaign, ordered by ID. The catalog is a
// handful of seeded rows, so it isn't paginated.
func (r *Repository) ListCampaigns(ctx context.Context) ([]Campaign, error) {
	var campaigns []Campaign
	if err := r.db.WithContext(ctx).Order("id").Find(&campaigns).Error; err != nil {
		return nil, fmt.Errorf("listing campaigns: %w", err)
	}
	return campaigns, nil
}

// CreateAttribution inserts a, after checking that its campaign exists, and
// fills in its ID and timestamps.
func (r *Repository) CreateAttribution(ctx context.Context, a *Attribution) error {
	if err := r.requireCampaign(ctx, a.UTMSource, a.UTMCampaign); err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(a).Error; err != nil {
		return fmt.Errorf("creating attribution: %w", err)
	}
	return nil
}

// GetAttribution returns the live attribution with the given ID.
func (r *Repository) GetAttribution(ctx context.Context, id uint64) (Attribution, error) {
	var a Attribution
	err := r.db.WithContext(ctx).First(&a, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Attribution{}, ErrAttributionNotFound
	}
	if err != nil {
		return Attribution{}, fmt.Errorf("getting attribution %d: %w", id, err)
	}
	return a, nil
}

// Convert links the attribution to the order that conversion describes.
//
// The check and the write are one conditional UPDATE that only matches an
// unconverted attribution. Postgres locks the row, and under READ COMMITTED a
// concurrent conversion re-checks "order_id IS NULL" against the winner's
// row, so exactly one order ever converts an attribution.
//
// When nothing matched, a lookup tells the cases apart: an unknown ID, an
// idempotent re-send of the same order (returned unchanged, first values
// kept), or a different order (ErrAlreadyConverted).
func (r *Repository) Convert(ctx context.Context, id uint64, conversion Conversion) (ConvertResult, error) {
	var a Attribution
	result := r.db.WithContext(ctx).
		Model(&a).
		Clauses(clause.Returning{}).
		Where("id = ? AND order_id IS NULL", id).
		Updates(map[string]any{
			"order_id":     conversion.OrderID,
			"order_amount": conversion.OrderAmount,
			"converted_at": gorm.Expr("NOW()"),
		})
	if result.Error != nil {
		return ConvertResult{}, fmt.Errorf("converting attribution %d: %w", id, result.Error)
	}
	if result.RowsAffected == 1 {
		return ConvertResult{Attribution: a, FirstConversion: true}, nil
	}

	existing, err := r.GetAttribution(ctx, id)
	if err != nil {
		return ConvertResult{}, err
	}
	if existing.OrderID == nil || *existing.OrderID != conversion.OrderID {
		return ConvertResult{}, ErrAlreadyConverted
	}
	return ConvertResult{Attribution: existing}, nil
}

func (r *Repository) requireCampaign(ctx context.Context, source, campaign string) error {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Campaign{}).
		Where("utm_source = ? AND utm_campaign = ?", source, campaign).
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("looking up campaign %s/%s: %w", source, campaign, err)
	}
	if count == 0 {
		return ErrCampaignNotFound
	}
	return nil
}
