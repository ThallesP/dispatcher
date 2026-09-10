package main

import (
	"context"
	"log"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"
)

// Railway's ledger says how much each kickback payout was worth but not which
// template earned it; the template list says how much each template has earned
// in total (totalPayout) but keeps no history. Between two snapshots the two
// must agree: the credit payouts that arrived sum, per template, to that
// template's payout delta. (templateMetrics.totalEarnings converges to the
// same figure but trails the ledger by hours, so it is not used here.) So each new payout is matched to a template by finding the partition
// of the window's payouts whose per-template sums equal those deltas.
//
// Windows are one snapshot apart (hourly), so they almost always hold a single
// billing batch from one or two templates and the partition is unique. When
// several templates move in the same window and more than one partition fits,
// the per-template sums are still exact; only which row carries which label
// may be swapped between those templates.

// unattributedTemplateID marks a payout the matcher gave up on, so it stops
// holding the window open for everything after it.
const unattributedTemplateID = "unknown"

// untrackedTemplateID marks a payout older than the first snapshot: nothing
// can ever explain it, and it should not read as pending.
const untrackedTemplateID = "untracked"

// attributionHorizon bounds how far past a payout the matcher will extend its
// window looking for a snapshot that explains it. Earnings and ledger normally
// agree within one snapshot; two days of disagreement mean Railway's figures
// moved in a way snapshots cannot explain (a refund, a manual adjustment), and
// the payout is marked unattributed.
const attributionHorizon = 48 * time.Hour

// maxPartitionSteps caps the search so a pathological window can never stall
// the collector. Real windows resolve in a few dozen steps.
const maxPartitionSteps = 2_000_000

type pendingPayout struct {
	ID          string
	CreatedAt   time.Time
	AmountCents int64
}

type templateDelta struct {
	TemplateID string
	Name       string
	Cents      int64
}

// runAttribution is the cron entrypoint: attribute and just log.
func runAttribution(db *gorm.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	n, err := attributePayouts(ctx, db)
	if err != nil {
		log.Printf("payout attribution: %v", err)
		return
	}
	if n > 0 {
		log.Printf("payout attribution: %d payouts matched to templates", n)
	}
}

// attributePayouts assigns a template to every credit payout it can prove and
// returns how many it assigned. It works oldest-first, one window at a time:
// the baseline is the last snapshot before the oldest pending payout, and the
// window end is the first later snapshot whose per-template deltas the pending
// payouts up to it reproduce exactly. A payout no snapshot within the horizon
// explains is marked unattributed so the payouts after it get their own,
// tighter window. Payouts older than the first snapshot are never touched.
func attributePayouts(ctx context.Context, db *gorm.DB) (int, error) {
	first, err := scanTime(ctx, db, `SELECT MIN(sampled_at) FROM template_snapshots`)
	if err != nil || first == nil {
		return 0, err
	}
	if err := db.WithContext(ctx).Exec(`UPDATE payouts SET template_id = ?, template_name = 'before tracking'
		WHERE kind = 'credits' AND COALESCE(template_id, '') = '' AND created_at < ?`, untrackedTemplateID, *first).Error; err != nil {
		return 0, err
	}
	attributed := 0
	for {
		pending := []pendingPayout{}
		if err := db.WithContext(ctx).Raw(`
			SELECT id, created_at, amount_cents FROM payouts
			WHERE kind = 'credits' AND COALESCE(template_id, '') = '' AND created_at >= ?
			ORDER BY created_at, id`, *first).Scan(&pending).Error; err != nil || len(pending) == 0 {
			return attributed, err
		}
		oldest := pending[0]
		baseAt, err := scanTime(ctx, db, `SELECT MAX(sampled_at) FROM template_snapshots WHERE sampled_at <= ?`, oldest.CreatedAt)
		if err != nil || baseAt == nil {
			return attributed, err
		}
		ends := []time.Time{}
		if err := db.WithContext(ctx).Raw(`
			SELECT DISTINCT sampled_at FROM template_snapshots
			WHERE sampled_at > ? ORDER BY sampled_at`, *baseAt).Scan(&ends).Error; err != nil {
			return attributed, err
		}
		base, _, err := earningsAt(ctx, db, *baseAt)
		if err != nil {
			return attributed, err
		}
		matched := false
		for _, end := range ends {
			if end.Sub(oldest.CreatedAt) > attributionHorizon {
				break
			}
			window := []pendingPayout{}
			for _, p := range pending {
				if !p.CreatedAt.After(end) {
					window = append(window, p)
				}
			}
			current, names, err := earningsAt(ctx, db, end)
			if err != nil {
				return attributed, err
			}
			assignment := matchWindow(window, base, current)
			if assignment == nil {
				continue
			}
			byTemplate := map[string][]string{}
			for payoutID, templateID := range assignment {
				byTemplate[templateID] = append(byTemplate[templateID], payoutID)
			}
			for templateID, ids := range byTemplate {
				if err := db.WithContext(ctx).Exec(`UPDATE payouts SET template_id = ?, template_name = ? WHERE id IN ?`,
					templateID, names[templateID], ids).Error; err != nil {
					return attributed, err
				}
			}
			attributed += len(assignment)
			matched = true
			break
		}
		if matched {
			continue
		}
		if len(ends) == 0 || ends[len(ends)-1].Sub(oldest.CreatedAt) <= attributionHorizon {
			return attributed, nil // no snapshot beyond the horizon yet; try again after the next one
		}
		if err := db.WithContext(ctx).Exec(`UPDATE payouts SET template_id = ? WHERE id = ?`, unattributedTemplateID, oldest.ID).Error; err != nil {
			return attributed, err
		}
	}
}

// matchWindow returns the payout → template assignment for one window, or nil
// when the payouts and the earnings deltas do not add up to the cent.
func matchWindow(window []pendingPayout, base, current map[string]float64) map[string]string {
	var deltas []templateDelta
	var deltaSum, payoutSum int64
	for id, earned := range current {
		if cents := int64(math.Round((earned - base[id]) * 100)); cents > 0 {
			deltas = append(deltas, templateDelta{TemplateID: id, Cents: cents})
			deltaSum += cents
		}
	}
	for _, p := range window {
		payoutSum += p.AmountCents
	}
	if len(window) == 0 || deltaSum != payoutSum {
		return nil
	}
	return partitionPayouts(window, deltas)
}

// earningsAt returns total_payout (dollars) and name per template for the
// snapshot taken at exactly `at`. Every template in one collection shares the
// same sampled_at, so this reads one whole snapshot.
func earningsAt(ctx context.Context, db *gorm.DB, at time.Time) (map[string]float64, map[string]string, error) {
	rows := []struct {
		TemplateID  string
		Name        string
		TotalPayout float64
	}{}
	err := db.WithContext(ctx).Raw(`SELECT template_id, name, total_payout FROM template_snapshots WHERE sampled_at = ?`, at).Scan(&rows).Error
	earnings, names := map[string]float64{}, map[string]string{}
	for _, r := range rows {
		earnings[r.TemplateID] = r.TotalPayout
		names[r.TemplateID] = r.Name
	}
	return earnings, names, err
}

// partitionPayouts finds an assignment payout id → template id such that each
// template's payouts sum exactly to its delta, or nil if none exists.
// Depth-first, largest payout first, largest delta first; deterministic.
func partitionPayouts(payouts []pendingPayout, deltas []templateDelta) map[string]string {
	ps := append([]pendingPayout(nil), payouts...)
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].AmountCents != ps[j].AmountCents {
			return ps[i].AmountCents > ps[j].AmountCents
		}
		return ps[i].ID < ps[j].ID
	})
	ds := append([]templateDelta(nil), deltas...)
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Cents != ds[j].Cents {
			return ds[i].Cents > ds[j].Cents
		}
		return ds[i].TemplateID < ds[j].TemplateID
	})
	remaining := make([]int64, len(ds))
	for i, d := range ds {
		remaining[i] = d.Cents
	}
	out := map[string]string{}
	steps := 0
	var walk func(i int) bool
	walk = func(i int) bool {
		steps++
		if steps > maxPartitionSteps {
			return false
		}
		if i == len(ps) {
			for _, r := range remaining {
				if r != 0 {
					return false
				}
			}
			return true
		}
		for j := range ds {
			if remaining[j] < ps[i].AmountCents {
				continue
			}
			remaining[j] -= ps[i].AmountCents
			out[ps[i].ID] = ds[j].TemplateID
			if walk(i + 1) {
				return true
			}
			remaining[j] += ps[i].AmountCents
			delete(out, ps[i].ID)
		}
		return false
	}
	if !walk(0) {
		return nil
	}
	return out
}
