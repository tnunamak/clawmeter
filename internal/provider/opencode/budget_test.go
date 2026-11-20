package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/forecast"
	display "github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// goMeters is the Go plan part of a service response: three unlimited windows.
const goMeters = `"go":{"rolling":{"percent":10,"limited":false,"resetsAt":1800000000000},` +
	`"weekly":{"percent":20,"limited":false,"resetsAt":1800100000000},` +
	`"monthly":{"percent":30,"limited":false,"resetsAt":1800200000000}}`

// budgetJSON is one budget in the shape of OpenCode's public schema: $12.50 of a
// $50 limit, for each member, across all models.
const budgetJSON = `{"id":"bdg_b","limit":50,"spent":12.5,"scope":"each","per":"set",` +
	`"coverage":"all","models":[],"vendors":[],"shared":false,"picked":false,` +
	`"resetsAt":1800300000000}`

// servicePayload is a service response with the given Go part (empty for an
// account without Go) and the given budgets.
func servicePayload(goPart string, budgets ...string) string {
	if goPart != "" {
		goPart += ","
	}
	return `{"data":{"workspace":{"id":"w","usageURL":"u"},` + goPart +
		`"budgets":[` + strings.Join(budgets, ",") + `]}}`
}

// fetchWindows returns the windows of a service response served with status 200.
func fetchWindows(t *testing.T, body string) []provider.UsageWindow {
	t.Helper()
	p, _ := serve(t, 200, "application/json", body)
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return data.Windows
}

func TestBudgetIsADollarWindowWithNoLength(t *testing.T) {
	windows := fetchWindows(t, servicePayload(goMeters, budgetJSON))
	if len(windows) != 4 {
		t.Fatalf("got %d windows, want 3 Go windows and 1 budget", len(windows))
	}
	w := windows[3]
	if w.Name != "budget-1" || w.DisplayName != "Budget (all models)" || w.Scope != "all models" {
		t.Errorf("label = %q, %q, scope %q; want budget-1, Budget (all models), all models", w.Name, w.DisplayName, w.Scope)
	}
	if w.Currency != "USD" || w.Limit != 5000 || w.Used != 1250 || w.Utilization != 25 {
		t.Errorf("money = %s %d of %d at %v%%; want USD 1250 of 5000 at 25%%", w.Currency, w.Used, w.Limit, w.Utilization)
	}
	if got := w.MoneyDetail(); got != "$12.50 / $50.00" {
		t.Errorf("MoneyDetail = %q, want $12.50 / $50.00", got)
	}
	if !w.LengthUnknown || w.Length != 0 || forecast.WindowLength(w) != 0 {
		t.Errorf("budget has length %v (unknown %v); a budget must not be projected", w.Length, w.LengthUnknown)
	}
	if !w.ResetsAt.Equal(time.UnixMilli(1800300000000)) {
		t.Errorf("ResetsAt = %v, want the budget's reset", w.ResetsAt)
	}
	if want := "Each member has this limit. Spent counts across the budget's models."; w.Description != want {
		t.Errorf("Description = %q, want %q", w.Description, want)
	}
}

// The ID carries "_3h_", which a name parser would read as three hours. The
// window is named by position, and it still has no length.
func TestBudgetIDCannotSetALength(t *testing.T) {
	id := strings.Replace(budgetJSON, `"bdg_b"`, `"bdg_3h_x"`, 1)
	w := fetchWindows(t, servicePayload(goMeters, id))[3]
	if strings.Contains(w.Name, "3h") || provider.WindowLengthFromName(w.Name) != 0 || forecast.WindowLength(w) != 0 {
		t.Errorf("budget window %q takes a length from its ID: length %v", w.Name, forecast.WindowLength(w))
	}
}

// A budget can exist on an account without a Go plan, and it still shows.
func TestBudgetsShowWithoutAGoPlan(t *testing.T) {
	windows := fetchWindows(t, servicePayload("", budgetJSON))
	if len(windows) != 1 || windows[0].Name != "budget-1" {
		t.Fatalf("got %+v, want the one budget", windows)
	}
}

// Budgets are numbered in ID order, and a budget with no positive limit is
// skipped but keeps its number. Overspend is kept, so the money shows the spend.
func TestBudgetsAreNumberedByIDAndOverspendIsKept(t *testing.T) {
	mk := func(id, limit, spent string) string {
		return `{"id":"` + id + `","limit":` + limit + `,"spent":` + spent +
			`,"scope":"each","per":"set","coverage":"all","models":[],"vendors":[],` +
			`"shared":false,"picked":false,"resetsAt":1800300000000}`
	}
	windows := fetchWindows(t, servicePayload("", mk("c", "10", "12"), mk("a", "0", "0"), mk("b", "20", "5")))
	if len(windows) != 2 || windows[0].Name != "budget-2" || windows[1].Name != "budget-3" {
		t.Fatalf("got %d windows; want budget-2 (id b) and budget-3 (id c), with id a skipped", len(windows))
	}
	over := windows[1]
	if over.Utilization != 100 || over.Used != 1200 || over.Limit != 1000 {
		t.Errorf("over-budget window = %+v, want 100%% at 1200 of 1000", over)
	}
	if got := over.MoneyDetail(); got != "$12.00 / $10.00, over cap" {
		t.Errorf("MoneyDetail = %q, want $12.00 / $10.00, over cap", got)
	}
}

// The row label names what the budget counts. The tray shows the label as it
// is, because a budget has no length to put in front of it.
func TestBudgetLabelsNameWhatTheBudgetCounts(t *testing.T) {
	limit, spent, price := 10.0, 1.0, 5.0
	five := []budgetModel{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}, {Name: "E"}}
	cases := []struct {
		name, label, scope string
		b                  budget
	}{
		{"all models", "Budget (all models)", "all models", budget{Coverage: "all"}},
		{"per model", "Per-model budget (all models)", "all models", budget{Coverage: "all", Per: "model"}},
		{"picked models", "Budget (GLM-5.3, Kimi K2.5)", "GLM-5.3, Kimi K2.5", budget{
			Coverage: "models",
			Models:   []budgetModel{{ModelID: "glm", Name: "GLM-5.3"}, {ModelID: "kimi", Name: "Kimi K2.5"}},
		}},
		{"more than three models", "Budget (5 models)", "A, B, C, D, E", budget{Coverage: "models", Models: five}},
		{"vendors", "Budget (anthropic, openai)", "anthropic, openai", budget{Coverage: "models", Vendors: []string{"anthropic", "openai"}}},
		{"price", "Budget (models from $5 per 1M input tokens)", "models from $5 per 1M input tokens", budget{
			Coverage:          "models",
			InputPriceAtLeast: &price,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.b
			b.ID, b.Limit, b.Spent = "x", &limit, &spent
			b.ResetsAt = json.RawMessage("1800300000000")
			windows, err := budgetWindows([]budget{b})
			if err != nil || len(windows) != 1 {
				t.Fatalf("budgetWindows = %d windows, error %v", len(windows), err)
			}
			w := windows[0]
			if w.DisplayName != tc.label || w.Scope != tc.scope {
				t.Errorf("label %q, scope %q; want %q, %q", w.DisplayName, w.Scope, tc.label, tc.scope)
			}
			if display.Code(w) != tc.label || display.Qualifier(w) != "" {
				t.Errorf("row shows code %q and qualifier %q; want the label alone", display.Code(w), display.Qualifier(w))
			}
		})
	}
}

func TestBudgetDescriptionSaysHowTheLimitApplies(t *testing.T) {
	got := budgetDescription(budget{Scope: "combined", Per: "model", Shared: true})
	want := "The whole workspace shares this limit. Each model has its own limit, and spent is the most spent on one model. Reaching it pauses model use for everyone in the workspace."
	if got != want {
		t.Errorf("budgetDescription = %q, want %q", got, want)
	}
}

// A budget without spent is not guessed at: the fetch fails, and the message
// names the budget.
func TestBudgetMissingSpentIsAnError(t *testing.T) {
	noSpent := `{"id":"bdg_b","limit":50,"scope":"each","per":"set","coverage":"all","models":[],` +
		`"vendors":[],"shared":false,"picked":false,"resetsAt":1800300000000}`
	p, _ := serve(t, 200, "application/json", servicePayload(goMeters, noSpent))
	if msg := fetchErr(t, p); !strings.Contains(msg, "budget") {
		t.Errorf("error %q does not name the budget", msg)
	}
}
