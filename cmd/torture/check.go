package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aniruddha81/task-queue/internal/queue"
)

type row struct {
	name, claim string
	evidence    int    // how often the guarantee's fault was exercised
	exercised   string // what evidence counts
	violations  []string
}

func (r row) pass() bool { return len(r.violations) == 0 && r.evidence > 0 }

type report struct {
	runID, mutant string
	seed          uint64
	duration      time.Duration
	faults        map[string]int
	stats         []string
	rows          []row
}

func (r *report) passed() bool {
	for _, x := range r.rows {
		if !x.pass() {
			return false
		}
	}
	return true
}

type jobRow struct {
	id, owner, key, typ, state string
	attempt                    int
	runAt                      time.Time
	request                    []byte
}

type attemptRow struct {
	attempt   int
	outcome   *string
	startedAt time.Time
	late      *string
}

func check(ctx context.Context, c config, l *load, runID string, start time.Time, settled bool, epochs int64, faults map[string]int) (*report, error) {
	jdb, err := pgx.Connect(ctx, c.jobsDB)
	if err != nil {
		return nil, err
	}
	defer jdb.Close(ctx)
	sdb, err := pgx.Connect(ctx, c.sinksDB)
	if err != nil {
		return nil, err
	}
	defer sdb.Close(ctx)

	// Source 2: the jobs database.
	jobs := map[string]jobRow{} // by id
	byKey := map[string][]jobRow{}
	rows, _ := jdb.Query(ctx, `SELECT id::text, owner_id::text, idempotency_key, type, state, attempt, run_at, request
		FROM jobs WHERE idempotency_key LIKE $1 || '-%' OR idempotency_key LIKE 'cron:' || $2 || ':%'`, runID, l.scheduleID)
	for rows.Next() {
		var j jobRow
		if err := rows.Scan(&j.id, &j.owner, &j.key, &j.typ, &j.state, &j.attempt, &j.runAt, &j.request); err != nil {
			return nil, err
		}
		jobs[j.id] = j
		byKey[j.key] = append(byKey[j.key], j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	attempts := map[string][]attemptRow{}
	rows, _ = jdb.Query(ctx, `SELECT job_id::text, attempt, outcome, started_at, late_result FROM job_attempts
		WHERE job_id = ANY($1::uuid[]) ORDER BY job_id, attempt`, ids)
	for rows.Next() {
		var id string
		var a attemptRow
		if err := rows.Scan(&id, &a.attempt, &a.outcome, &a.startedAt, &a.late); err != nil {
			return nil, err
		}
		attempts[id] = append(attempts[id], a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	r := &report{runID: runID, seed: c.seed, duration: c.duration, faults: faults, mutant: os.Getenv("MUTANT")}
	states := map[string]int{}
	for _, j := range jobs {
		states[j.state]++
	}
	r.stats = append(r.stats, fmt.Sprintf("%d jobs submitted, %d acknowledgements, %d jobs in the database: %v",
		l.submitted.Load(), l.acked.Load(), len(jobs), states))

	// G1: every acknowledged job exists. Source 1 (the ack log) against source 2.
	g1 := row{name: "G1", claim: "An acknowledged job is never lost",
		exercised: "service kills and database primary crashes (failovers)", evidence: faults["kill"] + faults["crash-primary"]}
	for _, a := range l.log {
		if _, ok := jobs[a.id]; !ok {
			g1.violations = append(g1.violations, fmt.Sprintf("acknowledged %s (key %s) is not in the database", a.id, a.key))
		}
	}

	// G2: one job per key; a changed body gets 409.
	g2 := row{name: "G2", claim: "Same key, same job; a changed request gets 409", exercised: "repeated keys and conflicting resubmits"}
	idsByKey := map[string]map[string]bool{}
	for _, a := range l.log {
		if idsByKey[a.key] == nil {
			idsByKey[a.key] = map[string]bool{}
		}
		idsByKey[a.key][a.id] = true
	}
	acksPerKey := map[string]int{}
	for _, a := range l.log {
		acksPerKey[a.key]++
	}
	for k, set := range idsByKey {
		if len(set) > 1 {
			g2.violations = append(g2.violations, fmt.Sprintf("key %s acknowledged as %d different jobs", k, len(set)))
		}
		if acksPerKey[k] > 1 {
			g2.evidence++
		}
	}
	g2.evidence += l.conflicts
	g2.violations = append(g2.violations, l.bad...)

	// G3: at most one succeeded attempt per job, and it is the last attempt.
	g3 := row{name: "G3", claim: "Only the current lease holder records a result", exercised: "late results rejected by fencing"}
	for id, as := range attempts {
		succeeded, last := 0, as[len(as)-1].attempt
		for _, a := range as {
			if a.late != nil {
				g3.evidence++
			}
			if a.outcome != nil && *a.outcome == "succeeded" {
				succeeded++
				if a.attempt != last {
					g3.violations = append(g3.violations, fmt.Sprintf("job %s: attempt %d succeeded but attempt %d exists", id, a.attempt, last))
				}
			}
		}
		if succeeded > 1 {
			g3.violations = append(g3.violations, fmt.Sprintf("job %s: %d succeeded attempts", id, succeeded))
		}
	}

	// G4: source 3, the sinks. Each effect at most once, exactly once for succeeded jobs.
	g4 := row{name: "G4", claim: "Effects at most once, exactly once on success", exercised: "duplicate deliveries absorbed by the sinks"}
	effects := func(table, like string) (map[string]int, error) {
		m := map[string]int{}
		rows, _ := sdb.Query(ctx, `SELECT effect_key, count(*) FROM `+table+` WHERE effect_key LIKE $1 GROUP BY effect_key`, like)
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return nil, err
			}
			m[k] = n
		}
		return m, rows.Err()
	}
	ledger, err1 := effects("ledger_entries", "%:"+runID+"-%")
	hooks, err2 := effects("webhook_deliveries", "%:"+runID+"-%")
	digests, err3 := effects("digests", "%:cron:"+l.scheduleID+":%")
	if err := firstErr(err1, err2, err3); err != nil {
		return nil, err
	}
	sdb.QueryRow(ctx, `SELECT count(*) FROM sink_calls WHERE result = 'duplicate' AND (effect_key LIKE $1 OR effect_key LIKE $2)`,
		"%:"+runID+"-%", "%:cron:"+l.scheduleID+":%").Scan(&g4.evidence)
	for _, sink := range []struct {
		typ   string
		count map[string]int
	}{{"ledger.transfer", ledger}, {"webhook.deliver", hooks}, {"report.daily", digests}} {
		seen := map[string]bool{}
		for _, j := range jobs {
			if j.typ != sink.typ {
				continue
			}
			ek := j.owner + ":" + j.key
			seen[ek] = true
			n := sink.count[ek]
			if n > 1 || (j.state == "succeeded" && n != 1) {
				g4.violations = append(g4.violations, fmt.Sprintf("%s job %s (%s): %d effects", sink.typ, j.id, j.state, n))
			}
		}
		for ek, n := range sink.count {
			if !seen[ek] {
				g4.violations = append(g4.violations, fmt.Sprintf("%s: %d effects for unknown key %s", sink.typ, n, ek))
			}
		}
	}
	// The ledger oracle: each account's balance equals the replay of its recorded entries.
	net := map[string]int64{}
	entries, _ := sdb.Query(ctx, `SELECT from_account, to_account, amount FROM ledger_entries WHERE effect_key LIKE $1`, "%:"+runID+"-%")
	for entries.Next() {
		var from, to string
		var amt int64
		entries.Scan(&from, &to, &amt)
		net[from] -= amt
		net[to] += amt
	}
	var total int64
	balances, _ := sdb.Query(ctx, `SELECT account, balance FROM ledger_accounts WHERE account LIKE $1 || ':%'`, runID)
	for balances.Next() {
		var acct string
		var bal int64
		balances.Scan(&acct, &bal)
		total += bal
		if bal != net[acct] {
			g4.violations = append(g4.violations, fmt.Sprintf("account %s: balance %d, entries say %d", acct, bal, net[acct]))
		}
	}
	if total != 0 {
		g4.violations = append(g4.violations, fmt.Sprintf("ledger total is %d, want 0", total))
	}

	// Email is at-least-once by design (not G4): every succeeded email arrived, maybe twice.
	email := row{name: "Email", claim: "At-least-once delivery (SMTP can't deduplicate)", exercised: "succeeded email jobs"}
	delivered, err := mailpitMessageIDs(ctx, c.mailpit)
	if err != nil {
		email.violations = append(email.violations, "reading Mailpit: "+err.Error())
	}
	dups := 0
	for _, n := range delivered {
		dups += n - 1
	}
	for _, j := range jobs {
		if j.typ == "email.send" && j.state == "succeeded" {
			email.evidence++
			sum := sha256.Sum256([]byte(j.owner + ":" + j.key))
			if delivered[hex.EncodeToString(sum[:16])+"@task-queue.local"] == 0 {
				email.violations = append(email.violations, fmt.Sprintf("email job %s succeeded but nothing arrived", j.id))
			}
		}
	}
	r.stats = append(r.stats, fmt.Sprintf("email: %d messages, %d duplicate deliveries (expected: at-least-once)", sumValues(delivered), dups))

	// G5: every job finished.
	g5 := row{name: "G5", claim: "Every job ends succeeded, dead or cancelled", exercised: "jobs that finished", evidence: len(jobs)}
	if !settled {
		for _, j := range jobs {
			if j.state == "available" || j.state == "running" {
				g5.violations = append(g5.violations, fmt.Sprintf("job %s still %s after quiescing", j.id, j.state))
			}
		}
	}

	// G6: exactly one job per tick, none skipped.
	g6 := row{name: "G6", claim: "Cron: one job per tick, none skipped", exercised: "leader changes while ticking", evidence: int(epochs)}
	var next time.Time
	jdb.QueryRow(ctx, `SELECT next_tick_at FROM schedules WHERE id = $1`, l.scheduleID).Scan(&next)
	var ticks []time.Time
	trows, _ := jdb.Query(ctx, `SELECT tick_at FROM schedule_ticks WHERE schedule_id = $1 ORDER BY tick_at`, l.scheduleID)
	for trows.Next() {
		var t time.Time
		trows.Scan(&t)
		ticks = append(ticks, t)
	}
	cr, _ := queue.ParseCron("* * * * *", "UTC")
	var want []time.Time
	for t := l.firstTick; t.Before(next); t = cr.Next(t) {
		want = append(want, t)
	}
	if len(ticks) == 0 || !slices.EqualFunc(ticks, want, time.Time.Equal) {
		g6.violations = append(g6.violations, fmt.Sprintf("ticks %v, want %v", fmtTimes(ticks), fmtTimes(want)))
	}
	cronJobs := 0
	for _, j := range jobs {
		if strings.HasPrefix(j.key, "cron:") {
			cronJobs++
		}
	}
	if cronJobs != len(ticks) {
		g6.violations = append(g6.violations, fmt.Sprintf("%d cron jobs for %d ticks", cronJobs, len(ticks)))
	}

	// G7: chores of different epochs never overlap.
	g7 := row{name: "G7", claim: "One leader per epoch: chores never overlap", exercised: "leader changes", evidence: int(epochs)}
	var overlaps int
	jdb.QueryRow(ctx, `SELECT count(*) FROM chore_runs a JOIN chore_runs b
		ON a.epoch < b.epoch AND a.committed_at > b.started_at
		WHERE a.started_at >= $1 AND b.started_at >= $1`, start).Scan(&overlaps)
	if overlaps > 0 {
		g7.violations = append(g7.violations, fmt.Sprintf("%d pairs of chores from different epochs overlapped", overlaps))
	}

	// G8: nothing starts before its run_at.
	g8 := row{name: "G8", claim: "No attempt starts before run_at", exercised: "delayed jobs"}
	for _, j := range jobs {
		as := attempts[j.id]
		if len(as) == 0 {
			continue
		}
		var req struct {
			RunAt *time.Time `json:"run_at"`
		}
		json.Unmarshal(j.request, &req)
		if req.RunAt != nil {
			g8.evidence++
			if as[0].startedAt.Before(*req.RunAt) {
				g8.violations = append(g8.violations, fmt.Sprintf("job %s started %v before its run_at %v", j.id, as[0].startedAt, *req.RunAt))
			}
		}
		if j.state == "succeeded" && as[len(as)-1].startedAt.Before(j.runAt) {
			g8.violations = append(g8.violations, fmt.Sprintf("job %s: last attempt started before run_at", j.id))
		}
	}

	r.rows = []row{g1, g2, g3, g4, email, g5, g6, g7, g8}
	return r, nil
}

func readEpoch(ctx context.Context, url string) (int64, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return 0, err
	}
	defer conn.Close(ctx)
	var e int64
	err = conn.QueryRow(ctx, `SELECT epoch FROM leader_leases WHERE name = 'scheduler'`).Scan(&e)
	return e, err
}

func unfinished(ctx context.Context, url, runID, scheduleID string) (int, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return -1, err
	}
	defer conn.Close(ctx)
	var n int
	err = conn.QueryRow(ctx, `SELECT count(*) FROM jobs
		WHERE (idempotency_key LIKE $1 || '-%' OR idempotency_key LIKE 'cron:' || $2 || ':%')
		  AND state IN ('available', 'running')`, runID, scheduleID).Scan(&n)
	return n, err
}

// mailpitMessageIDs counts delivered messages by Message-ID.
func mailpitMessageIDs(ctx context.Context, base string) (map[string]int, error) {
	out := map[string]int{}
	for start := 0; ; start += 500 {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v1/messages?start=%d&limit=500", base, start), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return out, err
		}
		var page struct {
			Messages []struct{ MessageID string }
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return out, err
		}
		for _, m := range page.Messages {
			out[strings.Trim(m.MessageID, "<>")]++
		}
		if len(page.Messages) < 500 {
			return out, nil
		}
	}
}

func (r *report) markdown() string {
	var b strings.Builder
	verdict := "PASS"
	if !r.passed() {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "# Chaos run %s: %s\n\n", r.runID, verdict)
	fmt.Fprintf(&b, "Seed `%d` · %v of load and faults", r.seed, r.duration)
	if r.mutant != "" {
		fmt.Fprintf(&b, " · **mutant build `%s`** (expected to FAIL)", r.mutant)
	}
	b.WriteString("\n\n")
	for _, s := range r.stats {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	b.WriteString("\n## Faults injected\n\n| Fault | Times |\n| --- | --- |\n")
	names := make([]string, 0, len(r.faults))
	for k := range r.faults {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(&b, "| %s | %d |\n", k, r.faults[k])
	}
	b.WriteString("\n## Guarantees\n\n| | Guarantee | Result | Exercised | Violations |\n| --- | --- | --- | --- | --- |\n")
	for _, x := range r.rows {
		res := "PASS"
		if !x.pass() {
			res = "**FAIL**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d %s | %d |\n", x.name, x.claim, res, x.evidence, x.exercised, len(x.violations))
	}
	for _, x := range r.rows {
		if len(x.violations) == 0 && x.evidence > 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", x.name)
		if x.evidence == 0 {
			b.WriteString("- Not exercised: its fault never happened, so nothing was proven.\n")
		}
		for i, v := range x.violations {
			if i == 10 {
				fmt.Fprintf(&b, "- … and %d more\n", len(x.violations)-10)
				break
			}
			fmt.Fprintf(&b, "- %s\n", v)
		}
	}
	return b.String()
}

func (r *report) write(dir, runID string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "chaos-" + runID
	if r.mutant != "" {
		name += "-" + r.mutant
	}
	path := filepath.Join(dir, name+".md")
	return path, os.WriteFile(path, []byte(r.markdown()), 0o644)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func sumValues(m map[string]int) (n int) {
	for _, v := range m {
		n += v
	}
	return n
}

func fmtTimes(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.UTC().Format("15:04")
	}
	return out
}
