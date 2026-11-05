package api

import "strings"

// browserOf names a browser session for «Устройства» from its User-Agent:
// the browser («Safari», «Chrome») and its system («iPhone», «Mac»); ""
// for what it does not know. Order matters: Chrome's string also says
// Safari, Edge's and Opera's also say Chrome, every iPhone browser says
// Safari and Mobile, an iPad asking for the desktop site says Macintosh.
func browserOf(ua string) (browser, system string) {
	has := func(s string) bool { return strings.Contains(ua, s) }
	switch {
	case has("Edg/"), has("EdgA/"), has("EdgiOS/"):
		browser = "Edge"
	case has("OPR/"), has("OPiOS/"):
		browser = "Opera"
	case has("YaBrowser/"):
		browser = "Yandex Browser"
	case has("Firefox/"), has("FxiOS/"):
		browser = "Firefox"
	case has("Chrome/"), has("CriOS/"):
		browser = "Chrome"
	case has("Safari/"):
		browser = "Safari"
	}
	switch {
	case has("iPhone"):
		system = "iPhone"
	case has("iPad"):
		system = "iPad"
	case has("Android"):
		system = "Android"
	case has("Macintosh"), has("Mac OS X"):
		system = "Mac"
	case has("Windows"):
		system = "Windows"
	case has("CrOS"):
		system = "ChromeOS"
	case has("Linux"):
		system = "Linux"
	}
	return browser, system
}
