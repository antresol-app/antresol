package bookmeta

// ISBN13 is v as the 13 digits of an ISBN-13: an ISBN-10 is converted, and
// hyphens and spaces are ignored. It is "" when v is no ISBN with a correct
// check digit, so two spellings of one ISBN compare equal.
func ISBN13(v string) string {
	if !validISBN(v) {
		return ""
	}
	var d []byte
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			d = append(d, byte(c))
		case c == 'X' || c == 'x':
			d = append(d, 'X')
		}
	}
	if len(d) == 10 {
		d = append([]byte("978"), d[:9]...)
		d = append(d, isbn13Check(d))
	}
	return string(d)
}

// isbn13Check is the check digit of the first 12 digits.
func isbn13Check(d []byte) byte {
	sum := 0
	for i, c := range d[:12] {
		sum += int(c-'0') * (1 + 2*(i%2))
	}
	return byte('0' + (10-sum%10)%10)
}
