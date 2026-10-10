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
	UTMSource   string `gorm:"size:100;not null;uniqueIndex:idx_campaigns_source_campaign,priority:1,where:deleted_at IS NULL;comment:Traffic source, the utm_source URL parameter: google, facebook, instagram, tiktok, email or organic."`
	UTMCampaign string `gorm:"size:100;not null;uniqueIndex:idx_campaigns_source_campaign,priority:2,where:deleted_at IS NULL;comment:Campaign name, the utm_campaign URL parameter, such as summer_sale. Unique together with utm_source."`
	// insert, and 0.00 (organic, email) is a meaningful spend.
	AdSpend decimal.Decimal `gorm:"type:decimal(10,2);not null;check:chk_campaigns_ad_spend,ad_spend >= 0;comment:Total spend on this campaign so far, in USD. A lifetime total, not a daily amount. 0.00 for unpaid channels (email, organic). A paid campaign is a row with ad_spend > 0: restrict to those for any question about paid campaigns, ROAS or acquisition cost, because the unpaid channels have no spend to divide by."`
}

// TableComment documents the table for the semantic layer (see
// database.TableCommenter).
func (Campaign) TableComment() string {
	return "Marketing campaigns (Marketing Service): one row per campaign, identified by the pair (utm_source, utm_campaign), with its total ad spend. Join attributions on both utm_source and utm_campaign. ROAS for a campaign is the cleared revenue of its converted attributions divided by ad_spend, and CAC is ad_spend divided by the number of distinct converted customers. Both divide by ad_spend, which is a column rather than an aggregate, so group by utm_source, utm_campaign AND ad_spend. The organic/none row represents traffic that arrived without an ad."
}

// Attribution is a row in the attributions table: one visit from a campaign
// and, once the Order Service reports it, the order it converted into.
//
// UserID and OrderID are logical references to the Customer and Order
// services. They are not foreign keys, because those rows live in other
// databases.
type Attribution struct {
	database.Model
	UserID      uint64 `gorm:"not null;check:chk_attributions_user_id,user_id > 0;comment:Customer who clicked. [ref: customers.id]"`
	UTMSource   string `gorm:"size:100;not null;comment:Traffic source of the click. Join to campaigns together with utm_campaign. [ref: campaigns.utm_source]"`
	UTMCampaign string `gorm:"size:100;not null;comment:Campaign of the click. Join to campaigns together with utm_source. [ref: campaigns.utm_campaign]"`
	// paid order, then all three are set together (the CHECK on ConvertedAt
	// is the backstop for that).
	OrderID     *uint64          `gorm:"check:chk_attributions_order_id,order_id > 0;comment:Paid order this click converted into. NULL until, and unless, it converts. [ref: orders.id]"`
	OrderAmount *decimal.Decimal `gorm:"type:decimal(10,2);check:chk_attributions_order_amount,order_amount > 0;comment:Value of the converted order in USD as reported at conversion time, NULL until converted. For cleared revenue prefer payments.amount where status is SUCCESS."`
	ConvertedAt *time.Time       `gorm:"check:chk_attributions_conversion,(order_id IS NULL) = (converted_at IS NULL) AND (order_id IS NULL) = (order_amount IS NULL);comment:When the conversion was recorded, in UTC, NULL until converted. converted_at minus created_at is the click-to-purchase time."`
}

// TableComment documents the table for the semantic layer (see
// database.TableCommenter).
func (Attribution) TableComment() string {
	return "Ad clicks and their conversions (Marketing Service): one row per tracked visit from a campaign. order_id, order_amount and converted_at are filled in when the visit leads to a paid order. Join campaigns on both utm_source and utm_campaign, customers via user_id, and orders and payments via order_id. Conversion rate is converted rows (order_id IS NOT NULL) divided by all rows."
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
