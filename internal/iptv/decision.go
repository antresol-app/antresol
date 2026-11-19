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
	// Variant: the admin's word on the entry's variants (variants_ai.go):
	// VariantOwn, a channel of its own; VariantWith + another entry's
	// Ident, a variant of that entry's channel; "": the rules and the AI
	// helper decide. It is not a decision about showing (ApplyManual
	// leaves Why alone for it).
	Variant string `json:"variant,omitempty"`
}
