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

func TestFlagOnPriceSpike(t *testing.T) {
	cfg := defaultConfig()
	orig := fetch
	defer func() { fetch = orig }()
	fetch = func(url string) (string, error) { return fixtureGLM, nil }
	_, flags := checkModels(cfg)
	if flags["glm-5.2"] != "ok" {
		t.Fatalf("expected ok, got %q", flags["glm-5.2"])
	}
	// Promo dies: both discounted hosts drop to the full price.
	fetch = func(url string) (string, error) {
		s := strings.ReplaceAll(fixtureGLM, "0.000000098", "0.00000068")
		s = strings.ReplaceAll(s, "0.000000112", "0.00000068")
		return s, nil
	}
	_, flags2 := checkModels(cfg)
	if !strings.Contains(flags2["glm-5.2"], "PRICE SPIKE") {
		t.Fatalf("spike not detected: %q", flags2["glm-5.2"])
	}
}

func TestSwapWritesProvidersAndBacksUp(t *testing.T) {
	cfg := defaultConfig()
	cfg.Templates = map[string][]interface{}{
		"luna-pro": {
			map[string]any{"name": "openrouter", "priority": 1, "model": "openai/gpt-5.6-luna-pro"},
		},
	}
	dir := t.TempDir()
	origProv := providersPath
	origLocks := runLocksDir
	defer func() { providersPath, runLocksDir = origProv, origLocks }()
	providersPath = filepath.Join(dir, "providers.json")
	runLocksDir = filepath.Join(dir, "run-locks") // nonexistent = no locks
	os.WriteFile(providersPath, []byte(`{"providers":[{"name":"old","priority":1}]}`), 0o600)
	result := swapModel(cfg, "luna-pro")
	if !strings.Contains(result, "swapped to luna-pro") {
		t.Fatalf("result = %q", result)
	}
	if _, err := os.Stat(providersPath + ".bak-cost-watch"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	data, _ := os.ReadFile(providersPath)
	if !strings.Contains(string(data), "openai/gpt-5.6-luna-pro") {
		t.Fatalf("providers.json not rewritten: %s", data)
	}
}
