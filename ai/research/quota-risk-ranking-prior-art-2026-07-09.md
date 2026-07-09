# Quota Risk Ranking Prior Art

Date: 2026-07-09

## Question

When multiple AI quota windows are over pace, should Clawmeter's Auto Risk tray target sort by highest projected percent at reset, or by the quota that is likely to block the user soonest?

## Sources

- Google SRE Workbook, "Alerting on SLOs": burn rate links budget consumption rate to time to exhaustion, and multi-window alerts reduce false positives by checking whether the burn is still active. <https://sre.google/workbook/alerting-on-slos/>
- Datadog, "Proactively monitor service performance with SLO alerts": burn-rate alerts combine long and short windows to reduce flapping while retaining fast recovery. <https://www.datadoghq.com/blog/monitor-service-performance-with-slo-alerts/>
- AWS Budgets best practices: forecast alerts are separate from actual alerts and require enough history before AWS generates forecasts. <https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-best-practices.html>
- Grafana alerting docs: no-data/error states can keep last state, but they remain distinct states and should not be treated as fresh measurements. <https://grafana.com/docs/grafana/latest/alerting/fundamentals/alert-rule-evaluation/nodata-and-error-states/>

## Decision

Keep Clawmeter's estimator simple and auditable. Do not add a black-box forecasting model for tray ranking.

For Auto Risk, order quota windows by factual urgency:

1. Expired/auth/error states remain separate operational tiers.
2. Quotas projected to run out rank ahead of merely tight quotas.
3. Among quotas projected to run out, the earlier `runs_out_in` ranks first.
4. If runout time ties, the larger blocked gap before reset ranks first.
5. If neither quota is projected to run out, higher projected percent remains the tiebreaker.

This preserves Clawmeter's core UX: show calculated facts at the edge of what is glanceable, without turning the tray icon into a recommendation engine.

## Non-Decision

Do not use recent short-window burn rate, day/night history, weekday/weekend history, or anomaly detection yet. Those may become useful, but they would change Clawmeter from a transparent pace meter into a predictor. The current product need is better ordering of already-shown facts, not more hidden inference.
