---
title: "Claude model names are provider-reported while Codex quota data remains model-agnostic"
date: 2026-09-05
topic: provider-usage
tags: [claude, codex, models, quotas, compatibility]
status: settled
sources: [claude-code-model-config, claude-models-overview, codex-models, codex-config-reference]
source_session: unknown
---

## CLAIMS

- Claude Code documents the aliases `fable`, `opus`, and `sonnet`, and says aliases resolve to provider-appropriate current model versions. [claude-code-model-config]
- The current Claude model overview lists Claude Fable 5.1, Claude Opus 5, Claude Sonnet 5, and Claude Haiku 4.5. [claude-models-overview]
- The installed Claude Code binary on this host is version 2.1.261 and its help accepts the `fable` alias and the full `claude-fable-5` model name. [installed-claude-code-2026-09-05]
- The current Codex model documentation lists GPT-6 Astra and GPT-5.6 Sol, Terra, and Luna, and documents GPT-5.4 and GPT-5.4-mini as retired for ChatGPT-authenticated Codex after 2026-08-31. [codex-models]
- The Codex configuration reference documents `model_reasoning_effort` separately from the model name. [codex-config-reference]
- Clawmeter's Codex quota surfaces provide time-window fields such as utilization, duration, and reset time, but do not provide the active model. [clawmeter-codex-rate-limit-adapter]

## SOURCES

**claude-code-model-config**
URL: https://code.claude.com/docs/en/model-config
Accessed: 2026-09-05
Quote: "Use a model alias to select model settings without remembering exact version numbers."

**claude-models-overview**
URL: https://platform.claude.com/docs/en/models/overview
Accessed: 2026-09-05

**codex-models**
URL: https://developers.openai.com/codex/models
Accessed: 2026-09-05

**codex-config-reference**
URL: https://developers.openai.com/codex/config-reference
Accessed: 2026-09-05

**installed-claude-code-2026-09-05**
URL: local command: `claude --version` and `claude --help`
Accessed: 2026-09-05

**clawmeter-codex-rate-limit-adapter**
URL: local source: `internal/provider/openai/openai.go`
Accessed: 2026-09-05

## SYNTHESIS

Clawmeter should not maintain a release-coupled list of Claude model names. The
Anthropic adapter already receives scoped names from the provider's normalized
usage limits, so display and diagnostic safety should accept a bounded model-label
shape. Codex should continue to display the provider name and quota window only;
adding Astra, Sol, Terra, or Luna to a quota row would claim information the quota
response does not contain. Documentation may mention current model availability,
but live quota output must remain source-faithful.
