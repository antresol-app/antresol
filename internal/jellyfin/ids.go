package jellyfin

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Jellyfin ids are GUIDs (32 hex). Vault's are deterministic and
// reversible: "5641554c" ("VAUL") + 4 hex of kind + 20 hex of the Vault id,
// so item 5 and file 5 never collide and no lookup table is needed.
const (
	kindItem = 1
	kindView = 2
	kindUser = 3
	kindFile = 4

	idPrefix = "5641554c"
)

func guid(kind int, id int64) string { return fmt.Sprintf(idPrefix+"%04x%020x", kind, id) }

func ItemID(id int64) string { return guid(kindItem, id) }
func UserID(id int64) string { return guid(kindUser, id) }
func FileID(id int64) string { return guid(kindFile, id) }

// viewNums numbers the library views.
var viewNums = map[string]int64{"movies": 1, "tvshows": 2}

// ViewID is the id of a library view ("movies", "tvshows").
func ViewID(name string) string { return guid(kindView, viewNums[name]) }

// ParseID reads a Vault GUID, with or without dashes, in any case.
func ParseID(s string) (kind int, id int64, ok bool) {
	s = strings.ToLower(strings.ReplaceAll(s, "-", ""))
	if len(s) != 32 || !strings.HasPrefix(s, idPrefix) {
		return 0, 0, false
	}
	k, err := strconv.ParseUint(s[8:12], 16, 16)
	if err != nil || k < kindItem || k > kindFile {
		return 0, 0, false
	}
	n, err := strconv.ParseUint(s[12:], 16, 64)
	if err != nil || n > math.MaxInt64 {
		return 0, 0, false
	}
	return int(k), int64(n), true
}
