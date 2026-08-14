// mino cost-watch — the price guardian.
//
// Scrapes OpenRouter model pages for per-provider pricing, exposes the mino
// extension protocol (DECISIONS.md §8), and runs an hourly autonomous check
// that alerts on Telegram when a promotional price expires. Alert-only by
// policy (REL-01, issue #128): it pages the owner — it never changes the
// brain. Model changes are human decisions.
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
	"regexp"
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
	configPath        = "/home/mino/.mino/cost-watch.json"
	legacyConfigPath  = "/etc/mino-cost-watch.json"
	providersPath     = "/home/mino/.mino/providers.json"
	runLocksDir       = "/home/mino/.mino/run-locks"
	cataloguePath     = "/home/mino/.mino/cost-catalogue.json"
)

type modelConfig struct {
	URL       string  `json:"url"`
	Expected  float64 `json:"expected"`
	Threshold float64 `json:"threshold"`
}

type config struct {
	Listen               string                 `json:"listen"`
	Port                 int                    `json:"port"`
	Models               map[string]modelConfig `json:"models"`
	Telegram             string                 `json:"telegram_chat_id"`
	CatalogueRefreshMin  int                    `json:"catalogue_refresh_minutes"` // 0 = default 60
	DataHandling         map[string]string      `json:"data_handling"`            // provider -> zdr|trains|unknown
}

// catalogueEntry is one provider's price for a model (CTX-020). In/Out are
// USD per 1M tokens; DataHandling is curated (zdr|trains|unknown), never scraped.
type catalogueEntry struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	In           float64 `json:"in"`
	Out          float64 `json:"out"`
	DataHandling string  `json:"data_handling"`
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
			"deepseek/deepseek-v4-flash-0731:deepinfra": {"https://openrouter.ai/deepseek/deepseek-v4-flash-0731", 0.08, 2.0},
			"qwen/qwen3.7-flash":                        {"https://openrouter.ai/qwen/qwen3.7-flash", 0.03, 2.0},
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
		if len(extra.DataHandling) > 0 {
			cfg.DataHandling = extra.DataHandling
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
		for p := range provs {
			if best == "" || provs[p].Input < provs[best].Input {
				best = p
			}
		}
		b := provs[best]
		prices[name] = map[string]any{
			"best_provider": best, "best_input": b.Input,
			"best_output": b.Output, "best_cache": b.Cache,
			"discount": b.Discount, "providers": len(provs),
		}
		limit := m.Expected * m.Threshold
		if b.Input > limit {
			flags[name] = fmt.Sprintf("PRICE SPIKE: best $%.4f/M > expected $%.4f × %.1f (promo likely expired)", b.Input, m.Expected, m.Threshold)
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
						Prompt     string `json:"prompt"`
						Completion string `json:"completion"`
					} `json:"pricing"`
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
			flag := cfg.DataHandling[provider]
			if flag == "" {
				flag = "unknown" // curated elsewhere; never scraped (verify-then-claim)
			}
			cat.Entries = append(cat.Entries, catalogueEntry{
				Model:        slug,
				Provider:     provider,
				In:           in * 1e6,
				Out:          out * 1e6,
				DataHandling: flag,
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
			Model string `json:"model"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &f) != nil {
		return nil, fmt.Errorf("invalid providers.json")
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range f.Providers {
		if p.Model != "" && strings.Contains(p.Model, "/") && !seen[p.Model] {
			seen[p.Model] = true
			out = append(out, p.Model)
		}
	}
	return out, nil
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
			fresh := loadConfig()
			if cat, err := fetchCatalogue(fresh); err == nil {
				if err := saveCatalogue(cat); err != nil {
					fmt.Println("catalogue save failed:", err)
				}
			}
			min := fresh.CatalogueRefreshMin
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
		// CTX-020: on-demand catalogue refresh (also runs periodically).
		cat, err := fetchCatalogue(cfg)
		if err != nil {
			return "", err
		}
		if err := saveCatalogue(cat); err != nil {
			return "", err
		}
		return fmt.Sprintf("catalogue refreshed: %d entries at %s", len(cat.Entries), cat.ScrapedAt), nil
	default:
		return "", fmt.Errorf("unknown tool %s", tool)
	}
}
