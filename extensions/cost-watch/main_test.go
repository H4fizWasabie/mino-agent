package main

import (
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
