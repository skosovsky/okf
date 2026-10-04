# Actual Codex CLI integration

Four isolated runs passed with codex-cli 0.160.0: EN/RU explicit skill discovery/use and EN/RU MCP connection/use. No global client configuration changed.

Skill runs read installed `.agents/skills/open-knowledge-format/SKILL.md`, read both teaching documents and executed strict validation successfully. MCP runs called `search_sections`, `read_concept(retry-policy)` and `read_concept(source-material)`. Each final answer states 24 hours and cites both documents.

Official setup reference: [Build skills](https://learn.chatgpt.com/docs/build-skills), checked 2026-10-04; repository discovery `.agents/skills`, `/skills` and explicit `$` invocation. Raw event streams and per-run exit records accompany client-validation.json. Initial sandbox attempts failed before client initialization because its existing local SQLite database was read-only; approved isolated client runs then succeeded. This is integration evidence, not a claim that all agent outputs are correct.
