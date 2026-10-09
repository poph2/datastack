// Package marketing is the Marketing Service domain: the ad campaigns traffic
// comes from, the touchpoint (attribution) logged for each visit, and the
// conversion that links a touchpoint to the order it produced.
package marketing

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/poph2/datastack/pkgs/database"
)

// moneyDecimals is the number of decimal places money is stored and rendered
// with. Money is a shopspring decimal everywhere (never float64) and is
// serialised as a fixed-point string such as "800.00".
const moneyDecimals = 2

// Campaign is a row in the campaigns table: one (utm_source, utm_campaign)
// pair and what has been spent on it. The gorm tags are the schema:
// AutoMigrate creates the columns, CHECK constraints and indexes from them.
type Campaign struct {
	database.Model
	// The pair is unique among live campaigns only (see the soft-delete
	// convention). Both fields name the same index, so GORM builds one
	// composite partial index.
	UTMSource   string `gorm:"size:100;not null;uniqueIndex:idx_campaigns_source_campaign,priority:1,where:deleted_at IS NULL"`
	UTMCampaign string `gorm:"size:100;not null;uniqueIndex:idx_campaigns_source_campaign,priority:2,where:deleted_at IS NULL"`
	// Deliberately no default: GORM skips zero-valued fields that have one on
	// insert, and 0.00 (organic, email) is a meaningful spend.
	AdSpend decimal.Decimal `gorm:"type:decimal(10,2);not null;check:chk_campaigns_ad_spend,ad_spend >= 0"`
}

// Attribution is a row in the attributions table: one visit from a campaign
// and, once the Order Service reports it, the order it converted into.
//
// UserID and OrderID are logical references to the Customer and Order
// services. They are not foreign keys, because those rows live in other
// databases.
type Attribution struct {
	database.Model
	UserID      uint64 `gorm:"not null;check:chk_attributions_user_id,user_id > 0"`
	UTMSource   string `gorm:"size:100;not null"`
	UTMCampaign string `gorm:"size:100;not null"`
	// The conversion: all three are NULL until the Order Service reports a
	// paid order, then all three are set together (the CHECK on ConvertedAt
	// is the backstop for that).
	OrderID     *uint64          `gorm:"check:chk_attributions_order_id,order_id > 0"`
	OrderAmount *decimal.Decimal `gorm:"type:decimal(10,2);check:chk_attributions_order_amount,order_amount > 0"`
	// Set by Postgres (NOW()), never by the pod's clock.
	ConvertedAt *time.Time `gorm:"check:chk_attributions_conversion,(order_id IS NULL) = (converted_at IS NULL) AND (order_id IS NULL) = (order_amount IS NULL)"`
}

// Converted reports whether the attribution has been linked to an order.
func (a Attribution) Converted() bool {
	return a.OrderID != nil
}

// Migrate creates or updates the Marketing Service schema and loads the seed
// campaigns into an empty campaigns table.
func Migrate(ctx context.Context, db *gorm.DB) error {
	if err := database.AutoMigrate(ctx, db, &Campaign{}, &Attribution{}); err != nil {
		return err
	}
	return seedCampaigns(ctx, db)
}
