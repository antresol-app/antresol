package i18n

import (
	"context"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Collator orders names for the viewer's language the way a person reads
// them: by the language's alphabet, ignoring case, and numbers by value
// («Группа 2» before «Группа 10»), as the web's Intl.Collator with
// numeric: true. A Collator is not safe for concurrent use: make one per
// sort.
func Collator(ctx context.Context) *collate.Collator {
	tag := language.Russian
	if FromContext(ctx) == EN {
		tag = language.English
	}
	return collate.New(tag, collate.Numeric, collate.IgnoreCase)
}
