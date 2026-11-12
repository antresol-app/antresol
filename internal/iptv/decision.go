package iptv

// Decision is what the editor decided about a channel: shown or hidden
// (nil: undecided), its category, name and number (zero: keep), and why.
// The rules (rules.go) make the default one; an admin's manual one is
// stored per member (Member.Manual) and wins field by field.
type Decision struct {
	Shown  *bool  `json:"shown,omitempty"`
	Group  string `json:"group,omitempty"`
	Name   string `json:"name,omitempty"`
	Number int    `json:"number,omitempty"`
	Why    string `json:"why,omitempty"`
}
