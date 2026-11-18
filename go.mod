module github.com/antresol-app/antresol

go 1.27.1

require (
	github.com/antresol-app/antresol/addonkit v0.0.0
	github.com/bodgit/sevenzip v1.6.5
	github.com/dhowden/tag v0.0.0-20240417053706-3d75831295e8
	github.com/go-chi/chi/v5 v5.5.0
	github.com/klauspost/compress v1.19.0
	github.com/nwaples/rardecode/v2 v2.4.5
	github.com/stangelandcl/ppmd v0.1.1
	github.com/ulikunitz/xz v0.5.15
	golang.org/x/crypto v0.58.0
	golang.org/x/image v0.47.0
	golang.org/x/sync v0.24.0
	golang.org/x/text v0.43.0
	modernc.org/sqlite v1.61.0
)

require (
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/anthropics/anthropic-sdk-go/v2 v2.5.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	go4.org v0.0.0-20260112195520-a5071408f32f // indirect
	golang.org/x/sys v0.49.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/bodgit/sevenzip => ./third_party/sevenzip

replace github.com/antresol-app/antresol/addonkit => ./addonkit
