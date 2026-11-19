package iptv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/channels"
)

// variantTruth: what a careful model answers about the fixture's
// look-alikes (testdata/variants_ru.m3u): the channel each name is, and
// its kind. A name not here is a channel of its own.
var variantTruth = map[string][2]string{
	"Россия 1":             {"r1", kindMain},
	"Россия 1 HD":          {"r1", kindQuality},
	"Россия 1 FHD":         {"r1", kindQuality},
	"Россия 1 UHD":         {"r1", kindQuality},
	"Россия 1 (Москва)":    {"r1", kindRegional},
	"Россия 1 Москва":      {"r1", kindRegional},
	"Rossiya 1":            {"r1", kindQuality},
	"Россия 1 Владивосток": {"r1", kindTimeshift},
	"Первый канал":         {"p1", kindMain},
	"Первый канал HD":      {"p1", kindQuality},
	"Pervyi kanal":         {"p1", kindQuality},
	"НТВ":                  {"ntv", kindMain},
	"НТВ HD":               {"ntv", kindQuality},
	"СТС":                  {"sts", kindMain},
	"СТС HD":               {"sts", kindQuality},
	"ТНТ":                  {"tnt", kindMain},
	"TNT":                  {"tnt", kindQuality},
	"Рен ТВ":               {"ren", kindMain},
	"REN TV":               {"ren", kindQuality},
	"Дом кино Премиум":     {"dkp", kindMain},
	"Дом кино Премиум HD":  {"dkp", kindQuality},
}

// variantModel is a stub OpenAI-compatible endpoint answering the variants
// question from variantTruth; it keeps every request.
type variantModel struct {
	mu     sync.Mutex
	asked  []string // the user messages
	format []any    // the response_format of each
	fail   bool     // answer 500
	broken bool     // answer one entry only
}

var askLine = regexp.MustCompile(`^(\d+)\. (.+?)(?: \| group: .*?)?(?: \| tvg-id: .*)?$`)

func (v *variantModel) serve(t *testing.T) *channels.LLM {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model          string
			Messages       []struct{ Content string }
			ResponseFormat any `json:"response_format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		user := body.Messages[len(body.Messages)-1].Content
		v.mu.Lock()
		v.asked = append(v.asked, user)
		v.format = append(v.format, body.ResponseFormat)
		fail, broken := v.fail, v.broken
		v.mu.Unlock()
		if fail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		var entries []map[string]any
		nums := map[string]int{}
		for _, line := range strings.Split(user, "\n") {
			if strings.HasPrefix(line, "Cluster ") {
				nums = map[string]int{}
				continue
			}
			m := askLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			tr, ok := variantTruth[m[2]]
			if !ok {
				tr = [2]string{"own:" + m[2], kindMain}
			}
			if nums[tr[0]] == 0 {
				nums[tr[0]] = len(nums) + 1
			}
			var n int
			fmt.Sscan(m[1], &n)
			entries = append(entries, map[string]any{"entry": n, "channel": nums[tr[0]], "kind": tr[1]})
		}
		if broken {
			entries = entries[:1]
		}
		b, _ := json.Marshal(map[string]any{"entries": entries})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(b)}}}})
	}))
	t.Cleanup(srv.Close)
	return &channels.LLM{URL: srv.URL + "/v1", Model: "medium-model"}
}

func (v *variantModel) calls() int { v.mu.Lock(); defer v.mu.Unlock(); return len(v.asked) }

func fixture(t *testing.T) string {
	b, err := os.ReadFile("testdata/variants_ru.m3u")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// groupsOf: the source's groups as «best|next|…» lines, and how each was made.
func (e *svcEnv) groupsOf(src Source) (string, map[string]string) {
	e.t.Helper()
	ctx := context.Background()
	ms, err := e.svc.Store.Members(ctx, src.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	info, err := e.svc.Variants(ctx, src, ms)
	if err != nil {
		e.t.Fatal(err)
	}
	name := map[int64]string{}
	for _, m := range ms {
		name[m.ID] = m.Name
	}
	var lines []string
	how := map[string]string{}
	done := map[int64]bool{}
	for _, m := range ms {
		g, ok := info[m.ID]
		if !ok || done[g.Members[0]] {
			continue
		}
		done[g.Members[0]] = true
		var ns []string
		for _, id := range g.Members {
			ns = append(ns, name[id])
		}
		lines = append(lines, strings.Join(ns, "|"))
		how[name[g.Members[0]]] = g.How
	}
	return strings.Join(lines, "\n"), how
}

func (e *svcEnv) member(src Source, name string) Member {
	e.t.Helper()
	ms, err := e.svc.Store.Members(context.Background(), src.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range ms {
		if m.Name == name {
			return m
		}
	}
	e.t.Fatalf("no member %q", name)
	return Member{}
}

const rulesOnly = `Россия 1 FHD|Россия 1 HD|Россия 1|Россия 1 (Москва)
Россия 24 HD|Россия 24
Первый канал HD|Первый канал
НТВ HD|НТВ
СТС HD|СТС
Матч ТВ HD|Матч ТВ
Пятница! HD|Пятница!
Дом кино Премиум HD|Дом кино Премиум
Disney|Disney Junior
Звезда HD orig|Звезда
Euronews HD|Euronews
Viasat History HD|Viasat History`

const withAI = `Россия 1 FHD|Россия 1 HD|Россия 1|Rossiya 1|Россия 1 (Москва)|Россия 1 Москва
Россия 24 HD|Россия 24
Первый канал HD|Первый канал|Pervyi kanal
НТВ HD|НТВ
СТС HD|СТС
ТНТ|TNT
Рен ТВ|REN TV
Матч ТВ HD|Матч ТВ
Пятница! HD|Пятница!
Дом кино Премиум HD|Дом кино Премиум
Звезда HD orig|Звезда
Euronews HD|Euronews
Viasat History HD|Viasat History`

func TestVariantsAIOnARussianPlaylist(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.p.put("/ru.m3u", fixture(t))
	src := e.source("RU", "/ru.m3u", func(s *Source) { s.PresetVariants = true })
	e.refresh(src.ID)
	// no AI helper: the rules, as before (Disney Junior shares Disney's tvg-id)
	if got, _ := e.groupsOf(src); got != rulesOnly {
		t.Fatalf("rules:\n%s\nwant:\n%s", got, rulesOnly)
	}
	e.svc.AskVariants(ctx) // no model: nothing asked, nothing breaks

	vm := &variantModel{}
	l := vm.serve(t)
	var tiers []channels.Tier
	e.svc.Model = func(_ context.Context, tier channels.Tier) *channels.LLM { tiers = append(tiers, tier); return l }
	e.svc.AskVariants(ctx)
	if vm.calls() != 1 || !slices.Equal(tiers, []channels.Tier{channels.TierMedium}) {
		t.Fatalf("calls %d tiers %v", vm.calls(), tiers)
	}
	f, _ := json.Marshal(vm.format[0])
	if !strings.Contains(string(f), `"type":"json_schema"`) || !strings.Contains(string(f), `"strict":true`) {
		t.Errorf("response_format: %s", f)
	}
	// only the look-alikes are asked about: not Россия 24, not Матч ТВ
	q := vm.asked[0]
	for _, n := range []string{"Rossiya 1", "Россия 1 Владивосток", "НТВ Мир", "Disney Junior", "Первый канал Европа", "Дом кино"} {
		if !strings.Contains(q, ". "+n) {
			t.Errorf("not asked about %q", n)
		}
	}
	for _, n := range []string{"Россия 24", "Матч ТВ", "Euronews", "Россия 1 +2", "Кино XXX"} {
		if strings.Contains(q, ". "+n+"\n") || strings.Contains(q, ". "+n+" |") {
			t.Errorf("asked about %q", n)
		}
	}
	got, how := e.groupsOf(src)
	if got != withAI {
		t.Fatalf("with AI:\n%s\nwant:\n%s", got, withAI)
	}
	if how["Россия 1 FHD"] != VariantByAI || how["Россия 24 HD"] != VariantByRules {
		t.Errorf("how: %v", how)
	}
	// the far-east feed is a timeshift copy: hidden as one
	if m := e.member(src, "Россия 1 Владивосток"); m.Shown || m.Why != WhyTimeshift {
		t.Errorf("Владивосток: shown %v why %q", m.Shown, m.Why)
	}
	// a variant the rules missed is a fallback of its channel
	lead := e.member(src, "Россия 1 FHD")
	if m := e.member(src, "Rossiya 1"); m.Why != WhyVariant || m.ChannelID != lead.ChannelID {
		t.Errorf("Rossiya 1: why %q row %d, want a variant on %d", m.Why, m.ChannelID, lead.ChannelID)
	}
	if a, b := e.member(src, "Disney"), e.member(src, "Disney Junior"); a.ChannelID == b.ChannelID || !b.Shown {
		t.Errorf("Disney Junior still a variant of Disney")
	}

	// asked once: again, after a restart, after a refresh of the same playlist
	e.svc.AskVariants(ctx)
	svc2 := &Service{Store: e.svc.Store, Seal: e.svc.Seal, Fetch: e.svc.Fetch, Now: e.svc.Now, Model: e.svc.Model}
	svc2.AskVariants(ctx)
	if _, err := svc2.RefreshSource(ctx, src.ID, true); err != nil {
		t.Fatal(err)
	}
	svc2.AskVariants(ctx)
	if vm.calls() != 1 {
		t.Fatalf("asked again: %d", vm.calls())
	}
	e.svc = svc2
	if got, _ := e.groupsOf(src); got != withAI {
		t.Errorf("after restart:\n%s", got)
	}

	// a playlist change asks about the changed cluster only
	e.p.put("/ru.m3u", fixture(t)+"#EXTINF:-1 group-title=\"Эфирные\",Россия 1 UHD\nhttp://stream.example.test/live/999.m3u8\n")
	e.refresh(src.ID)
	e.svc.AskVariants(ctx)
	if vm.calls() != 2 {
		t.Fatalf("calls after a change: %d", vm.calls())
	}
	if q := vm.asked[1]; !strings.Contains(q, "Россия 1 UHD") || strings.Contains(q, "НТВ") {
		t.Errorf("second question:\n%s", q)
	}
	var keys int
	if err := e.svc.Store.DB.QueryRow(`SELECT COUNT(*) FROM iptv_variant_ai`).Scan(&keys); err != nil || keys != 8 {
		t.Errorf("cached clusters %d (%v), want 8 (the old Россия 1 one dropped)", keys, err)
	}

	// the admin's word wins over both
	if err := e.svc.SetVariant(ctx, e.member(src, "Rossiya 1"), VariantOwn); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetVariant(ctx, e.member(src, "Disney Junior"), VariantWith+e.member(src, "Disney").Ident); err != nil {
		t.Fatal(err)
	}
	got, how = e.groupsOf(src)
	if !strings.Contains(got, "Disney|Disney Junior") || how["Disney"] != VariantByAdmin || strings.Contains(got, "Rossiya 1") {
		t.Errorf("after the admin:\n%s\n%v", got, how)
	}
	if m := e.member(src, "Rossiya 1"); !m.Shown || m.Why != WhySourceDefault {
		t.Errorf("Rossiya 1 on its own: shown %v why %q", m.Shown, m.Why)
	}
	if m := e.member(src, "Disney Junior"); m.Why != WhyVariant || m.ChannelID != e.member(src, "Disney").ChannelID {
		t.Errorf("Disney Junior: why %q", m.Why)
	}
	if err := e.svc.SetVariant(ctx, e.member(src, "Rossiya 1"), ""); err != nil {
		t.Fatal(err)
	}
	if m := e.member(src, "Rossiya 1"); m.Manual != nil || m.Why != WhyVariant {
		t.Errorf("Rossiya 1 back to automatic: manual %+v why %q", m.Manual, m.Why)
	}
}

// A model that fails waits aiRetry; one that answers unusably for a
// cluster leaves it to the rules and is not asked about it again.
func TestVariantsAIFailures(t *testing.T) {
	e := newSvcEnv(t)
	ctx := context.Background()
	e.p.put("/ru.m3u", fixture(t))
	src := e.source("RU", "/ru.m3u", func(s *Source) { s.PresetVariants = true })
	e.refresh(src.ID)
	vm := &variantModel{fail: true}
	l := vm.serve(t)
	e.svc.Model = func(context.Context, channels.Tier) *channels.LLM { return l }
	e.svc.AskVariants(ctx)
	e.svc.AskVariants(ctx)
	if vm.calls() != 1 {
		t.Fatalf("calls %d: a failure should wait", vm.calls())
	}
	if got, _ := e.groupsOf(src); got != rulesOnly {
		t.Fatalf("after a failure:\n%s", got)
	}
	e.now = e.now.Add(aiRetry + 1)
	vm.mu.Lock()
	vm.fail, vm.broken = false, true
	vm.mu.Unlock()
	e.svc.AskVariants(ctx)
	e.svc.AskVariants(ctx)
	if vm.calls() != 2 {
		t.Fatalf("calls %d", vm.calls())
	}
	if got, _ := e.groupsOf(src); got != rulesOnly {
		t.Errorf("an unusable answer changed the groups:\n%s", got)
	}
}

func TestLookAlikes(t *testing.T) {
	by := map[string]int{"россия 1": 0, "россия 1 москва": 1, "россия 24": 2, "rossiya 1": 3, "russia 1": 4, "нтв": 5, "нтв мир": 6, "мир": 7, "мир 24": 8, "стс": 9, "tnt": 10, "тнт": 11, "тнт4": 12}
	var got []string
	for _, p := range lookAlikes(by) {
		got = append(got, fmt.Sprint(p[0], "-", p[1]))
	}
	want := "0-1 0-3 0-4 5-6 10-11"
	if strings.Join(got, " ") != want {
		t.Errorf("pairs %v, want %s", got, want)
	}
}
