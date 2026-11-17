package media

import "regexp"

// logInput is an input (a file path or a loopback URL) as media logs it:
// the internal token of a loopback URL (?k=… or &k=…) is hidden. Every log
// line naming an input, or carrying ffmpeg's stderr (which starts with
// it), goes through here.
func logInput(s string) string {
	return inputToken.ReplaceAllString(s, "${1}k=…")
}

var inputToken = regexp.MustCompile(`([?&])k=[^&\s"':]*`)
