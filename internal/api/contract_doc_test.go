package api

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/antresol-app/antresol/internal/addons"
)

// The contract doc and the code it describes, read by path from this
// package's directory.
const (
	contractDocPath = "../../docs/addons/contract.md"
	supervisorPath  = "../addons/supervisor.go"
	registryPath    = "../../web/src/addons/registry.ts"
	vitePluginPath  = "../../web/addon-sdk/vite-plugin.mjs"
	addonCorePrefix = "/api/addon-core/v1"
)

// inventoryRe: a fenced list of contract.md, ```inventory-<kind> … ```.
var inventoryRe = regexp.MustCompile("(?ms)^```inventory-([a-z-]+)\n(.*?)^```$")

// TestContractDocInventories diffs each fenced inventory of contract.md
// both ways against the code: the core API's routes (chi.Walk over
// mountAddonCore), the hooks (addons.KnownHooks), the registerAddon keys
// (AddonContributions in registry.ts), the SDK's shared names (SHARED in
// vite-plugin.mjs) and the addon's environment (the supervisor's childEnv).
func TestContractDocInventories(t *testing.T) {
	inv := docInventories(t, readFile(t, contractDocPath))
	code := map[string][]string{
		"routes":        contractRoutes(t),
		"hooks":         slices.Clone(addons.KnownHooks),
		"contributions": contributionKeys(t),
		"shared":        sharedNames(t),
		"env":           childEnvNames(t),
	}
	for kind, want := range code {
		got, ok := inv[kind]
		if !ok {
			t.Errorf("contract.md has no ```inventory-%s list; the code has:\n%s", kind, strings.Join(sorted(want), "\n"))
			continue
		}
		if missing, extra := diffSets(want, got); len(missing)+len(extra) > 0 {
			t.Errorf("inventory-%s differs from the code:\n  missing from contract.md: %v\n  in contract.md, not in the code: %v", kind, missing, extra)
		}
	}
	for kind := range inv {
		if _, ok := code[kind]; !ok {
			t.Errorf("contract.md has ```inventory-%s, which no code list checks", kind)
		}
	}
}

// degradeBullets: one bullet each of the list under the degrade line.
var degradeBullets = []string{
	"the file import",
	"«Нет в медиатеке»",
	"`GET /api/import/services` lists no service",
	"`GET /api/import/search` is a 503",
	"`GET /api/import/oauth/<service>/callback` is a 404 «Сервис не подключён»",
}

// TestContractDocLines: lines of contract.md the code relies on an addon
// reading.
func TestContractDocLines(t *testing.T) {
	doc := readFile(t, contractDocPath)

	// the stable profile key, in the proxy's header and in GET /users
	header := section(t, doc, "## The proxy: the app → the addon")
	if !strings.Contains(header, `"uid":"…"`) || !strings.Contains(header, "`uid` is the stable profile key") {
		t.Error("the proxy section does not give X-Antresol-User's uid as the stable profile key")
	}
	// the remote quality cap in the header, and the media prefix a media key opens
	if !strings.Contains(header, "`\"maxHeight\"`") || !regexp.MustCompile(`«Качество\s+вне\s+дома»`).MatchString(header) {
		t.Error("the proxy section does not give X-Antresol-User's maxHeight as «Качество вне дома»")
	}
	if !regexp.MustCompile(`caps the addon's default\s+\(«Авто»\) choice`).MatchString(header) {
		t.Error("the proxy section does not say maxHeight caps only the default («Авто») choice")
	}
	if !strings.Contains(header, "`/api/addons/<id>/x/media/…`") || !strings.Contains(header, "**`GET` or `HEAD`**") ||
		!strings.Contains(header, "must not log or store it") {
		t.Error("the proxy section does not state the media routes' rule (prefix, GET/HEAD, the key not logged or stored)")
	}
	users := tableRow(doc, "| `GET /users` |")
	if !strings.Contains(users, `"uid"`) || !strings.Contains(users, "`uid` is the stable profile key") {
		t.Errorf("the GET /users row does not name uid as the stable profile key: %q", users)
	}

	// core.getTitle may refuse a rating-limited adult
	if !regexp.MustCompile("`core\\.getTitle\\(kind,\\s+tmdbId, signal\\?\\)`[^-]*?may answer 403 for a\\s+rating-limited adult profile").MatchString(doc) {
		t.Error("contract.md does not say core.getTitle may answer 403 for a rating-limited adult profile")
	}

	// episodeActions: every field of EpisodeAction, and order > 0
	row := tableRow(doc, "| `episodeActions` |")
	if row == "" {
		t.Fatal("contract.md has no episodeActions slot row")
	}
	if missing, extra := signatureDiff(row, tsTypeFields(t, readFile(t, registryPath), "EpisodeAction")); len(missing)+len(extra) > 0 {
		t.Errorf("the episodeActions row's signature differs from registry.ts EpisodeAction: missing %v, not in the type %v", missing, extra)
	}
	if !strings.Contains(row, "**> 0**") {
		t.Error("the episodeActions row does not say an order must be > 0")
	}

	// the imports have no first-party answerer yet: the core's degrade path
	const degrade = "no first-party answerer until a later stage; with none, the core degrades:"
	imports := section(t, doc, "### Imports")
	if !strings.Contains(imports, degrade) {
		t.Errorf("«Imports» lacks the line %q", degrade)
	}
	for _, err := range checkDegradeBullets(imports, degrade, degradeBullets) {
		t.Error("«Imports»: ", err)
	}
	hooks := section(t, doc, "### Playlist import: ")
	if !strings.Contains(hooks, degrade) {
		t.Errorf("the import hooks' section lacks the line %q", degrade)
	}

	// "Coming later in contract 1" holds no hook or slot the code has
	if later := sectionOrEmpty(doc, "## Coming later in contract 1"); later != "" {
		for _, name := range append(slices.Clone(addons.KnownHooks), contributionKeys(t)...) {
			if strings.Contains(later, "`"+name+"`") {
				t.Errorf("«Coming later in contract 1» names %q, which the code has", name)
			}
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// docInventories: kind → the lines of its fenced list (blank lines and
// lines starting with # left out). A shared list's line is
// `<module>: <name>, <name>, …`, one entry per name.
func docInventories(t *testing.T, doc string) map[string][]string {
	t.Helper()
	out, errs := parseInventories(doc)
	for _, err := range errs {
		t.Error("contract.md: ", err)
	}
	return out
}

func parseInventories(doc string) (map[string][]string, []error) {
	out := map[string][]string{}
	var errs []error
	for _, m := range inventoryRe.FindAllStringSubmatch(doc, -1) {
		kind := m[1]
		if _, dup := out[kind]; dup {
			errs = append(errs, fmt.Errorf("two ```inventory-%s lists", kind))
		}
		var lines []string
		seen := map[string]bool{}
		add := func(l string) {
			if seen[l] {
				errs = append(errs, fmt.Errorf("inventory-%s lists %q twice", kind, l))
			}
			seen[l] = true
			lines = append(lines, l)
		}
		for _, l := range strings.Split(m[2], "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			if kind == "shared" {
				mod, names, ok := strings.Cut(l, ":")
				if !ok {
					errs = append(errs, fmt.Errorf("inventory-shared line without a module: %q", l))
					continue
				}
				for _, n := range strings.FieldsFunc(names, func(r rune) bool { return r == ',' || r == ' ' }) {
					add(strings.TrimSpace(mod) + " " + n)
				}
				continue
			}
			add(l)
		}
		out[kind] = lines
	}
	return out, errs
}

// contractRoutes: "METHOD /path" under /api/addon-core/v1, as mounted.
func contractRoutes(t *testing.T) []string {
	t.Helper()
	r := chi.NewRouter()
	mountAddonCore(r, &addonsAPI{})
	var out []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		p, ok := strings.CutPrefix(route, addonCorePrefix)
		if !ok {
			t.Errorf("a core API route outside %s: %s %s", addonCorePrefix, method, route)
			return nil
		}
		// chi keeps the trailing slash of a nested Route's own path
		if p != "/" && strings.HasSuffix(p, "/") && !strings.HasSuffix(p, "*/") {
			p = strings.TrimSuffix(p, "/")
		}
		out = append(out, method+" "+p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// contributionKeys: the fields of registry.ts's AddonContributions.
func contributionKeys(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, f := range tsTypeFields(t, readFile(t, registryPath), "AddonContributions") {
		out = append(out, strings.TrimSuffix(f, "?"))
	}
	return out
}

var tsFieldRe = regexp.MustCompile(`^\s{2}(\w+)(\??):`)

// tsTypeFields: the top-level fields (`name` or `name?`) of
// `export type <name> = [AdminOnly &] { … }`, comments skipped.
func tsTypeFields(t *testing.T, src, name string) []string {
	t.Helper()
	head := regexp.MustCompile(`(?m)^export type ` + name + ` = (?:\w+ & )?\{\n`)
	loc := head.FindStringIndex(src)
	if loc == nil {
		t.Fatalf("registry.ts has no type %s", name)
	}
	body, _, ok := strings.Cut(src[loc[1]:], "\n}")
	if !ok {
		t.Fatalf("registry.ts: type %s is not closed", name)
	}
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if m := tsFieldRe.FindStringSubmatch(l); m != nil {
			out = append(out, m[1]+m[2])
		}
	}
	if len(out) == 0 {
		t.Fatalf("registry.ts: type %s has no fields", name)
	}
	return out
}

// SHARED's entries, read one after the other from its start: a module
// key (bare, '…' or "…") with its list, and in the list each name ('…' or
// "…"). What neither reads is an error, not a name left out.
var (
	sharedModRe  = regexp.MustCompile(`^\s*(?:'([^']+)'|"([^"]+)"|([\w$-]+))\s*:\s*\[([^\]]*)\]\s*,?`)
	sharedNameRe = regexp.MustCompile(`^\s*(?:'([^']+)'|"([^"]+)")\s*,?`)
)

// sharedNames: "<module> <name>" for every name of vite-plugin.mjs's SHARED.
func sharedNames(t *testing.T) []string {
	t.Helper()
	out, err := parseShared(readFile(t, vitePluginPath))
	if err != nil {
		t.Fatal("vite-plugin.mjs: ", err)
	}
	return out
}

func parseShared(src string) ([]string, error) {
	start := strings.Index(src, "export const SHARED = {")
	if start < 0 {
		return nil, errors.New("no SHARED")
	}
	body, _, ok := strings.Cut(src[start:], "\n}\n")
	if !ok {
		return nil, errors.New("SHARED is not closed")
	}
	// the entries, but for the opening line and the comment lines
	var b strings.Builder
	for _, l := range strings.Split(body, "\n")[1:] {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			b.WriteString(l + "\n")
		}
	}
	rest := b.String()
	var out []string
	for strings.TrimSpace(rest) != "" {
		m := sharedModRe.FindStringSubmatch(rest)
		if m == nil {
			return nil, fmt.Errorf("SHARED has an entry the test cannot read: %.60q", strings.TrimSpace(rest))
		}
		rest = rest[len(m[0]):]
		mod := m[1] + m[2] + m[3]
		for names := m[4]; strings.TrimSpace(names) != ""; {
			n := sharedNameRe.FindStringSubmatch(names)
			if n == nil {
				return nil, fmt.Errorf("SHARED[%q] has a name the test cannot read: %.60q", mod, strings.TrimSpace(names))
			}
			names = names[len(n[0]):]
			out = append(out, mod+" "+n[1]+n[2])
		}
	}
	if len(out) == 0 {
		return nil, errors.New("SHARED has no names")
	}
	return out, nil
}

// childEnvNames: the contract's variables the supervisor's childEnv sets.
func childEnvNames(t *testing.T) []string {
	t.Helper()
	out, err := parseChildEnv(readFile(t, supervisorPath))
	if err != nil {
		t.Fatal("supervisor.go: ", err)
	}
	return out
}

// envAssignRe: a string literal that sets a variable, "ANTRESOL_X=…" (a
// prefix to concatenate to, a Sprintf format or a full value).
var envAssignRe = regexp.MustCompile(`^(ANTRESOL_[A-Z0-9_]+)=`)

// parseChildEnv: every ANTRESOL_ variable a string literal in childEnv sets
// ("ANTRESOL_X=" + v, fmt.Sprintf("ANTRESOL_X=%s", v), "ANTRESOL_X=1").
// Each entry of its `contract` list must hold one: an entry that does not
// (a helper call, a variable) is an error, not a variable left out.
func parseChildEnv(src string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "supervisor.go", src, 0)
	if err != nil {
		return nil, err
	}
	setBy := func(n ast.Node) []string {
		var names []string
		ast.Inspect(n, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					if m := envAssignRe.FindStringSubmatch(s); m != nil {
						names = append(names, m[1])
					}
				}
			}
			return true
		})
		return names
	}
	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if d, ok := d.(*ast.FuncDecl); ok && d.Name.Name == "childEnv" {
			fn = d
		}
	}
	if fn == nil {
		return nil, errors.New("no func childEnv")
	}
	var list *ast.CompositeLit
	var errs []error
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == "contract" {
			list, _ = as.Rhs[0].(*ast.CompositeLit)
		}
		return true
	})
	if list == nil {
		return nil, errors.New("childEnv has no `contract := []string{…}` list")
	}
	for i, e := range list.Elts {
		if len(setBy(e)) == 0 {
			errs = append(errs, fmt.Errorf("entry %d of childEnv's contract list sets no ANTRESOL_ variable the test can read", i+1))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	out := setBy(fn.Body)
	if len(out) == 0 {
		return nil, errors.New("childEnv sets no ANTRESOL_ variable")
	}
	return out, nil
}

// section: the text from the heading line to the next heading of the same
// or a higher level.
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	s := sectionOrEmpty(doc, heading)
	if s == "" {
		t.Fatalf("contract.md has no section %q", heading)
	}
	return s
}

func sectionOrEmpty(doc, heading string) string {
	i := strings.Index(doc, "\n"+heading)
	if i < 0 {
		return ""
	}
	level := strings.Index(heading, " ")
	rest := doc[i+1:]
	lines := strings.SplitAfter(rest, "\n")
	var b strings.Builder
	b.WriteString(lines[0])
	fenced := false
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "```") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(l, "#") {
			if n := strings.Index(l, " "); n > 0 && n <= level && strings.Trim(l[:n], "#") == "" {
				break
			}
		}
		b.WriteString(l)
	}
	return b.String()
}

// signatureRe: a row's backticked `{ a, b?, c(x) }` signature.
var signatureRe = regexp.MustCompile("`\\{ ([^`{}]*) \\}`")

// signatureDiff: the fields of the row's first `{ … }` signature (a
// method's `(…)` dropped) against the type's fields, as whole names.
func signatureDiff(row string, fields []string) (missing, extra []string) {
	var sig []string
	if m := signatureRe.FindStringSubmatch(row); m != nil {
		for _, f := range strings.Split(m[1], ",") {
			f, _, _ = strings.Cut(strings.TrimSpace(f), "(")
			sig = append(sig, f)
		}
	}
	return diffSets(fields, sig)
}

// checkDegradeBullets: the list right under the lead line has one bullet
// per phrase, each naming its own, and no other bullet. Text around the
// list does not count.
func checkDegradeBullets(sec, lead string, phrases []string) []error {
	_, after, ok := strings.Cut(sec, lead)
	if !ok {
		return []error{fmt.Errorf("no line %q", lead)}
	}
	_, after, _ = strings.Cut(after, "\n") // the rest of the lead's line
	// the list: "- " lines and their indented continuations, up to the
	// first line that is neither
	var bullets []string
	for _, l := range strings.Split(strings.TrimLeft(after, " \n"), "\n") {
		if strings.HasPrefix(l, "- ") {
			bullets = append(bullets, strings.TrimPrefix(l, "- "))
		} else if strings.HasPrefix(l, "  ") && strings.TrimSpace(l) != "" && len(bullets) > 0 {
			bullets[len(bullets)-1] += " " + strings.TrimSpace(l)
		} else {
			break
		}
	}
	var errs []error
	used := make([]bool, len(bullets))
	for _, p := range phrases {
		n := 0
		for i, b := range bullets {
			if strings.Contains(b, p) {
				n++
				used[i] = true
			}
		}
		if n != 1 {
			errs = append(errs, fmt.Errorf("%d of the degrade bullets name %q, want 1", n, p))
		}
	}
	for i, u := range used {
		if !u {
			errs = append(errs, fmt.Errorf("a degrade bullet no check names: %q", bullets[i]))
		}
	}
	if len(bullets) == 0 {
		errs = append(errs, fmt.Errorf("no bullet list under %q", lead))
	}
	return errs
}

// tableRow: the doc's line starting with prefix ("" if none).
func tableRow(doc, prefix string) string {
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// diffSets: what want has that got lacks, and the other way round (each
// sorted; a name given twice counts once).
func diffSets(want, got []string) (missing, extra []string) {
	w, g := map[string]bool{}, map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	for _, s := range got {
		g[s] = true
	}
	for s := range w {
		if !g[s] {
			missing = append(missing, s)
		}
	}
	for s := range g {
		if !w[s] {
			extra = append(extra, s)
		}
	}
	return sorted(missing), sorted(extra)
}
