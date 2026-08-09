#!/usr/bin/env python3
"""Unit tests for mino cost-watch (no network)."""
import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(__file__))
import cost_watch as cw

# Fixture: escaped-JSON pricing like the live OpenRouter pages.
FIXTURE = r'''
<script>window.__DATA__ = "{\"name\":\"StreamLake\",\"pricing\":{\"prompt\":\"0.000000098\",\"completion\":\"0.000000308\",\"input_cache_read\":\"0.0000000182\",\"discount\":0.93,\"display_pricing\":[{\"kind\":\"token\"}]},\"name\":\"Baidu\",\"pricing\":{\"prompt\":\"0.000000112\",\"completion\":\"0.000000352\",\"input_cache_read\":\"0.0000000208\",\"discount\":0.92},\"name\":\"Z.ai\",\"pricing\":{\"prompt\":\"0.00000068\",\"completion\":\"0.00000213\",\"input_cache_read\":\"0.000000126\",\"discount\":0.0}}"</script>'''

FIXTURE_LUNA = r'''
<script>window.__DATA__ = "{\"name\":\"OpenAI\",\"pricing\":{\"prompt\":\"0.0000001\",\"completion\":\"0.0000006\",\"input_cache_read\":\"0.00000001\",\"discount\":0.5},\"name\":\"Azure\",\"pricing\":{\"prompt\":\"0.0000011\",\"completion\":\"0.0000066\",\"input_cache_read\":\"0.00000011\",\"discount\":0.0}}"</script>'''


def approx(a, b, tol=0.0001):
    return all(abs(x - y) <= tol for x, y in zip(a, b))


def test_parse_glm_pricing():
    provs = cw.parse_pricing(FIXTURE)
    assert approx(provs["StreamLake"], (0.098, 0.308, 0.0182, 0.93)), provs
    assert approx(provs["Baidu"], (0.112, 0.352, 0.0208, 0.92)), provs
    assert abs(provs["Z.ai"][0] - 0.68) < 0.0001, provs
    print("ok: glm pricing parsed")


def test_parse_luna_pricing():
    provs = cw.parse_pricing(FIXTURE_LUNA)
    assert approx(provs["OpenAI"], (0.10, 0.60, 0.01, 0.5)), provs
    assert abs(provs["Azure"][0] - 1.10) < 0.0001, provs
    print("ok: luna pricing parsed")


def test_best_provider():
    provs = cw.parse_pricing(FIXTURE)
    best = min(provs.items(), key=lambda kv: kv[1][0])
    assert best[0] == "StreamLake" and best[1][0] == 0.098, best
    print("ok: best provider = StreamLake $0.098")


def test_flag_on_price_spike(monkeypatch=None):
    cfg = json.loads(json.dumps(cw.DEFAULT_CONFIG))
    cfg["models"]["glm-5.2"]["expected"] = 0.098
    cfg["models"]["glm-5.2"]["threshold"] = 2.0
    orig_fetch = cw.fetch
    cw.fetch = lambda url: FIXTURE if "glm" in url else FIXTURE_LUNA
    try:
        prices, flags = cw.check_models(cfg)
        assert flags["glm-5.2"] == "ok", flags
        # Simulate the promo expiring: BOTH discounted hosts drop to the
        # full $0.68 (otherwise the next-cheapest host becomes the best).
        cw.fetch = lambda url: (FIXTURE
                                .replace("0.000000098", "0.00000068")
                                .replace("0.000000112", "0.00000068")
                                .replace('"discount":0.93', '"discount":0.0')
                                .replace('"discount":0.92', '"discount":0.0')
                                if "glm" in url else FIXTURE_LUNA)
        prices2, flags2 = cw.check_models(cfg)
        assert "PRICE SPIKE" in flags2["glm-5.2"], flags2
        assert "ok" == flags2["luna-pro"], flags2
    finally:
        cw.fetch = orig_fetch
    print("ok: promo expiry detected, luna unaffected")


def test_swap_writes_providers_and_backs_up(tmp=None):
    cfg = json.loads(json.dumps(cw.DEFAULT_CONFIG))
    d = tempfile.mkdtemp()
    prov_path = os.path.join(d, "providers.json")
    with open(prov_path, "w") as f:
        json.dump({"providers": [{"name": "old", "priority": 1}]}, f)
    orig_path = cw.PROVIDERS_JSON
    orig_locks = cw.RUN_LOCKS
    cw.PROVIDERS_JSON = prov_path
    cw.RUN_LOCKS = os.path.join(d, "run-locks")  # nonexistent = no locks
    try:
        result = cw.swap_model(cfg, "luna-pro")
        assert "swapped to luna-pro" in result, result
        assert os.path.exists(prov_path + ".bak-cost-watch")
        with open(prov_path) as f:
            data = json.load(f)
        assert data["providers"][0]["model"] == "openai/gpt-5.6-luna-pro", data
    finally:
        cw.PROVIDERS_JSON = orig_path
        cw.RUN_LOCKS = orig_locks
    print("ok: swap writes providers.json + backup")


if __name__ == "__main__":
    test_parse_glm_pricing()
    test_parse_luna_pricing()
    test_best_provider()
    test_flag_on_price_spike()
    test_swap_writes_providers_and_backs_up()
    print("ALL TESTS PASSED")
