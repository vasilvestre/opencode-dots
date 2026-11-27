package repos

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

func init() { Register(budget{}) }

// maxDailyUSD is the largest daily cap accepted: a typo of a zero too many must not
// pass as a cap that never trips.
const maxDailyUSD = 10000

// budget caps what the dot's autonomous actions on a repository may spend in one UTC
// day (ADR 0036, first step). It is opt-in: with no key nothing is capped, nothing is
// read and no generated file changes.
type budget struct{}

func (budget) Key() string { return "budget" }
func (budget) Doc() string {
	return "daily spend cap on autonomous actions: {daily_usd: N}, a positive number up to 10000 with at most 2 decimals; once the repository's runs cost N in a UTC day the dot stops opening, pushing and merging until the next day (bin/budget); needs autonomy act or auto, comments stay allowed; a profile is where a company sets it (docs/budget.md)"
}

func (budget) Check(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("must be a block with `daily_usd`")
	}
	for k := range m {
		if k != "daily_usd" {
			return fmt.Errorf("unknown key %q: only `daily_usd` is accepted", k)
		}
	}
	_, err := dailyUSD(m)
	return err
}

func (budget) Emit(*Resolved, *Output) error { return nil }

// Modify refuses a cap on a repository that spends nothing autonomous (observe and
// propose prepare and hold, a person releases), and hands the rules a `budget` input:
// `daily_usd=N`. A workflow that does not declare the input ignores it (ADR 0033).
func (budget) Modify(r *Resolved, out *Output, from int) error {
	val := budgetInput(r)
	if val == "" {
		return nil // Check already reported it
	}
	if lvl := EffectiveAutonomy(r); lvl != "act" && lvl != "auto" {
		return fmt.Errorf("needs autonomy act or auto, this repository is at %s and spends nothing autonomous that a cap could bound; remove budget or raise autonomy", lvl)
	}
	for i := from; i < len(out.Rules); i++ {
		if out.Rules[i].Inputs == nil {
			out.Rules[i].Inputs = map[string]string{}
		}
		out.Rules[i].Inputs["budget"] = val
	}
	return nil
}

// dailyUSD reads and validates the cap of a budget block.
func dailyUSD(m map[string]any) (float64, error) {
	raw, ok := m["daily_usd"]
	if !ok {
		return 0, errors.New("daily_usd is required")
	}
	var x float64
	switch n := raw.(type) {
	case int:
		x = float64(n)
	case int64:
		x = float64(n)
	case uint64:
		x = float64(n)
	case float64:
		x = n
	default:
		return 0, errors.New("daily_usd must be a number")
	}
	if x <= 0 || x > maxDailyUSD {
		return 0, fmt.Errorf("daily_usd must be above 0 and at most %d, got %v", maxDailyUSD, x)
	}
	if math.Abs(x*100-math.Round(x*100)) > 1e-6 {
		return 0, fmt.Errorf("daily_usd takes at most 2 decimals, got %v", x)
	}
	return math.Round(x*100) / 100, nil
}

// budgetCap is the cap of a repository as written in tend.list (`budget=N`) and in
// the rule input (`daily_usd=N`): empty when the repository sets no valid budget.
func budgetCap(r *Resolved) string {
	m, ok := r.Values["budget"].(map[string]any)
	if !ok {
		return ""
	}
	x, err := dailyUSD(m)
	if err != nil {
		return ""
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

func budgetInput(r *Resolved) string {
	if c := budgetCap(r); c != "" {
		return "daily_usd=" + c
	}
	return ""
}
