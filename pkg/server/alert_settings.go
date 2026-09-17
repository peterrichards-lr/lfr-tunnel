package server

// The admin alert vocabulary (#1882).
//
// Six alert_notify_* keys existed and the settings endpoint exposed three, so three alerts could
// be switched off from System Settings and three could not be switched off at all except by
// writing the row by hand. An owner who found watchdog or vanity-hook alerts noisy had no way to
// stop them, and nothing told them the setting existed.
//
// The list lived in three places that had to agree: the sendAdminAlert call sites, the settings
// endpoint's hardcoded reads, and the portal forms. Adding a fourth alert meant editing all three
// and missing one was silent -- the same shape #1851 fixed for user statuses, and the reason
// alert_notify_watchdog_restart (#1875) was added following the broken pattern rather than
// fixing it.
//
// So the keys are declared ONCE here, and everything else reads this. tests/hooks and
// scripts/check-alert-vocabulary.cjs bind the three consumers to it, so an alert with no toggle
// fails the build rather than shipping invisible.

// The stored form of a toggle. admin_settings holds strings, and these are the only two values
// the endpoint accepts, so they are named once rather than spelled at each comparison.
const (
	alertSettingOn  = "true"
	alertSettingOff = "false"
)

// AlertSetting is one admin alert an owner can switch off.
type AlertSetting struct {
	// Key is the admin_settings row, and the name the portals POST back.
	Key string `json:"key"`
	// LabelKey is the i18n key for its label. Named rather than derived from Key so a label
	// can be reworded without renaming a stored setting.
	LabelKey string `json:"label_key"`
	// DefaultOn is what an unset row means.
	//
	// Not uniform, and the asymmetry is deliberate rather than accidental: a tunnel going
	// offline is routine on a laptop that closed, so mailing about it by default would train
	// an owner to ignore the channel. Everything else is either rare or actionable.
	DefaultOn bool `json:"default_on"`
}

// AlertSettings is the complete vocabulary, in the order the portals render it.
//
// Adding an alert means adding it here and nowhere else. The gate will then require a label in
// every locale bundle and a toggle in both portal arms before the build passes.
var AlertSettings = []AlertSetting{
	{Key: "alert_notify_registration", LabelKey: "alert_registration", DefaultOn: true},
	{Key: "alert_notify_blacklist", LabelKey: "alert_blacklist", DefaultOn: true},
	{
		// Off by default: a tunnel dropping is routine -- a closed laptop, a lost network --
		// and an owner paged for each one stops reading the channel.
		Key: "alert_notify_tunnel_offline", LabelKey: "alert_tunnel_offline", DefaultOn: false,
	},
	{Key: "alert_notify_vanity_hook_failure", LabelKey: "alert_vanity_hook_failure", DefaultOn: true},
	{Key: "alert_notify_extension_requested", LabelKey: "alert_extension_requested", DefaultOn: true},
	{Key: "alert_notify_watchdog_restart", LabelKey: "alert_watchdog_restart", DefaultOn: true},
	// An edge that stops reporting bandwidth is silent by nature -- there is no error to
	// notice (#1980). Defaults on: quota enforcement is sized from this feed, so a dead one
	// produces limits that never fire.
	{Key: "alert_notify_edge_metrics_stalled", LabelKey: "alert_edge_metrics_stalled", DefaultOn: true},
	// Two entries, not one with a severity, because they are two different events (#1959).
	// Throttled is informational: someone is using a lot and has been slowed down, and an
	// owner may want to raise their allowance. Stopped is somebody's demo ending.
	{Key: "alert_notify_quota_throttled", LabelKey: "alert_quota_throttled", DefaultOn: true},
	{Key: "alert_notify_quota_stopped", LabelKey: "alert_quota_stopped", DefaultOn: true},
}

// alertSettingDefault reports whether an unset row means on, and whether the key is one this
// gateway knows about at all.
//
// The second return matters: an unknown key must not be treated as "on by default", because that
// would make a typo'd setting silently enable an alert nobody declared.
func alertSettingDefault(key string) (defaultOn bool, known bool) {
	for _, a := range AlertSettings {
		if a.Key == key {
			return a.DefaultOn, true
		}
	}
	return false, false
}

// alertSettingEnabled resolves a stored value against its declared default.
//
// "false" is off and "true" is on whatever the default; anything else, including an absent row,
// falls back to the declaration. Parsed here rather than at each call site so the tunnel-offline
// special case cannot be forgotten by the next reader.
func alertSettingEnabled(key, stored string) bool {
	switch stored {
	case alertSettingOff:
		return false
	case alertSettingOn:
		return true
	}
	on, known := alertSettingDefault(key)
	if !known {
		// An undeclared key defaults ON, not off.
		//
		// The instinct is the opposite -- refuse what was not declared -- but here that
		// silently DROPS an alert, and a dropped alert is indistinguishable from nothing
		// having happened, which is the failure this whole area exists to prevent (#1824).
		// alert_notify_test is exactly this case: the admin clicked a button to send it, so
		// it has no toggle and never will.
		//
		// The typo risk that would argue for defaulting off is handled by
		// check-alert-vocabulary.cjs instead, which fails the build on any key raised but not
		// declared. A gate is the right tool for that; a runtime default is not.
		return true
	}
	return on
}
