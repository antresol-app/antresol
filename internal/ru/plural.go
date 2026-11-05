// Package ru holds Russian language helpers shared by the API and the
// migration (which cannot import the API).
package ru

import "strconv"

// Plural picks the Russian form for n: one (1, 21, 101…), few (2–4,
// 22–24…) or many (0, 5–20, 25…) and returns "n <form>": «1 том», «2 тома»,
// «5 томов».
func Plural(n int, one, few, many string) string {
	m := n
	if m < 0 {
		m = -m
	}
	form := many
	switch m10, m100 := m%10, m%100; {
	case m100 >= 11 && m100 <= 14:
	case m10 == 1:
		form = one
	case m10 >= 2 && m10 <= 4:
		form = few
	}
	return strconv.Itoa(n) + " " + form
}
