// Package addonkit is the Go side of the Antresol addon contract 1
// (docs/addons/contract.md in the core): the JSON shapes the core and an
// addon exchange (camelCase keys), the process environment (FromEnv), a
// client of the core API v1 (Client), the hooks the core calls (HookMux),
// the acting profile (UserFrom, RequireAdmin), the address guard for what a
// third party names (BlockedAddr, DialControl, NewGuardedTransport) and
// readiness (Ready). The release manifest tool is cmd/manifest. It
// depends on the standard library only, and is under the MIT License, so
// an addon of any licence can import it.
package addonkit
