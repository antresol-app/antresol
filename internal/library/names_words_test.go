//go:build !sources

package library

import "testing"

// familyWords returns the words that no exported name may hold; none here.
func familyWords(t *testing.T) []string {
	t.Helper()
	t.Log("the word half of this check runs only where a word list is supplied")
	return nil
}
