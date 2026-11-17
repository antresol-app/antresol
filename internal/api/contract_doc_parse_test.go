package api

import (
	"slices"
	"strings"
	"testing"
)

// The contract doc test's parsers, fed the inputs they must not miss.

func TestParseSharedQuotes(t *testing.T) {
	src := "export const SHARED = {\n" +
		"  // a comment, 'quoted' \"too\"\n" +
		"  react: [\n    'use', \"useId\",\n  ],\n" +
		"  'react-dom': ['createPortal', \"createRoot\"],\n" +
		"  \"react-dom/client\": ['hydrateRoot'],\n" +
		"}\n"
	got, err := parseShared(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"react use", "react useId", "react-dom createPortal", "react-dom createRoot", "react-dom/client hydrateRoot"}
	if !slices.Equal(sorted(got), want) {
		t.Errorf("parseShared = %v, want %v", sorted(got), want)
	}

	for name, body := range map[string]string{
		"a module not a list": "  react: REACT_NAMES,\n",
		"a name not a string": "  react: ['use', useId],\n",
		"a spread":            "  ...more,\n  react: ['use'],\n",
	} {
		if _, err := parseShared("export const SHARED = {\n" + body + "}\n"); err == nil {
			t.Errorf("%s: parseShared read it without an error", name)
		}
	}
}

func TestParseChildEnvForms(t *testing.T) {
	src := `package addons

func (s *Supervisor) childEnv(id, sock, token string) []string {
	contract := []string{
		"ANTRESOL_ADDON_ID=" + id,
		fmt.Sprintf("ANTRESOL_FOO=%s", id),
		"ANTRESOL_BAR=1",
	}
	env := append([]string{}, contract...)
	env = append(env, "ANTRESOL_BAZ=x")
	k, _, _ := strings.Cut(sock, "=")
	_ = strings.HasPrefix(k, "ANTRESOL_")
	return env
}
`
	got, err := parseChildEnv(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ANTRESOL_ADDON_ID", "ANTRESOL_BAR", "ANTRESOL_BAZ", "ANTRESOL_FOO"}
	if !slices.Equal(sorted(got), want) {
		t.Errorf("parseChildEnv = %v, want %v", sorted(got), want)
	}

	unparsed := strings.Replace(src, `"ANTRESOL_BAR=1",`, `envVar("BAR", "1"),`, 1)
	if _, err := parseChildEnv(unparsed); err == nil {
		t.Error("an entry of the contract list that names no variable was read without an error")
	}
}

func TestParseInventoriesDuplicates(t *testing.T) {
	for name, doc := range map[string]string{
		"a line twice":        "```inventory-hooks\nimport.search\nfiles.list\nimport.search\n```\n",
		"a shared name twice": "```inventory-shared\nreact: use, useId\nreact: use\n```\n",
	} {
		if _, errs := parseInventories(doc); len(errs) == 0 {
			t.Errorf("%s: no error", name)
		}
	}
	if _, errs := parseInventories("```inventory-hooks\nimport.search\nfiles.list\n```\n"); len(errs) != 0 {
		t.Errorf("a list without repeats: %v", errs)
	}
}

func TestSignatureDiffWholeIdentifiers(t *testing.T) {
	fields := []string{"kinds", "labels?", "order?", "use"}
	if m, e := signatureDiff("| `episodeActions` | plays: `{ kinds, labels?, order?, use(ctx) }`. |", fields); len(m)+len(e) != 0 {
		t.Errorf("the full signature: missing %v, extra %v", m, e)
	}
	// `use` and `order?` only in the prose, `kinds` only inside another word
	row := "| `episodeActions` | plays sources of the addon's own kinds: `{ kindsList, labels? }`; `use` is a hook; the `order?` sorts. |"
	m, _ := signatureDiff(row, fields)
	if want := []string{"kinds", "order?", "use"}; !slices.Equal(m, want) {
		t.Errorf("missing = %v, want %v", m, want)
	}
	if _, e := signatureDiff("| `episodeActions` | `{ kinds, labels?, order?, use(ctx), extra }` |", fields); !slices.Equal(e, []string{"extra"}) {
		t.Errorf("extra = %v, want [extra]", e)
	}
	if m, _ := signatureDiff("| `episodeActions` | no signature here |", fields); len(m) != len(fields) {
		t.Errorf("a row without a signature: missing %v", m)
	}
}

func TestCheckDegradeBulletsScoped(t *testing.T) {
	const lead = "with none, the core degrades:"
	phrases := []string{"alpha", "beta"}
	full := "### X\n\nThe paragraph says alpha and beta.\n\nNo answerer; **" + lead + "**\n\n- alpha\n  goes on;\n- beta.\n\nAfter: alpha, beta.\n"
	if errs := checkDegradeBullets(full, lead, phrases); len(errs) != 0 {
		t.Errorf("the full list: %v", errs)
	}
	dropped := strings.Replace(full, "- beta.\n", "", 1)
	if errs := checkDegradeBullets(dropped, lead, phrases); len(errs) == 0 {
		t.Error("a bullet deleted, its phrase still in the paragraphs around: no error")
	}
	extra := strings.Replace(full, "- beta.\n", "- beta.\n- gamma.\n", 1)
	if errs := checkDegradeBullets(extra, lead, phrases); len(errs) == 0 {
		t.Error("a bullet no phrase names: no error")
	}
	if errs := checkDegradeBullets("### X\n\n- alpha\n- beta\n", lead, phrases); len(errs) == 0 {
		t.Error("no lead line: no error")
	}
}
