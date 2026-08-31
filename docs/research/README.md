# docs/research — reference material fetched from the web

Source of truth for grounding agent instructions in real Redis semantics and
prompt-engineering practice. All files fetched via `curl` (the DeepSeek
web-search tool is unavailable — the harness `web-search-deepseek` provider
has no valid `DEEPSEEK_API_KEY`; see `guidelines.md` §8 for the workaround).

- `resp-protocol-spec.md` — official Redis serialization protocol spec
  (redis.io/docs/latest/reference/protocol-spec/).
- `redis-<cmd>.md` — official command docs from the `redis/redis-doc` GitHub
  repo (raw.githubusercontent.com/redis/redis-doc/master/commands/<cmd>.md).
- `prompt-engineering-guide.txt` — LLM Agents chapter, Prompt Engineering
  Guide (promptingguide.ai/research/llm-agents), HTML→text extraction.

Fetched: 2026-08-31. Treat as reference, not law: mem-x aims for Redis
*compatibility on the wire*, not a byte-for-byte clone of Redis internals.
