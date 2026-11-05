// Package antresol carries the licence texts into the binary, so a server
// hands them to its users with its source offer (GET /api/about).
package antresol

import _ "embed"

// License is the server's licence (AGPL-3.0-or-later), the LICENSE file.
//
//go:embed LICENSE
var License string

// ThirdPartyLicenses are the notices of the code the server and its web
// app contain, THIRD_PARTY_LICENSES.md.
//
//go:embed THIRD_PARTY_LICENSES.md
var ThirdPartyLicenses string

// SPDX is the server's licence expression.
const SPDX = "AGPL-3.0-or-later"
