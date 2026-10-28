package service

import "encoding/json"

// SidebarMenuVisibility controls navigation entries only. All entries default off.
type SidebarMenuVisibility struct {
	Channels           bool `json:"channels"`
	Announcements      bool `json:"announcements"`
	RedeemCodes        bool `json:"redeem_codes"`
	PromoCodes         bool `json:"promo_codes"`
	AdminSubscriptions bool `json:"admin_subscriptions"`
	Subscriptions      bool `json:"subscriptions"`
	Redeem             bool `json:"redeem"`
	Profile            bool `json:"profile"`
	AuditLogs          bool `json:"audit_logs"`
}

func parseSidebarMenuVisibility(raw string) SidebarMenuVisibility {
	var visibility SidebarMenuVisibility
	if err := json.Unmarshal([]byte(raw), &visibility); err != nil {
		return SidebarMenuVisibility{}
	}
	return visibility
}
