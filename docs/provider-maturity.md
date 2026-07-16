# Provider maturity

Clawmeter uses a binary provider maturity model. An integration is either
**experimental** or it is not. Experimental means the live-user confidence is
below 90%, the producer
  contract is undocumented or unstable, or known semantic defects remain.

Current classifications are explicit metadata, not an inference from whether
an audit covered a provider:

| Providers | Maturity |
|---|---|
| Claude, Codex (`openai`), Gemini, Grok (`xai`) | not experimental |
| Kimi, Kimi K2, Copilot, OpenRouter, JetBrains, Synthetic, z.ai | experimental |

The experimental group reflects the current provider audit's documented
contract or semantic risks. The other group retains this project's existing
live-validation evidence; the ten-provider cross-review did not assess those
four providers. Maturity describes confidence in the integration, not whether
credentials were found or whether polling is enabled. The `providers`
inventory remains the place to see setup and polling state; quota rows and the
tray intentionally do not carry maturity labels.

Removing the experimental flag requires a deliberate metadata change backed by
focused tests and current evidence. CodexBar is useful executable prior art,
not provider authority. No live entitled-account validation was available for
the current experimental classifications.
