# Provider maturity

Clawmeter uses a binary provider maturity model:

- **ordinary** means the integration has sufficient current evidence for normal
  product claims.
- **experimental** means the live-user confidence is below 90%, the producer
  contract is undocumented or unstable, or known semantic defects remain.

Current classifications are explicit metadata, not an inference from whether
an audit covered a provider:

| Providers | Maturity |
|---|---|
| Claude, Codex (`openai`), Gemini, Grok (`xai`) | ordinary |
| Kimi, Kimi K2, Copilot, OpenRouter, JetBrains, Synthetic, z.ai | experimental |

The experimental group reflects the current provider audit's documented
contract or semantic risks. The ordinary group retains this project's existing
live-validation evidence; the ten-provider cross-review did not assess those
four providers. Maturity describes confidence in the integration, not whether
credentials were found or whether polling is enabled. The `providers`
inventory remains the place to see setup and polling state; quota rows and the
tray intentionally do not carry maturity labels.

Promotion to ordinary requires a deliberate metadata change backed by focused
tests and evidence. Current audit evidence is in the local handoff at
`/home/tnunamak/.tmp/clawmeter-provider-audit/06-cross-review.md`; CodexBar is
useful executable prior art, not provider authority. No live entitled-account
validation was available for this classification.

The stable learn-more destination emitted in structured metadata is this
document: `docs/provider-maturity.md`.
