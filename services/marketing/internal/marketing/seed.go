package marketing

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// seedCampaign is one entry of the demo campaign catalog.
type seedCampaign struct {
	source   string
	campaign string
	adSpend  string
}

// The deterministic demo campaigns, inserted in this order so every
// environment has the same IDs. k6 picks its utm_source/utm_campaign pairs
// from this list. email and organic cost nothing, so their ROAS is unbounded
// and their CAC is zero.
var seedCatalog = []seedCampaign{
	{"google", "search_brand", "1200.00"},
	{"facebook", "summer_sale", "800.00"},
	{"instagram", "influencer_q3", "650.00"},
	{"tiktok", "viral_launch", "400.00"},
	{"email", "newsletter", "0.00"},
	{"organic", "none", "0.00"},
}

// seedCampaigns inserts the demo campaigns if the table has never held any.
// Soft-deleted rows count, so a deliberately deleted campaign isn't recreated
// and a redeploy never resets ad spend.
func seedCampaigns(ctx context.Context, db *gorm.DB) error {
	var existing int64
	if err := db.WithContext(ctx).Unscoped().Model(&Campaign{}).Count(&existing).Error; err != nil {
		return fmt.Errorf("counting campaigns: %w", err)
	}
	if existing > 0 {
		return nil
	}

	campaigns := make([]Campaign, 0, len(seedCatalog))
	for _, s := range seedCatalog {
		campaigns = append(campaigns, Campaign{
			UTMSource:   s.source,
			UTMCampaign: s.campaign,
			AdSpend:     decimal.RequireFromString(s.adSpend),
		})
	}
	if err := db.WithContext(ctx).Create(&campaigns).Error; err != nil {
		return fmt.Errorf("seeding campaigns: %w", err)
	}
	return nil
}
