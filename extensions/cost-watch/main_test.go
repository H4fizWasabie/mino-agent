package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures mirror the escaped-JSON pricing on the live OpenRouter pages.
const fixtureGLM = `<script>window.__DATA__ = "{\"name\":\"StreamLake\",\"pricing\":{\"prompt\":\"0.000000098\",\"completion\":\"0.000000308\",\"input_cache_read\":\"0.0000000182\",\"discount\":0.93,\"display_pricing\":[{\"kind\":\"token\"}]},\"name\":\"Baidu\",\"pricing\":{\"prompt\":\"0.000000112\",\"completion\":\"0.000000352\",\"input_cache_read\":\"0.0000000208\",\"discount\":0.92},\"name\":\"Z.ai\",\"pricing\":{\"prompt\":\"0.00000068\",\"completion\":\"0.00000213\",\"input_cache_read\":\"0.000000126\",\"discount\":0.0}}"</script>`

const fixtureLUNA = `<script>window.__DATA__ = "{\"name\":\"OpenAI\",\"pricing\":{\"prompt\":\"0.0000001\",\"completion\":\"0.0000006\",\"input_cache_read\":\"0.00000001\",\"discount\":0.5},\"name\":\"Azure\",\"pricing\":{\"prompt\":\"0.0000011\",\"completion\":\"0.0000066\",\"input_cache_read\":\"0.00000011\",\"discount\":0.0}}"</script>`

const fixtureQwen = `<script>window.__DATA__ = "{\"name\":\"Qwen\",\"pricing\":{\"prompt\":\"0.00000003\",\"completion\":\"0.00000013\",\"input_cache_read\":\"0.000000006\",\"discount\":0.0}}"</script>`

func approx(a, b float64) bool { return a-b < 0.0001 && b-a < 0.0001 }

func TestParseGLMPricing(t *testing.T) {
	provs := parsePricing(fixtureGLM)
	sl, ok := provs["StreamLake"]
	if !ok || !approx(sl.Input, 0.098) || !approx(sl.Cache, 0.0182) || !approx(sl.Discount, 0.93) {
		t.Fatalf("StreamLake = %+v", sl)
	}
	if !approx(provs["Baidu"].Input, 0.112) {
		t.Fatalf("Baidu = %+v", provs["Baidu"])
	}
	if !approx(provs["Z.ai"].Input, 0.68) {
		t.Fatalf("Z.ai = %+v", provs["Z.ai"])
	}
}

func TestParseLunaPricing(t *testing.T) {
	provs := parsePricing(fixtureLUNA)
	if !approx(provs["OpenAI"].Input, 0.10) || !approx(provs["OpenAI"].Output, 0.60) {
		t.Fatalf("OpenAI = %+v", provs["OpenAI"])
	}
	if !approx(provs["Azure"].Input, 1.10) {
		t.Fatalf("Azure = %+v", provs["Azure"])
	}
}

// luna's live page carries extra pricing keys (input_cache_write, web_search)
// between input_cache_read and discount — the parser must tolerate them.
const fixtureLunaExtra = `<script>window.__DATA__ = "{\"name\":\"OpenAI\",\"pricing\":{\"prompt\":\"0.0000001\",\"completion\":\"0.0000006\",\"input_cache_read\":\"0.00000001\",\"input_cache_write\":\"0.000000125\",\"web_search\":\"0.005\",\"discount\":0.5}}"</script>`

func TestParseLunaExtraKeys(t *testing.T) {
	provs := parsePricing(fixtureLunaExtra)
	if !approx(provs["OpenAI"].Input, 0.10) || !approx(provs["OpenAI"].Cache, 0.01) {
		t.Fatalf("OpenAI = %+v", provs["OpenAI"])
	}
}

func TestBestProvider(t *testing.T) {
	provs := parsePricing(fixtureGLM)
	best := ""
	for p := range provs {
		if best == "" || provs[p].Input < provs[best].Input {
			best = p
		}
	}
	if best != "StreamLake" {
		t.Fatalf("best = %q, want StreamLake", best)
	}
}

func TestDefaultConfigMonitorsPolicyModels(t *testing.T) {
	cfg := defaultConfig()
	// REL-01 policy: paging must cover the actual brain (issue #128).
	want := map[string]float64{
		"deepseek/deepseek-v4-flash-0731:deepinfra": 0.08,
		"qwen/qwen3.7-flash":                        0.03,
	}
	for m, price := range want {
		mc, ok := cfg.Models[m]
		if !ok {
			t.Fatalf("policy model %q not monitored", m)
		}
		if mc.Expected != price {
			t.Fatalf("%s expected = %v, want %v", m, mc.Expected, price)
		}
	}
	if len(cfg.Models) != len(want) {
		t.Fatalf("monitored set = %v, want only the three policy models", cfg.Models)
	}
}

func TestFlagOnPriceSpike(t *testing.T) {
	cfg := defaultConfig()
	orig := fetch
	defer func() { fetch = orig }()
	fetch = func(url string) (string, error) { return fixtureQwen, nil }
	_, flags := checkModels(cfg)
	for _, m := range []string{"deepseek/deepseek-v4-flash-0731:deepinfra", "qwen/qwen3.7-flash"} {
		if flags[m] != "ok" {
			t.Fatalf("%s: expected ok, got %q", m, flags[m])
		}
	}
	// Promo dies: the discounted host drops to the full price.
	fetch = func(url string) (string, error) {
		s := strings.ReplaceAll(fixtureQwen, "0.00000003", "0.0000006")
		return s, nil
	}
	_, flags2 := checkModels(cfg)
	if !strings.Contains(flags2["qwen/qwen3.7-flash"], "PRICE SPIKE") {
		t.Fatalf("spike not detected: %q", flags2["qwen/qwen3.7-flash"])
	}
}

func TestExecuteRejectsModelSwap(t *testing.T) {
	// Alert-only policy (REL-01, #128): no tool may change the brain.
	cfg := defaultConfig()
	orig := fetch
	defer func() { fetch = orig }()
	fetch = func(url string) (string, error) { return fixtureLUNA, nil }

	for _, tool := range []string{"cost_watch_swap", "swap_model", "swap"} {
		if _, err := executeTool(cfg, tool, map[string]any{"model": "qwen/qwen3.7-flash"}); err == nil {
			t.Fatalf("%s must be rejected", tool)
		}
	}
	for _, tool := range toolSchemas() {
		if name, _ := tool["name"].(string); name == "cost_watch_swap" {
			t.Fatal("cost_watch_swap must not be advertised in /tools")
		}
	}
	if _, err := executeTool(cfg, "cost_watch_status", nil); err != nil {
		t.Fatalf("status tool should still work: %v", err)
	}
}

func TestCheckFlagsSpikeWithoutTouchingProviders(t *testing.T) {
	cfg := defaultConfig()
	orig := fetch
	defer func() { fetch = orig }()
	fetch = func(url string) (string, error) {
		s := strings.ReplaceAll(fixtureQwen, "0.00000003", "0.0000006")
		return s, nil
	}
	home := t.TempDir()
	providers := filepath.Join(home, "providers.json")
	os.WriteFile(providers, []byte(`{"providers":[{"name":"unchanged"}]}`), 0o600)
	before, _ := os.ReadFile(providers)
	_, flags := checkModels(cfg)
	if !strings.Contains(flags["qwen/qwen3.7-flash"], "PRICE SPIKE") {
		t.Fatalf("spike not flagged: %q", flags["qwen/qwen3.7-flash"])
	}
	after, _ := os.ReadFile(providers)
	if string(before) != string(after) {
		t.Fatal("a price spike must never rewrite providers.json")
	}
}

// CTX-020: catalogue targets derive from the user's providers.json — only
// OpenRouter-hosted slugs (contain "/"); direct-API models have no endpoints
// listing and are skipped. Nothing hardcoded.
func TestConfiguredOpenRouterModels(t *testing.T) {
	old := providersPath
	defer func() { providersPath = old }()
	providersPath = filepath.Join(t.TempDir(), "providers.json")
	os.WriteFile(providersPath, []byte(`{"providers":[
		{"model":"deepseek/deepseek-v4-flash-0731"},
		{"model":"qwen/qwen3.7-flash"},
		{"model":"direct-api-model"}
	]}`), 0644)
	models, err := configuredOpenRouterModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "deepseek/deepseek-v4-flash-0731" || models[1] != "qwen/qwen3.7-flash" {
		t.Fatalf("got %v, want only the two openrouter slugs", models)
	}
}

// CTX-020: the catalogue persists and round-trips with the data-handling flag.
func TestSaveCatalogueRoundTrip(t *testing.T) {
	old := cataloguePath
	defer func() { cataloguePath = old }()
	cataloguePath = filepath.Join(t.TempDir(), "cost-catalogue.json")
	cat := catalogue{ScrapedAt: "2026-08-13T00:00:00Z", Entries: []catalogueEntry{
		{Model: "deepseek/deepseek-v4-flash-0731", Provider: "DeepInfra", In: 0.08, Out: 0.18, DataHandling: "zdr"},
		{Model: "deepseek/deepseek-v4-flash-0731", Provider: "DeepSeek", In: 0.14, Out: 0.28, DataHandling: "trains"},
	}}
	if err := saveCatalogue(cat); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cataloguePath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "DeepInfra") || !strings.Contains(s, "zdr") || !strings.Contains(s, "trains") {
		t.Fatalf("catalogue file wrong: %s", s)
	}
}

func TestDefaultConfigCuratesBaiduCacheOnly(t *testing.T) {
	cfg := defaultConfig()
	if cfg.DataHandling["Baidu"] != "cache_only" {
		t.Fatalf("Baidu not curated cache_only: %q", cfg.DataHandling["Baidu"])
	}
	// A live config map must not clobber the default vetting.
	dir := t.TempDir()
	oldCfg := configPath
	defer func() { configPath = oldCfg }()
	configPath = filepath.Join(dir, "cost-watch.json")
	os.WriteFile(configPath, []byte(`{"data_handling":{"DeepInfra":"zdr"}}`), 0600)
	cfg = loadConfig()
	if cfg.DataHandling["Baidu"] != "cache_only" {
		t.Fatalf("default Baidu curation lost by merge: %q", cfg.DataHandling["Baidu"])
	}
	if cfg.DataHandling["DeepInfra"] != "zdr" {
		t.Fatalf("custom curation lost: %q", cfg.DataHandling["DeepInfra"])
	}
}

func TestPinOrderExcludesTrains(t *testing.T) {
	cfg := defaultConfig()
	cfg.DataHandling["DeepSeek"] = "trains"
	cfg.DataHandling["DeepInfra"] = "zdr"
	cat := []catalogueEntry{
		{Model: "m", Provider: "DeepSeek", In: 0.01, DataHandling: "trains"}, // cheapest but banned
		{Model: "m", Provider: "Baidu", In: 0.08, DataHandling: "cache_only"},
		{Model: "m", Provider: "DeepInfra", In: 0.14, DataHandling: "zdr"},
	}
	order := pinOrder(cfg, cat)
	if len(order) != 2 || order[0] != "Baidu" || order[1] != "DeepInfra" {
		t.Fatalf("order = %v, want [Baidu DeepInfra] (trains never appears)", order)
	}
}

func TestPinOrderCapsAtMaxPins(t *testing.T) {
	cfg := defaultConfig()
	var cat []catalogueEntry
	for i := 0; i < 10; i++ {
		cat = append(cat, catalogueEntry{Model: "m", Provider: fmt.Sprintf("P%d", i), In: float64(i), DataHandling: "unknown"})
	}
	if order := pinOrder(cfg, cat); len(order) != maxPins {
		t.Fatalf("order = %d entries, want %d", len(order), maxPins)
	}
}

func TestApplyPinsRewritesRoutingAndBacksUp(t *testing.T) {
	old := providersPath
	defer func() { providersPath = old }()
	providersPath = filepath.Join(t.TempDir(), "providers.json")
	orig := []byte(`{"providers":[
		{"name":"openrouter","priority":1,"base_url":"https://openrouter.ai/api/v1","api_key_env":"MINO_OPENROUTER_KEY","model":"deepseek/deepseek-v4-flash-0731:deepinfra","small_model":"deepseek/deepseek-v4-flash-0731:deepinfra","text_only":true,"provider_routing":["DeepInfra"],"small_provider_routing":["DeepInfra"]},
		{"name":"qwen-fallback","priority":2,"base_url":"https://openrouter.ai/api/v1","api_key_env":"MINO_OPENROUTER_KEY","model":"qwen/qwen3.7-flash"}
	]}`)
	os.WriteFile(providersPath, orig, 0644)

	cfg := defaultConfig()
	cfg.DataHandling["DeepSeek"] = "trains"
	cfg.DataHandling["DeepInfra"] = "zdr"
	cat := catalogue{Entries: []catalogueEntry{
		{Model: "deepseek/deepseek-v4-flash-0731", Provider: "DeepSeek", In: 0.01, DataHandling: "trains"},
		{Model: "deepseek/deepseek-v4-flash-0731", Provider: "Baidu", In: 0.0798, DataHandling: "cache_only"},
		{Model: "deepseek/deepseek-v4-flash-0731", Provider: "DeepInfra", In: 0.08, DataHandling: "zdr"},
		{Model: "qwen/qwen3.7-flash", Provider: "Qwen", In: 0.03},
	}}
	changed, summary, err := applyPins(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a pin change")
	}
	if !strings.Contains(summary, "openrouter") || strings.Contains(summary, "qwen-fallback") {
		t.Fatalf("summary = %q: touched openrouter only, never the unpinned fallback", summary)
	}
	data, _ := os.ReadFile(providersPath)
	s := string(data)
	if strings.Contains(s, ":deepinfra") {
		t.Fatalf("stale :suffix survived: %s", s)
	}
	// DeepSeek is cheapest but trains — Baidu must lead, DeepSeek must not appear.
	var got struct {
		Providers []struct {
			Name                 string   `json:"name"`
			ProviderRouting      []string `json:"provider_routing"`
			SmallProviderRouting []string `json:"small_provider_routing"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != 2 || got.Providers[0].Name != "openrouter" {
		t.Fatalf("providers = %+v", got.Providers)
	}
	want := []string{"Baidu", "DeepInfra"}
	p := got.Providers[0]
	if len(p.ProviderRouting) != 2 || p.ProviderRouting[0] != "Baidu" || p.ProviderRouting[1] != "DeepInfra" {
		t.Fatalf("routing = %v, want %v", p.ProviderRouting, want)
	}
	if len(p.SmallProviderRouting) != 2 || p.SmallProviderRouting[0] != "Baidu" || p.SmallProviderRouting[1] != "DeepInfra" {
		t.Fatalf("small routing = %v, want %v", p.SmallProviderRouting, want)
	}
	if strings.Contains(s, "DeepSeek") {
		t.Fatalf("trains provider leaked into providers.json: %s", s)
	}
	if len(got.Providers[1].ProviderRouting) != 0 {
		t.Fatalf("qwen-fallback must stay unpinned: %+v", got.Providers[1])
	}
	bak, err := os.ReadFile(providersPath + ".bak-pin")
	if err != nil || string(bak) != string(orig) {
		t.Fatalf("backup missing or wrong: %v", err)
	}
}

func TestApplyPinsIdempotent(t *testing.T) {
	old := providersPath
	defer func() { providersPath = old }()
	providersPath = filepath.Join(t.TempDir(), "providers.json")
	orig := []byte(`{"providers":[{"name":"openrouter","model":"m","provider_routing":["A"]}]}`)
	os.WriteFile(providersPath, orig, 0644)
	cfg := defaultConfig()
	cat := catalogue{Entries: []catalogueEntry{{Model: "m", Provider: "A", In: 0.1}}}
	changed, _, err := applyPins(cfg, cat)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unchanged order must not rewrite providers.json")
	}
	data, _ := os.ReadFile(providersPath)
	if string(data) != string(orig) {
		t.Fatalf("file rewritten anyway: %s", data)
	}
}

func TestFetchCatalogueParsesDiscount(t *testing.T) {
	old := providersPath
	defer func() { providersPath = old }()
	providersPath = filepath.Join(t.TempDir(), "providers.json")
	os.WriteFile(providersPath, []byte(`{"providers":[{"model":"deepseek/deepseek-v4-flash-0731:deepinfra"}]}`), 0644)
	orig := fetch
	defer func() { fetch = orig }()
	fetch = func(url string) (string, error) {
		return `{"data":{"endpoints":[{"name":"Baidu | deepseek","pricing":{"prompt":"0.0000000798","completion":"0.0000001596","input_cache_read":"0.00000001596","discount":0.43}}]}}`, nil
	}
	cat, err := fetchCatalogue(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Entries) != 1 || cat.Entries[0].Provider != "Baidu" {
		t.Fatalf("entries = %+v", cat.Entries)
	}
	e := cat.Entries[0]
	if !approx(e.In, 0.0798) || !approx(e.Discount, 0.43) {
		t.Fatalf("price/discount = %+v", e)
	}
}

// Every tool executeTool can dispatch must be advertised in the extension's
// /tools schema — a tool that exists but is not advertised is unreachable by
// Mino (the cost_watch_refresh gap found live on the VPS, 2026-08-14).
func TestToolSchemasAdvertiseAllDispatchableTools(t *testing.T) {
	dispatchable := map[string]bool{
		"cost_watch_status":  false,
		"cost_watch_check":   false,
		"cost_watch_refresh": false,
	}
	for _, s := range toolSchemas() {
		if _, ok := dispatchable[s["name"].(string)]; ok {
			dispatchable[s["name"].(string)] = true
		}
	}
	for name, advertised := range dispatchable {
		if !advertised {
			t.Errorf("tool %q is dispatchable via executeTool but missing from /tools schema", name)
		}
	}
}

// The config must load from the mino-writable location first; /etc is the
// legacy fallback only (CTX-020 hot-reload: the model edits its own watchdog).
func TestLoadConfigPrefersMinoHomeOverLegacy(t *testing.T) {
	oldCfg, oldLegacy := configPath, legacyConfigPath
	defer func() { configPath, legacyConfigPath = oldCfg, oldLegacy }()

	dir := t.TempDir()
	homeCfg := filepath.Join(dir, "home.json")
	legacy := filepath.Join(dir, "legacy.json")
	os.WriteFile(homeCfg, []byte(`{"catalogue_refresh_minutes": 7}`), 0600)
	os.WriteFile(legacy, []byte(`{"catalogue_refresh_minutes": 99}`), 0600)

	configPath, legacyConfigPath = homeCfg, legacy
	if cfg := loadConfig(); cfg.CatalogueRefreshMin != 7 {
		t.Fatalf("home config not preferred: refresh=%d", cfg.CatalogueRefreshMin)
	}

	// Home missing -> legacy fallback.
	configPath = filepath.Join(dir, "missing.json")
	if cfg := loadConfig(); cfg.CatalogueRefreshMin != 99 {
		t.Fatalf("legacy fallback not used: refresh=%d", cfg.CatalogueRefreshMin)
	}

	// Both missing -> defaults (0 = unset; the refresh loop applies 60 at use time).
	legacyConfigPath = filepath.Join(dir, "missing2.json")
	if cfg := loadConfig(); cfg.CatalogueRefreshMin != 0 {
		t.Fatalf("defaults not used: refresh=%d", cfg.CatalogueRefreshMin)
	}
}
