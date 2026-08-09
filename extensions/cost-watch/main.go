// mino cost-watch — the price guardian.
//
// Scrapes OpenRouter model pages for per-provider pricing, exposes the mino
// extension protocol (DECISIONS.md §8), and runs an hourly autonomous check
// that alerts on Telegram when a promotional price expires.
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
	"strconv"
	"strings"
	"time"
)

const (
	configPath  = "/etc/mino-cost-watch.json"
	minoEnvPath = "/home/mino/.mino/mino.env"
	ua          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"
)

// vars so tests can point them at temp dirs
var (
	providersPath = "/home/mino/.mino/providers.json"
	runLocksDir   = "/home/mino/.mino/run-locks"
)

type modelConfig struct {
	URL       string  `json:"url"`
	Expected  float64 `json:"expected"`
	Threshold float64 `json:"threshold"`
}

type config struct {
	Listen    string                   `json:"listen"`
	Port      int                      `json:"port"`
	Models    map[string]modelConfig   `json:"models"`
	Chain     []string                 `json:"chain"`
	Templates map[string][]interface{} `json:"provider_templates"`
	Telegram  string                   `json:"telegram_chat_id"`
}

func defaultConfig() *config {
	return &config{
		Listen: "127.0.0.1",
		Port:   9300,
		Models: map[string]modelConfig{
			"glm-5.2":  {"https://openrouter.ai/z-ai/glm-5.2", 0.098, 2.0},
			"luna-pro": {"https://openrouter.ai/openai/gpt-5.6-luna-pro", 0.10, 2.0},
		},
		Chain: []string{"glm-5.2", "luna-pro", "qwen"},
	}
}

func loadConfig() *config {
	cfg := defaultConfig()
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
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
		if len(extra.Chain) > 0 {
			cfg.Chain = extra.Chain
		}
		if len(extra.Templates) > 0 {
			cfg.Templates = extra.Templates
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
	LastCheck string                     `json:"last_check"`
	Prices    map[string]map[string]any  `json:"prices"`
	Flags     map[string]string          `json:"flags"`
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

func swapModel(cfg *config, model string) string {
	tmpl, ok := cfg.Templates[model]
	if !ok {
		return fmt.Sprintf("Error: no provider template for %q (chain: %s)", model, strings.Join(cfg.Chain, ", "))
	}
	orig, err := os.ReadFile(providersPath)
	if err != nil {
		return "Error: providers.json not found: " + err.Error()
	}
	backup := providersPath + ".bak-cost-watch"
	if err := os.WriteFile(backup, orig, 0o600); err != nil {
		return "Error: backup failed: " + err.Error()
	}
	out, _ := json.MarshalIndent(map[string]any{"providers": tmpl}, "", "  ")
	if err := os.WriteFile(providersPath, out, 0o644); err != nil {
		return "Error: write failed: " + err.Error()
	}
	// In-flight playbook guard: defer the restart (self-updater rule).
	if entries, err := os.ReadDir(runLocksDir); err == nil && len(entries) > 0 {
		return fmt.Sprintf("providers.json swapped to %s (backup at %s); restart deferred — playbook run in flight", model, backup)
	}
	if err := exec.Command("systemctl", "restart", "mino").Run(); err != nil {
		return fmt.Sprintf("providers.json swapped to %s (backup at %s); restart failed: %v", model, backup, err)
	}
	return fmt.Sprintf("providers.json swapped to %s (backup at %s); mino restarted", model, backup)
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
		{"name": "cost_watch_swap",
			"description": "Swap providers.json to a chain model and restart mino. Chain: glm-5.2, luna-pro, qwen.",
			"schema": map[string]any{"type": "object", "properties": map[string]any{
				"model": map[string]any{"type": "string", "description": "Target model: glm-5.2, luna-pro, qwen"},
			}, "required": []string{"model"}}},
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
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
			msg := "⚠️ mino cost-watch\n" + strings.Join(problems, "\n") +
				fmt.Sprintf("\nSwap: cost_watch_swap (chain: %s)", strings.Join(cfg.Chain, ", "))
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
		var result string
		switch req.Tool {
		case "cost_watch_status":
			result = statusText(cfg)
		case "cost_watch_check":
			_, flags := checkModels(cfg)
			result = statusText(cfg) + "\n" + fmt.Sprintf("%v", flags)
		case "cost_watch_swap":
			model, _ := req.Args["model"].(string)
			result = swapModel(cfg, model)
		default:
			writeJSON(w, 400, map[string]any{"error": "unknown tool " + req.Tool})
			return
		}
		writeJSON(w, 200, map[string]any{"result": result})
	})
	addr := fmt.Sprintf("%s:%d", cfg.Listen, cfg.Port)
	fmt.Println("cost-watch listening on", addr)
	http.ListenAndServe(addr, mux)
}
