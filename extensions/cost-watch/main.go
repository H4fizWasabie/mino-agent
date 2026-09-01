// mino cost-watch — the price guardian.
//
// Scrapes OpenRouter per-provider pricing, exposes the mino extension protocol
// (docs/decisions.md §8), runs an hourly autonomous check that re-pins the
// brain's routing to the best-ranked eligible hosts across input, cache-read,
// and output prices (curated `trains` providers are hard-excluded) and signals mino to hot-reload
// via SIGHUP — silent, no owner paging. The promo-expiry pager stays for the
// case pinning cannot fix: every eligible host above threshold.
//
//	GET  /tools    -> [{"name": "...", "schema": {...}}]
//	POST /execute  -> {"tool": "...", "args": {...}} -> {"result": "..."}
//	GET  /check    -> {"alert": bool, "message": "..."}
//
// Single static binary — no runtime dependencies (issue #47).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	minoEnvPath = "/home/mino/.mino/mino.env"
	ua          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"
)

// vars so tests can point them at temp dirs
var (
	// CTX-020 hot-reload: the config must live where the mino user (and thus
	// the model) can write it — a root-owned /etc file makes "the model edits
	// its own watchdog" dead on arrival. Legacy installs fall back to /etc.
	configPath       = "/home/mino/.mino/cost-watch.json"
	legacyConfigPath = "/etc/mino-cost-watch.json"
	providersPath    = "/home/mino/.mino/providers.json"
	runLocksDir      = "/home/mino/.mino/run-locks"
	cataloguePath    = "/home/mino/.mino/cost-catalogue.json"
)

type modelConfig struct {
	URL       string  `json:"url"`
	Expected  float64 `json:"expected"`
	Threshold float64 `json:"threshold"`
	// PinMetric is retained for config compatibility; ranking uses all prices.
	PinMetric string `json:"pin_metric,omitempty"`
}

type config struct {
	Listen              string                 `json:"listen"`
	Port                int                    `json:"port"`
	Models              map[string]modelConfig `json:"models"`
	PinMetric           string                 `json:"pin_metric,omitempty"` // legacy compatibility; ranking uses all prices
	Telegram            string                 `json:"telegram_chat_id"`
	CatalogueRefreshMin int                    `json:"catalogue_refresh_minutes"` // 0 = default 60
	DataHandling        map[string]string      `json:"data_handling"`             // provider -> zdr|trains|unknown
}

// catalogueEntry is one provider's price for a model (CTX-020). In/Out are
// USD per 1M tokens; Discount is the endpoint's promo fraction (0.43 = 43% off
// list, from the endpoints API); DataHandling is curated
// (zdr|cache_only|trains|unknown), never scraped.
type catalogueEntry struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	In           float64 `json:"in"`
	Out          float64 `json:"out"`
	Cache        float64 `json:"cache"`
	Discount     float64 `json:"discount"`
	Uptime       float64 `json:"uptime"`
	Latency      float64 `json:"latency"`
	LatencyKnown bool    `json:"latency_known"`
	DataHandling string  `json:"data_handling"`
	Quantization string  `json:"quantization"` // e.g. "fp8", "fp4", "unknown" — from the endpoints API
}

type catalogue struct {
	ScrapedAt string           `json:"scraped_at"`
	Entries   []catalogueEntry `json:"entries"`
}

func defaultConfig() *config {
	// The REL-01 policy models (issue #128): promo-expiry and price-spike
	// paging only covers the actual brain. Expected prices mirror cost.go.
	return &config{
		Listen: "127.0.0.1",
		Port:   9300,
		Models: map[string]modelConfig{
			"deepseek/deepseek-v4-flash-0731:deepinfra": {URL: "https://openrouter.ai/deepseek/deepseek-v4-flash-0731", Expected: 0.08, Threshold: 2.0},
			"qwen/qwen3.7-flash":                        {URL: "https://openrouter.ai/qwen/qwen3.7-flash", Expected: 0.03, Threshold: 2.0},
		},
		DataHandling: map[string]string{
			// Owner-vetted: retains prompts for caching, never trains (the
			// cache_only bucket — cheaper than zdr, same no-train guarantee).
			"Baidu": "cache_only",
		},
	}
}

func loadConfig() *config {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath)
	if err != nil {
		// legacy install: config used to live in /etc (root-owned, not
		// model-editable); prefer the new location when present.
		data, err = os.ReadFile(legacyConfigPath)
		if err != nil {
			return cfg
		}
	}
	var extra config
	if json.Unmarshal(data, &extra) == nil {
		if extra.Listen != "" {
			cfg.Listen = extra.Listen
		}
		if extra.Port != 0 {
			cfg.Port = extra.Port
		}
		if extra.Telegram != "" {
			cfg.Telegram = extra.Telegram
		}
		for k, v := range extra.Models {
			cfg.Models[k] = v
		}
		if extra.CatalogueRefreshMin > 0 {
			cfg.CatalogueRefreshMin = extra.CatalogueRefreshMin
		}
		// Per-key merge so a curated map (zdr for the incumbent hosts) never
		// clobbers defaults like Baidu's cache_only vetting.
		for k, v := range extra.DataHandling {
			cfg.DataHandling[k] = v
		}
	}
	return cfg
}

func readMinoEnv(key string) string {
	data, err := os.ReadFile(minoEnvPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			return strings.TrimPrefix(line, key+"=")
		}
	}
	return ""
}

// --- scraper ---------------------------------------------------------------

// pricingRE matches the escaped-JSON pricing objects on the OpenRouter pages:
// \"pricing\":{\"prompt\":\"...\",\"completion\":\"...\",\"input_cache_read\":\"...\",\"discount\":N
// [^{}]* between fields tolerates extra keys in the pricing object (luna-pro
// carries input_cache_write + web_search between input_cache_read and discount).
var pricingRE = regexp.MustCompile(`\\?"pricing\\?":\{[^{}]*\\?"prompt\\?":\\?"([0-9.]+)\\?"[^{}]*\\?"completion\\?":\\?"([0-9.]+)\\?"[^{}]*\\?"input_cache_read\\?":\\?"([0-9.]+)\\?"[^{}]*\\?"discount\\?":([0-9.]+)`)

var nameRE = regexp.MustCompile(`\\?"name\\?":\\?"([^\\"]{2,40})\\"`)

type providerPrice struct {
	Input    float64
	Output   float64
	Cache    float64
	Discount float64
}

var fetch = func(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parsePricing(html string) map[string]providerPrice {
	out := map[string]providerPrice{}
	for _, m := range pricingRE.FindAllStringSubmatchIndex(html, -1) {
		in, _ := strconv.ParseFloat(html[m[2]:m[3]], 64)
		outp, _ := strconv.ParseFloat(html[m[4]:m[5]], 64)
		cache, _ := strconv.ParseFloat(html[m[6]:m[7]], 64)
		disc, _ := strconv.ParseFloat(html[m[8]:m[9]], 64)
		// Walk back for the provider name.
		start := m[0] - 2500
		if start < 0 {
			start = 0
		}
		back := html[start:m[0]]
		names := nameRE.FindAllStringSubmatch(back, -1)
		provider := "?"
		if len(names) > 0 {
			provider = names[len(names)-1][1]
		}
		out[provider] = providerPrice{in * 1e6, outp * 1e6, cache * 1e6, disc}
	}
	return out
}

type priceState struct {
	LastCheck string                    `json:"last_check"`
	Prices    map[string]map[string]any `json:"prices"`
	Flags     map[string]string         `json:"flags"`
}

var state = &priceState{Prices: map[string]map[string]any{}, Flags: map[string]string{}}

func checkModels(cfg *config) (map[string]map[string]any, map[string]string) {
	prices := map[string]map[string]any{}
	flags := map[string]string{}
	for name, m := range cfg.Models {
		html, err := fetch(m.URL)
		if err != nil {
			flags[name] = "SCRAPE FAILED: " + err.Error()
			prices[name] = map[string]any{"error": err.Error()}
			continue
		}
		provs := parsePricing(html)
		if len(provs) == 0 {
			flags[name] = "no pricing parsed (page structure changed?)"
			prices[name] = map[string]any{}
			continue
		}
		best := ""
		entries := make([]catalogueEntry, 0, len(provs))
		for p, price := range provs {
			entries = append(entries, catalogueEntry{Provider: p, In: price.Input, Cache: price.Cache, Out: price.Output, DataHandling: cfg.DataHandling[p]})
		}
		ranked := rankCatalogueEntries(entries)
		if len(ranked) > 0 {
			best = ranked[0].Provider
		}
		if best == "" {
			flags[name] = "no complete pricing parsed"
			prices[name] = map[string]any{"providers": len(provs)}
			continue
		}
		b := provs[best]
		prices[name] = map[string]any{
			"best_provider": best, "best_input": b.Input,
			"best_output": b.Output, "best_cache": b.Cache,
			"pin_metric": "all",
			"discount":   b.Discount, "providers": len(provs),
		}
		limit := m.Expected * m.Threshold
		if b.Input > limit {
			flags[name] = fmt.Sprintf("PRICE SPIKE: best $%.4f/M input > expected $%.4f × %.1f (promo likely expired)", b.Input, m.Expected, m.Threshold)
		} else {
			flags[name] = "ok"
		}
	}
	state.LastCheck = time.Now().Format("2006-01-02 15:04:05")
	state.Prices, state.Flags = prices, flags
	return prices, flags
}

// --- actions ---------------------------------------------------------------

func sendTelegram(cfg *config, message string) string {
	token := readMinoEnv("TELEGRAM_BOT_TOKEN")
	chat := cfg.Telegram
	if chat == "" {
		chat = readMinoEnv("MINO_TELEGRAM_CHAT_ID")
	}
	if token == "" || chat == "" {
		return "telegram not configured"
	}
	body, _ := json.Marshal(map[string]string{"chat_id": chat, "text": message})
	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(string(body)))
	if err != nil {
		return "send failed: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "send failed: " + err.Error()
	}
	defer resp.Body.Close()
	return fmt.Sprintf("sent (%d)", resp.StatusCode)
}

func statusText(cfg *config) string {
	var b strings.Builder
	b.WriteString("last check: " + state.LastCheck)
	if state.LastCheck == "" {
		b.WriteString("never")
	}
	b.WriteString("\n")
	for name := range cfg.Models {
		p := state.Prices[name]
		f, _ := state.Flags[name]
		if p == nil || f == "" {
			b.WriteString(fmt.Sprintf("- %s: not checked\n", name))
			continue
		}
		if prov, _ := p["best_provider"].(string); prov != "" {
			in, _ := p["best_input"].(float64)
			outp, _ := p["best_output"].(float64)
			b.WriteString(fmt.Sprintf("- %s: best %s $%.4f/M in ($%.4f/M out) [%s]\n", name, prov, in, outp, f))
		} else {
			b.WriteString(fmt.Sprintf("- %s: %s\n", name, f))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// --- HTTP (extension protocol) ----------------------------------------------

func toolSchemas() []map[string]any {
	return []map[string]any{
		{"name": "cost_watch_status",
			"description": "Current best provider prices for the configured models, last check time, and promo-expiry flags.",
			"schema":      map[string]any{"type": "object", "properties": map[string]any{}}},
		{"name": "cost_watch_check",
			"description": "Scrape the OpenRouter model pages NOW, refresh prices, and return them with any flags.",
			"schema":      map[string]any{"type": "object", "properties": map[string]any{}}},
		{"name": "cost_watch_refresh",
			"description": "Fetch the OpenRouter endpoints price catalogue NOW and persist it (also runs hourly); returns the entry count and scrape time.",
			"schema":      map[string]any{"type": "object", "properties": map[string]any{}}},
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
}

// --- catalogue (CTX-020) --------------------------------------------------

// fetchCatalogue fetches the OpenRouter endpoints API for each OpenRouter-hosted
// model in providers.json and returns every hosting provider's live price plus a
// curated data-handling flag (zdr|trains|unknown). Model-agnostic: the targets
// are whatever the user configured, never a hardcoded list. Prices are the
// API's own per-token USD scaled to per-1M.
func fetchCatalogue(cfg *config) (catalogue, error) {
	cat := catalogue{ScrapedAt: time.Now().UTC().Format(time.RFC3339)}
	models, err := configuredOpenRouterModels()
	if err != nil {
		return cat, err
	}
	for _, slug := range models {
		body, err := fetch("https://openrouter.ai/api/v1/models/" + slug + "/endpoints")
		if err != nil {
			continue // one model failing must not kill the whole catalogue
		}
		var resp struct {
			Data struct {
				Endpoints []struct {
					Name    string `json:"name"`
					Pricing struct {
						Prompt         string  `json:"prompt"`
						Completion     string  `json:"completion"`
						InputCacheRead string  `json:"input_cache_read"`
						Discount       float64 `json:"discount"`
					} `json:"pricing"`
					Uptime       float64  `json:"uptime_last_30m"`
					Latency      *float64 `json:"latency_last_30m"`
					Quantization string   `json:"quantization"`
				} `json:"endpoints"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(body), &resp) != nil {
			continue
		}
		for _, ep := range resp.Data.Endpoints {
			provider := ep.Name
			if i := strings.Index(provider, " | "); i >= 0 {
				provider = provider[:i]
			}
			in, _ := strconv.ParseFloat(ep.Pricing.Prompt, 64)
			out, _ := strconv.ParseFloat(ep.Pricing.Completion, 64)
			cache, _ := strconv.ParseFloat(ep.Pricing.InputCacheRead, 64)
			flag := cfg.DataHandling[provider]
			if flag == "" {
				flag = "unknown" // curated elsewhere; never scraped (verify-then-claim)
			}
			latency := 0.0
			if ep.Latency != nil {
				latency = *ep.Latency
			}
			cat.Entries = append(cat.Entries, catalogueEntry{
				Model:        slug,
				Provider:     provider,
				In:           in * 1e6,
				Out:          out * 1e6,
				Cache:        cache * 1e6,
				Discount:     ep.Pricing.Discount,
				Uptime:       ep.Uptime,
				Latency:      latency,
				LatencyKnown: ep.Latency != nil,
				DataHandling: flag,
				Quantization: ep.Quantization,
			})
		}
	}
	return cat, nil
}

// configuredOpenRouterModels returns the model slugs from providers.json that
// are OpenRouter-hosted (contain a "/" — the openrouter slug shape). Direct-API
// models (no slash) have no endpoints listing and are skipped.
func configuredOpenRouterModels() ([]string, error) {
	data, err := os.ReadFile(providersPath)
	if err != nil {
		return nil, err
	}
	var f struct {
		Providers []struct {
			Model      string `json:"model"`
			SmallModel string `json:"small_model"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &f) != nil {
		return nil, fmt.Errorf("invalid providers.json")
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range f.Providers {
		for _, model := range []string{p.Model, p.SmallModel} {
			if model == "" || !strings.Contains(model, "/") {
				continue
			}
			slug := stripProviderPin(model)
			if !seen[slug] {
				seen[slug] = true
				out = append(out, slug)
			}
		}
	}
	return out, nil
}

// --- autonomous pinning (rel-01 reversal, issue #128 revisited) ------------
//
// The 2026-08-10 alert-only restriction shipped because a broken pager (the
// promo-expiry timer was never installed) was misread as autonomous-behavior
// failure. The pager is fixed; price churn on a promo-driven stack goes
// silent again unless the pin itself chases the price. So: every catalogue
// refresh ranks eligible hosts (curated `trains` providers are hard-excluded;
// everything else — zdr, cache_only, unknown — is eligible) and rewrites
// provider_routing order. Promos manifest as low input prices, so price
// ranking rides promos without a separate discount filter. Silent and
// idempotent: no owner paging, no write when the order is unchanged.

const maxPins = 5 // routing-list cap; the tail beyond this never gets traffic

// eligibleForPin is the hard privacy invariant (CTX-020): a curated trains
// provider never appears in routing, whatever its price.
func (cfg *config) eligibleForPin(provider string) bool {
	return cfg.DataHandling[provider] != "trains"
}

// precisionWorseThanFP8 is the known-worse-than-fp8 set (2026-08-31, issue
// #495: an fp4 endpoint outranked fp8 alternatives on price alone and was
// implicated in a live GLM decode-collapse incident). This is a floor, not a
// ladder — anything not in this set (fp8, fp16, bf16, fp32, "unknown", "")
// ranks in the same top tier, since there's no evidence those are worse.
var precisionWorseThanFP8 = map[string]bool{
	"fp4": true, "fp2": true, "fp1": true, "int4": true, "int2": true,
}

// precisionTier returns 1 for a quantization known to be worse than fp8, 0
// otherwise (including unknown/empty — never penalize what isn't proven bad).
func precisionTier(quant string) int {
	if precisionWorseThanFP8[strings.ToLower(quant)] {
		return 1
	}
	return 0
}

// pinOrder returns the routing order for one model slug: eligible providers
// rank by precision tier (fp8-or-better beats known-worse, e.g. fp4) first,
// then input price, output price, cache-read price, uptime, then latency.
// Incomplete prices trail complete ones so an omitted cache-read field cannot
// become a false winner. Input/output rank ahead of cache-read (owner
// decision, 2026-08-31: a cache-first order let Morph — 3.32s latency, 16
// tps, 91.4% uptime — outrank Novita/DeepInfra/GMICloud on a cheap cache-read
// price alone, despite losing on input/output price and every reliability
// metric; input+output now decide first, so the tied-cheapest full-price
// providers win before cache price or uptime ever get checked).
func rankCatalogueEntries(entries []catalogueEntry) []catalogueEntry {
	type scored struct {
		entry catalogueEntry
		full  bool
	}
	ranked := make([]scored, len(entries))
	for i, entry := range entries {
		ranked[i] = scored{entry: entry, full: entry.In > 0 && entry.Cache > 0 && entry.Out > 0}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if pi, pj := precisionTier(ranked[i].entry.Quantization), precisionTier(ranked[j].entry.Quantization); pi != pj {
			return pi < pj
		}
		if ranked[i].full != ranked[j].full {
			return ranked[i].full
		}
		if ranked[i].entry.In != ranked[j].entry.In {
			return ranked[i].entry.In < ranked[j].entry.In
		}
		if ranked[i].entry.Out != ranked[j].entry.Out {
			return ranked[i].entry.Out < ranked[j].entry.Out
		}
		if ranked[i].entry.Cache != ranked[j].entry.Cache {
			return ranked[i].entry.Cache < ranked[j].entry.Cache
		}
		if ranked[i].entry.Uptime != ranked[j].entry.Uptime {
			return ranked[i].entry.Uptime > ranked[j].entry.Uptime
		}
		if ranked[i].entry.LatencyKnown != ranked[j].entry.LatencyKnown {
			return ranked[i].entry.LatencyKnown
		}
		if ranked[i].entry.LatencyKnown && ranked[i].entry.Latency != ranked[j].entry.Latency {
			return ranked[i].entry.Latency < ranked[j].entry.Latency
		}
		if ranked[i].entry.In != ranked[j].entry.In {
			return ranked[i].entry.In < ranked[j].entry.In
		}
		if ranked[i].entry.Out != ranked[j].entry.Out {
			return ranked[i].entry.Out < ranked[j].entry.Out
		}
		return ranked[i].entry.Cache < ranked[j].entry.Cache
	})
	out := make([]catalogueEntry, len(ranked))
	for i, entry := range ranked {
		out[i] = entry.entry
	}
	return out
}

// pinOrder returns the routing order for one model slug, capped at maxPins.
func pinOrder(cfg *config, entries []catalogueEntry) []string {
	var eligible []catalogueEntry
	for _, e := range entries {
		if cfg.eligibleForPin(e.Provider) {
			eligible = append(eligible, e)
		}
	}
	eligible = rankCatalogueEntries(eligible)
	if len(eligible) > maxPins {
		eligible = eligible[:maxPins]
	}
	out := make([]string, len(eligible))
	for i, e := range eligible {
		out[i] = e.Provider
	}
	return out
}

// stripProviderPin removes the OpenRouter ":provider" suffix so the routing
// list is the single source of truth — a stale suffix would override the
// list and pin traffic to a host pinning no longer wants (the dead-:pin
// class, mino #159).
func stripProviderPin(model string) string {
	lastSlash := strings.LastIndex(model, "/")
	lastColon := strings.LastIndex(model, ":")
	if lastSlash != -1 && lastColon > lastSlash {
		return model[:lastColon]
	}
	return model
}

// applyPins rewrites routing order for provider entries that already carry a
// pin (provider_routing / small_provider_routing or a :suffix) — unpinned
// fallback entries stay untouched. Idempotent: changed=false when the order
// is already what pinning wants. Backs up before writing (the swap-era
// insurance, providers.json.bak-pin).
func applyPins(cfg *config, cat catalogue) (changed bool, summary string, err error) {
	data, err := os.ReadFile(providersPath)
	if err != nil {
		return false, "", err
	}
	var file map[string]any
	if err := json.Unmarshal(data, &file); err != nil {
		return false, "", fmt.Errorf("invalid providers.json: %w", err)
	}
	list, _ := file["providers"].([]any)
	if len(list) == 0 {
		return false, "", fmt.Errorf("providers.json has no providers")
	}
	bySlug := map[string][]catalogueEntry{}
	for _, e := range cat.Entries {
		bySlug[e.Model] = append(bySlug[e.Model], e)
	}
	var touched []string
	for i, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		before, _ := json.Marshal(entry)
		if m, _ := entry["model"].(string); m != "" {
			slug := stripProviderPin(m)
			if _, pinned := entry["provider_routing"]; pinned || slug != m {
				if order := pinOrder(cfg, bySlug[slug]); len(order) > 0 {
					entry["model"] = slug
					entry["provider_routing"] = order
				}
			}
		}
		if sm, _ := entry["small_model"].(string); sm != "" {
			slug := stripProviderPin(sm)
			if _, pinned := entry["small_provider_routing"]; pinned || slug != sm {
				if order := pinOrder(cfg, bySlug[slug]); len(order) > 0 {
					entry["small_model"] = slug
					entry["small_provider_routing"] = order
				}
			}
		}
		after, _ := json.Marshal(entry)
		if string(before) != string(after) {
			changed = true
			name, _ := entry["name"].(string)
			touched = append(touched, name)
			list[i] = entry
		}
	}
	if !changed {
		return false, "pins unchanged", nil
	}
	file["providers"] = list
	out, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(providersPath+".bak-pin", data, 0644); err != nil {
		return false, "", fmt.Errorf("pin backup failed: %w", err)
	}
	tmp := providersPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return false, "", err
	}
	if err := os.Rename(tmp, providersPath); err != nil {
		return false, "", err
	}
	return true, "pinned " + strings.Join(touched, ", "), nil
}

// sendHUP signals the running mino to hot-reload providers.json. Var for
// tests. The write already happened; a failed signal only delays the pin to
// the next restart (deploy does one anyway).
var sendHUP = func() error {
	if runtime.GOOS == "darwin" {
		return exec.Command("pkill", "-HUP", "-x", "mino").Run()
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("automatic provider reload is unavailable on Windows; restart mino manually")
	}
	return exec.Command("systemctl", "kill", "-s", "HUP", "mino").Run()
}

// refreshAndPin is the hourly loop body and the cost_watch_refresh tool:
// fetch fresh prices, persist the catalogue, chase the best-ranked eligible pin,
// and hot-reload mino when the order changed.
func refreshAndPin(cfg *config) string {
	cat, err := fetchCatalogue(cfg)
	if err != nil {
		return "catalogue fetch failed: " + err.Error()
	}
	if err := saveCatalogue(cat); err != nil {
		return "catalogue save failed: " + err.Error()
	}
	changed, summary, err := applyPins(cfg, cat)
	if err != nil {
		return "pin failed: " + err.Error()
	}
	if !changed {
		return summary
	}
	if err := sendHUP(); err != nil {
		return summary + "; mino reload failed (applies at next restart): " + err.Error()
	}
	return summary + "; mino reloaded"
}

// saveCatalogue writes the catalogue atomically to cost-catalogue.json.
func saveCatalogue(cat catalogue) error {
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	tmp := cataloguePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, cataloguePath)
}

func main() {
	cfg := loadConfig()
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		_, flags := checkModels(cfg)
		var problems []string
		for n, f := range flags {
			if f != "ok" {
				problems = append(problems, n+": "+f)
			}
		}
		if len(problems) > 0 {
			msg := "⚠️ mino cost-watch\n" + strings.Join(problems, "\n")
			fmt.Println(sendTelegram(cfg, msg))
		} else {
			fmt.Println("all prices ok")
		}
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, toolSchemas()) })
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		_, flags := checkModels(cfg)
		var problems []string
		for n, f := range flags {
			if f != "ok" {
				problems = append(problems, n+": "+f)
			}
		}
		msg := "all prices ok"
		if len(problems) > 0 {
			msg = strings.Join(problems, "; ")
		}
		writeJSON(w, 200, map[string]any{"alert": len(problems) > 0, "message": msg})
	})
	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"error": "bad request: " + err.Error()})
			return
		}
		result, err := executeTool(cfg, req.Tool, req.Args)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"result": result})
	})
	addr := fmt.Sprintf("%s:%d", cfg.Listen, cfg.Port)
	// CTX-020: catalogue refresh loop — hot-reloads the config each cycle so
	// Mino's edits to cost-watch's settings apply without a restart, then
	// persists the fresh price catalogue for the harness's system_check.
	go func() {
		for {
			fmt.Println("refresh:", refreshAndPin(loadConfig()))
			min := loadConfig().CatalogueRefreshMin
			if min <= 0 {
				min = 60
			}
			time.Sleep(time.Duration(min) * time.Minute)
		}
	}()
	fmt.Println("cost-watch listening on", addr)
	http.ListenAndServe(addr, mux)
}

// executeTool dispatches one extension-tool call. Model-changing tools do not
// exist here by policy (REL-01, issue #128): cost-watch pages, it never
// swaps.
func executeTool(cfg *config, tool string, args map[string]any) (string, error) {
	switch tool {
	case "cost_watch_status":
		return statusText(cfg), nil
	case "cost_watch_check":
		_, flags := checkModels(cfg)
		return statusText(cfg) + "\n" + fmt.Sprintf("%v", flags), nil
	case "cost_watch_refresh":
		// CTX-020: on-demand catalogue refresh (also runs periodically);
		// now also chases the best-ranked eligible pin.
		return refreshAndPin(cfg), nil
	default:
		return "", fmt.Errorf("unknown tool %s", tool)
	}
}
