// stars/shared_fields.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

// SyncedPreferenceFields lists every field on Preferences (including
// flattened CommonPreferences) that is shared across all relief
// controllers at a TCW via TCWDisplayState. Mutations must go through
// a typed RPC; loads of saved preference sets must NOT overwrite these.
//
// The field names here are the Go field names on Preferences/CommonPreferences
// (no JSON tags). See stars/shared_fields_test.go for the enforcement.
var SyncedPreferenceFields = []string{
	// Scope view - flat scope geometry
	"DefaultCenter",
	"UserCenter",
	"UseUserCenter",
	"Range",
	"RangeRingsUserCenter",
	"RangeRingRadius",
	"UseUserRangeRingsCenter",

	// Radar site selection (affects what's drawn)
	"RadarSiteSelected",
	"FusedRadarMode",

	// Leader-line directions for other-controller tracks
	"ControllerLeaderLineDirections",
	"OtherControllerLeaderLineDirection",
	"UnassociatedLeaderLineDirection",

	// Filtering
	"AltitudeFilters",
	"AutomaticHandoffs",

	// Quick-look
	"QuickLookAll",
	"QuickLookAllIsPlus",
	"QuickLookTCPs",
	"DisabledQLRegions",

	// Coordination display
	"DisplayEmptyCoordinationLists",

	// CRDA
	"CRDA",

	// LDB / beacon-code visibility
	"DisplayLDBBeaconCodes",
	"SelectedBeacons",

	// Safety alerts
	"DisableCAWarnings",
	"DisableMCIWarnings",
	"DisableMSAW",

	// Video maps visible (but not brightness of video map groups)
	"VideoMapVisible",

	// 4-29 behavior
	"InhibitPositionSymOnUnassociatedPrimary",

	// Scope geometry shared with everyone at the TCW
	"RadarTrackHistory",
	"RadarTrackHistoryRate",

	// Weather display (which levels are on, not brightness)
	"DisplayWeatherLevel",
	"LastDisplayWeatherLevel",

	// Own-track leader line
	"LeaderLineDirection",
	"LeaderLineLength",

	// Full-datablock forcing
	"OverflightFullDatablocks",
	"AutomaticFDBOffset",

	// Suspended-track altitude
	"DisplaySuspendedTrackAltitude",

	// TPA / ATPA globals
	"DisplayTPASize",
	"DisplayATPAInTrailDist",
	"DisplayATPAWarningAlertCones",
	"DisplayATPAMonitorCones",

	// Predicted track line globals
	"PTLLength",
	"PTLOwn",
	"PTLAll",

	// Dwell mode and list layout
	"DwellMode",
	"SSAList",
	"VFRList",
	"TABList",
	"AlertList",
	"CoastList",
	"SignOnList",
	"VideoMapsList",
	"CRDAStatusList",
	"MCISuppressionList",
	"TowerLists",
	"CoordinationLists",
	"RestrictionAreaList",
}

// UnsyncedPreferenceFields lists every field that remains local per user.
// These are personal-comfort settings (brightness, font sizes, cursor
// behavior) or bookkeeping state that doesn't affect the shared picture.
var UnsyncedPreferenceFields = []string{
	// Bookkeeping
	"Name",

	// DCB chrome (position/visibility is personal)
	"DisplayDCB",
	"DCBPosition",

	// Audio / comfort
	"AudioVolume",
	"AudioEffectEnabled",

	// Cursor behavior
	"AutoCursorHome",
	"CursorHome",

	// Personal brightness and font sizing
	"Brightness",
	"CharSize",

	// Preview area position (personal UI chrome)
	"PreviewAreaPosition",

	// Restriction area visibility/behavior (per the type comment, local per user)
	"RestrictionAreaSettings",
}
