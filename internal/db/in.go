package db

// In is the placeholder list of an IN clause for n values, parentheses
// included: "(?,?,?)". For n <= 0 it is "()", which SQLite accepts: «IN ()»
// matches nothing, so a read or a harmless write with no values needs no
// guard (it can still skip the query). «NOT IN ()» is the opposite, true of
// every row: guard before a NOT IN that deletes or updates, or the empty
// list takes everything.
func In(n int) string {
	if n <= 0 {
		return "()"
	}
	b := make([]byte, 0, 2*n+1)
	b = append(b, '(', '?')
	for i := 1; i < n; i++ {
		b = append(b, ',', '?')
	}
	return string(append(b, ')'))
}

// Args is vals as query arguments (database/sql takes a []any, not a []T).
func Args[T any](vals []T) []any {
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return args
}

// InList is In(len(vals)) and Args(vals): the clause and its arguments.
func InList[T any](vals []T) (string, []any) { return In(len(vals)), Args(vals) }
