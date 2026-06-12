# Clawmeter Tray Icon Design Variations

This is a design handoff for a second-pass visual review. It focuses on the tray icon / quota visualization, not the full CLI or installer UX.

Preview images are in [docs/design-variations/](/home/tnunamak/code/clawmeter/docs/design-variations). The enlarged review sheet is [contact-sheet.png](/home/tnunamak/code/clawmeter/docs/design-variations/contact-sheet.png). Real-size sheets are [contact-sheet-real-22px.png](/home/tnunamak/code/clawmeter/docs/design-variations/contact-sheet-real-22px.png) and [contact-sheet-real-32px.png](/home/tnunamak/code/clawmeter/docs/design-variations/contact-sheet-real-32px.png). Historical static PNGs were extracted from git; the `current-*` images were generated from the current working tree and are representative of the latest procedural renderer.

The enlarged sheet is for concept comparison only. It should not be used to judge tray legibility. The 22px and 32px sheets are closer to what a real user sees in a system tray, depending on platform, panel scale, and tray implementation.

## Ordered Variations

1. **AI-generated standalone claw meter**

   Concept: a standalone crab/crawfish claw wrapped around a small gauge. The icon only communicated usage state via whole-icon color: green, yellow, red.

   Commits: `9534876` (`Replace placeholder icons with AI-generated claw-meter design`), later adjusted by `b9f557c` for transparency and a gray expired-token state.

   Images: `01-ai-generated-green.png`, `01-ai-generated-yellow.png`, `01-ai-generated-red.png`, `02-transparent-gray.png`.

   Feedback / issue: visually claw-ish and branded, but not enough information density. It only conveyed coarse status, not provider identity, quota window, expected pace, or reset timing. It was also vulnerable to losing detail at tray size.

   Research documented: commit message says the assets were generated via FLUX2 Klein on `llm.vivid.fish`; no external prior-art notes found in the repo for this step.

2. **Projection-based static colors**

   Concept: keep the claw-meter shape, but drive green/yellow/red from projected usage at reset rather than raw current usage. This moved the semantic question from “how much have I used?” to “am I on pace to run out?”

   Commits: `b0ee052` (`feat: projection-based colors, auto-launch tray on install`), `1e420e6` (`fix: update README for projection-based colors...`).

   Images: `03-projection-green.png`, `03-projection-red.png`.

   Feedback / issue: this was directionally better because projection accounts for time remaining, but red/yellow/green was too coarse. You said you kept clicking because the icon did not answer enough at a glance.

   Research documented: no separate research doc found. The implementation rationale is in commit messages: projection at reset is more actionable than raw percentage.

3. **Bolder filled static icons**

   Concept: simplify and embolden the static claw icon for KDE/Plasma and 22px tray rendering. Copy changed from vague status words to projection language like `on pace`, `may hit limit`, and `on pace to exceed`.

   Commits: `81eb5ac` (`fix: clearer projection language, bolder tray icons`), `343384d` (`feat: proper KDE icon sizing, show projected percentage`).

   Images: not separately generated here; the closest visual reference is the static projection set.

   Feedback / issue: the code history itself says the previous detailed claw design lost visual weight at tray size. This fixed weight, but it further reduced the sense of a distinctive claw-meter visual.

   Research documented: commit message documents the platform constraint: KDE downscaling and tray-size rendering were the reason for bolder shapes.

4. **Restore original detailed claw icons**

   Concept: revert from the bolder filled icons back to the earlier more detailed claw-meter art.

   Commits: `1347b33` (`fix: restore original claw icons`).

   Images: `04-restored-original-green.png`, `04-restored-original-red.png`.

   Feedback / issue: this preserved the claw identity better than the bolder icon, but the same fundamental limitation remained: one color could not encode provider, quota, current usage, expected pace, and reset risk.

   Research documented: no external research notes found.

5. **Provider logo composited inside the crawfish**

   Concept: runtime-generated composite icon. The provider logo was placed inside the clawmeter crawfish, and the crawfish filled bottom-to-top with gray/green/yellow/red based on utilization. Tooltip/menu items sorted worst-first.

   Commits: `e28389f` (`feat: dynamic tray icons showing provider identity and usage level`).

   Images: not regenerated exactly in this handoff; this was a code-era variant rather than a static blob.

   Feedback / issue: this later became a hard constraint violation. You repeatedly clarified that provider logos must render as themselves and the clawmeter must be overlaid separately. You specifically rejected rasterizing ChatGPT/OpenAI into the same image as the claw meter.

   Research documented: no external research notes found. Commit message documents the runtime-compositing concept and worst-first ordering rationale.

6. **Provider logo as the base, claw overlay in a corner**

   Concept: separate provider identity from clawmeter status. The provider logo became the base layer, with a claw/crawfish overlay in the top-left. The logo was enlarged and official brand assets replaced monochrome logos.

   Commits: `7233259` (`fix: larger crawfish overlay in top-left, bigger provider logo`), `a7bb4b6` (`fix: use real brand-colored provider logos, crawfish top-left and larger`).

   Images: not regenerated exactly here.

   Feedback / issue: closer to the desired separation of concerns, but OpenAI was hard to see on a dark tray because the black mark had no light background. You also said the claw used to be fine and did not want it changed or removed while fixing OpenAI contrast.

   Research documented: brand-asset rationale is in `a7bb4b6`; no external visual-prior-art note found.

7. **Provider-focused radial pacing gauge**

   Concept: keep the provider logo as the primary icon and overlay a radial gauge around it. The radial language tried to encode two dimensions: actual usage and expected pace for the current point in the reset window. This was the first major move away from whole-icon color.

   Commits: `7c9e4d9` (`feat(tray): improve provider icon focus and pacing gauge`).

   Images: current equivalents are `current-openai-5h-under.png`, `current-openai-7d-over.png`, `current-claude-7s-under.png`, and `current-claude-7a-over.png`, but note that these include later marker changes.

   Feedback / issue: you were aligned with the two-dimensional premise, but the visual grammar was still unclear. You asked whether it should behave like a clock/radial bar, whether red/green was ideal, and what exactly the user wants to know at a glance. You also rejected overly coarse red/yellow/green because it forced clicking.

   Research documented: no durable repo document found. In-session rationale referenced video-game HUD patterns: compact meters often combine a fill, a threshold/target marker, and urgent color only where attention is needed. This was discussed, not committed as research.

8. **Quota-aware click cycling and icon labels**

   Concept: left-click cycles visible provider/quota windows, ordered most-at-risk to least-at-risk; double-click returns to Auto; Auto is not included in the toggle chain. The icon gained short quota labels like `5H`, `7D`, `7S`, `7A` so the user can tell which quota window is currently displayed.

   Commits: `7a1093e` (`feat(tray): add quota-aware icon cycling`), `0f6a9bd` (`fix(tray): keep auto out of icon cycle`), `9a2abf7` (`fix(ux): label tray hover quota`), `e845a69` (`fix(ux): improve tray cycling and hover copy`).

   Images: `08-usage-head-claude-5h.png`, `08-usage-head-openai-5h.png`, `08-usage-head-openai-7d.png`, plus current variants.

   Feedback / issue: the label direction was accepted after several size/weight tweaks: centered, bigger, blockier, black text, light halo, no text background. Auto inside the toggle chain was confusing and was removed. Toasts were considered annoying, so the icon needed to carry quota identity itself.

   Research documented: no external research notes found. Design rationale came from your stated glance question: “which provider/quota am I looking at?”

9. **White usage head / expected pace marker**

   Concept: a white marker indicates expected pace or the current/terminal point, with red/green showing the gap between actual usage and target. This attempted to make the radial gauge more precise than a single color band.

   Commits: `60af5f7` (`fix(tray): mark usage head on icon`), then several uncommitted iterations in the current working tree.

   Images: `08-usage-head-openai-7d.png`, `current-openai-7d-over.png`, `current-openai-5h-under.png`.

   Feedback / issue: this is where the most recent churn happened. You asked for the white marker to become a bar rather than a dot, then for a colored head marker, then for less color overuse. You later observed that the minute details felt messy and that the white bar overlapped the red/green radial bar underneath it.

   Research documented: no external research notes found. The working design principle was “white/neutral shows allowance or target; red/green shows deviation.”

10. **Current split-bookend marker attempt**

   Concept: make the marker bookend the colored radial segment instead of covering it, so the full red/green length remains visible. The current code attempts this by drawing two separated white ticks outside and inside the annular band rather than one continuous radial stroke through it.

   Commits: uncommitted working tree changes in `internal/tray/icons/gen.go` and `internal/tray/icons/gen_test.go`.

   Images: `current-openai-7d-over.png`, `current-openai-5h-under.png`, `current-claude-7s-under.png`, `current-claude-7a-over.png`.

   Feedback / issue: you said the latest version has gotten worse and that the marker still reads as one continuous piece overlaying the red bar. The likely design problem is that even if the code avoids painting the band center, the tray-size result still reads as a single interrupting white shape, especially after downscaling and against the high-contrast logo/text. The real-size 22px sheet makes the fine marker details look noisy and under-resolved.

   Research documented: no external research notes found. This was a direct implementation response to your “bookend, not overlay” critique.

## Open Design Questions For Review

1. What is the best single-glance visual hierarchy: provider identity first, quota-window label second, pacing status third, or a different order?
2. Should the red/green deviation be a full radial band, a short delta segment, or a separate marker that does not compete with provider logo/text?
3. Is the expected pace marker necessary in the tray icon, or should the tray icon show only projected outcome while hover/menu explains pace and reset?
4. If a marker is necessary, should it be radial, tangential, notched, cut out of the ring, or placed outside the ring entirely?
5. Is the `5H` / `7D` text too expensive in pixel budget, or is it required because cycling multiple quotas otherwise becomes ambiguous?
6. Should the claw metaphor remain literal, or should the final tray icon prioritize a clearer system-meter metaphor and reserve claw branding for the app icon / README?

## Research Notes Found

1. Documented in commits: the original claw-meter art was AI-generated via FLUX2 Klein on `llm.vivid.fish`.
2. Documented in commits: KDE/Plasma tray downscaling drove the move toward bolder icons and multi-size pixmaps.
3. Documented in commits: projection at reset replaced raw utilization as the more actionable status basis.
4. Not found: a committed prior-art document comparing game HUDs, radial progress meters, Apple/Menu Bar conventions, Stripe-style controls, or quota/status tray apps.
5. Discussed but not durably documented: game HUD-style meters often combine fill, target/threshold, and alert color; our implementation has not yet found a tray-size version that reads cleanly.
