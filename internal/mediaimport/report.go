// Package mediaimport moves what other media servers know (Jellyfin, Kavita,
// Audiobookshelf: who watched what, where they stopped, favourites) into
// Vault. Importers only read the old services; every outcome is a line of a
// Report, saved after each step. The sources build's migration adds its own
// sources to the same Service.
package mediaimport

import (
	"github.com/antresol-app/antresol/internal/mediaimport/runkit"
)

// Line statuses.
const (
	StatusPlanned  = runkit.StatusPlanned
	StatusImported = runkit.StatusImported
	StatusSkipped  = runkit.StatusSkipped
	StatusFailed   = runkit.StatusFailed
	StatusWarning  = runkit.StatusWarning
)

// A run's report and its lines (runkit: Report.Add, LinesWhere, Tally,
// Localize; use a Report by pointer only).
type (
	Line   = runkit.Line
	Report = runkit.Report
)

// The reports: their ids, saving, loading and listing them, and lines
// served on their own in the request's language.
var (
	LocalizeLines = runkit.LocalizeLines
	NewReportID   = runkit.NewReportID
	SaveReport    = runkit.SaveReport
	LoadReport    = runkit.LoadReport
	ListReports   = runkit.ListReports
)

var errBadReportID = runkit.ErrBadReportID
