# AI News Daily

Purpose: Every day, identify the top three trending AI stories involving OpenAI, Google, Anthropic, Meta, xAI, or Microsoft; verify them deterministically; compose one combined Threads post and a complete Telegram report.

Routing: Three-stage pipeline. 01-judgment (LLM): pick and verify the top-3 topics, write topics.md. 02-fetch (script): fetch the chosen URLs and extract facts — zero inference, no notifications. 03-synthesize (LLM, final): compose the Threads post + Telegram report; this is the only stage that notifies (send_message). Persona: trend-researcher — see persona.md.
