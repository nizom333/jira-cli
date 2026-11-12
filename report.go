package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// reportWindow — Report window: календарный месяц в поясе пользователя, [start, end).
type reportWindow struct {
	start, end time.Time
}

// reportWindowFor строит Report window для месяца arg (YYYY-MM; пусто — текущий месяц now) в поясе loc.
func reportWindowFor(arg string, now time.Time, loc *time.Location) (reportWindow, error) {
	now = now.In(loc)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	if arg != "" {
		t, err := time.ParseInLocation("2006-01", arg, loc)
		if err != nil {
			return reportWindow{start, start}, fmt.Errorf("месяц: ожидается YYYY-MM")
		}
		start = t
	}
	return reportWindow{start, start.AddDate(0, 1, 0)}, nil
}

// month — метка месяца отчёта, YYYY-MM.
func (w reportWindow) month() string { return w.start.Format("2006-01") }

// locate разбирает время Jira и переводит его в пояс окна; in — попадает в окно;
// err — Unreadable date.
func (w reportWindow) locate(ts string) (t time.Time, in bool, err error) {
	t, err = time.Parse(jiraTime, ts)
	if err != nil {
		return t, false, err
	}
	t = t.In(w.start.Location())
	return t, !t.Before(w.start) && t.Before(w.end), nil
}

type reportRow struct {
	Key      string   `json:"key"`
	Status   string   `json:"status"`
	Estimate duration `json:"estimate_h"`
	Spent    duration `json:"spent_h"` // Spent in month
	// ClosedOn — локальная дата закрытия, только если задача Closed in month.
	// Ключ JSON "resolved" — по тому же полю Jira resolutiondate, что в show --json
	// (там дата сервера, здесь локальная); входит ли задача в отчёт, решает Closed in month.
	ClosedOn string `json:"resolved,omitempty"`
	Summary  string `json:"summary"`
}

// daySpent — Spent in month за один день (локальная дата YYYY-MM-DD).
type daySpent struct {
	date  string
	spent duration
}

// monthReport — рассчитанный Month report.
// Списки всегда non-nil (в JSON — массивы, [] если пусто).
type monthReport struct {
	rows           []reportRow
	days           []daySpent // по возрастанию даты
	estimateClosed duration   // Estimate of closed
	spent          duration   // Spent in month по всем задачам
	closed         int        // число строк Closed in month
	warnings       []string
}

// buildMonthReport считает Month report пользователя user за окно w по Report candidate'ам
// (worklog'и у issues уже полные). Closed in month — исполнитель user и закрытие в окне;
// Spent in month — только worklog'и user, начатые в окне. Username без учёта регистра.
// Строка есть, только если задача Closed in month или Spent in month > 0; остальные Report candidate'ы отброшены.
// Unreadable date, влияющие на отчёт (started у worklog'ов user, непустая resolutiondate у задач
// исполнителя user), не теряются молча — они попадают в warnings.
func buildMonthReport(issues []Issue, user string, w reportWindow) monthReport {
	rep := monthReport{rows: []reportRow{}, days: []daySpent{}, warnings: []string{}}
	byDay := map[string]duration{}
	var bad int
	var badKeys []string // в порядке первого появления, без повторов
	seen := map[string]bool{}
	unreadable := func(key string) {
		bad++
		if !seen[key] {
			seen[key] = true
			badKeys = append(badKeys, key)
		}
	}
	for _, is := range issues {
		f := is.Fields
		r := reportRow{Key: is.Key, Status: f.Status.Name, Summary: f.Summary, Estimate: f.TimeOriginalEstimate}
		for _, wl := range f.Worklog.Worklogs {
			if !strings.EqualFold(wl.Author.Name, user) {
				continue
			}
			t, in, err := w.locate(wl.Started)
			if err != nil {
				unreadable(is.Key)
			}
			if in {
				r.Spent += wl.TimeSpent
				byDay[t.Format(dayLayout)] += wl.TimeSpent
			}
		}
		if f.Assignee != nil && strings.EqualFold(f.Assignee.Name, user) && f.ResolutionDate != "" {
			t, in, err := w.locate(f.ResolutionDate)
			if err != nil {
				unreadable(is.Key)
			}
			if in {
				r.ClosedOn = t.Format(dayLayout)
				rep.estimateClosed += r.Estimate
				rep.closed++
			}
		}
		if r.ClosedOn == "" && r.Spent == 0 {
			continue // Report candidate (в т.ч. из расширения дат), не вошедший в отчёт
		}
		rep.spent += r.Spent
		rep.rows = append(rep.rows, r)
	}
	if bad > 0 {
		rep.warnings = append(rep.warnings,
			fmt.Sprintf("записей с неразборчивой датой: %d (%s)", bad, strings.Join(badKeys, ", ")))
	}
	for d, s := range byDay {
		rep.days = append(rep.days, daySpent{d, s})
	}
	sort.Slice(rep.days, func(i, j int) bool { return rep.days[i].date < rep.days[j].date })
	return rep
}

// cmdReport: Month report — JQL с запасом по датам, дозагрузка worklog'ов, расчёт buildMonthReport;
// результат — по задачам (reportByIssue) или --by day (reportByDay).
// reportFields — поля Report candidate, которые читает buildMonthReport.
const reportFields = "summary,status,assignee,resolutiondate,timeoriginalestimate,worklog"

func cmdReport(c *Client, a Args) (result, error) {
	if by := a.get("by"); by != "" && by != "day" {
		return nil, fmt.Errorf("--by: только day")
	}
	w, err := reportWindowFor(a.arg(0), time.Now(), time.Local) // до запросов к Jira
	if err != nil {
		return nil, err
	}
	who, err := c.filterUser(a.get("user"))
	if err != nil {
		return nil, err
	}
	user := who.Name
	// Jira считает даты JQL в поясе профиля (Etc/UTC), а окно — в локальном: ищем с запасом ±1 день,
	// точную границу проводит buildMonthReport.
	s := jqlQuote(w.start.AddDate(0, 0, -1).Format(dayLayout))
	e := jqlQuote(w.end.AddDate(0, 0, 1).Format(dayLayout))
	u := jqlQuote(user)
	jql := fmt.Sprintf("((assignee = %s AND resolved >= %s AND resolved < %s) OR "+
		"(worklogAuthor = %s AND worklogDate >= %s AND worklogDate < %s))",
		u, s, e, u, s, e)
	if p := a.get("project"); p != "" {
		jql += " AND project = " + jqlQuote(p)
	}
	jql += " ORDER BY key"
	issues, err := c.searchAll(jql, reportFields)
	if err != nil {
		return nil, err
	}
	for i, is := range issues {
		if wl := is.Fields.Worklog; wl.Total > len(wl.Worklogs) { // в поиске Jira отдаёт только первые 20 записей
			full, err := c.issueWorklogs(is.Key)
			if err != nil {
				return nil, err
			}
			issues[i].Fields.Worklog.Worklogs = full
		}
	}
	rep := buildMonthReport(issues, user, w)
	if a.get("by") == "day" {
		return newReportByDay(rep, w.month(), user), nil
	}
	return newReportByIssue(rep, w.month(), user), nil
}

// reportByIssue — результат report: строки по задачам и итоги.
type reportByIssue struct {
	ClosedCount         int         `json:"closed_count"`
	Issues              []reportRow `json:"issues"`
	IssuesCount         int         `json:"issues_count"`
	Month               string      `json:"month"`
	TotalEstimateClosed duration    `json:"total_estimate_closed_h"`
	TotalSpent          duration    `json:"total_spent_h"`
	User                string      `json:"user"`
	Warnings            []string    `json:"warnings"`
}

func newReportByIssue(rep monthReport, month, user string) *reportByIssue {
	return &reportByIssue{Month: month, User: user, Issues: rep.rows,
		TotalEstimateClosed: rep.estimateClosed, TotalSpent: rep.spent,
		IssuesCount: len(rep.rows), ClosedCount: rep.closed, Warnings: rep.warnings}
}

func (r *reportByIssue) text(w io.Writer) {
	fmt.Fprintf(w, "# %s · %s · KEY ОЦЕНКА СПИСАНО ЗАКРЫТА STATUS SUMMARY\n", r.Month, oneLine(r.User))
	for _, row := range r.Issues {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(row.Key), row.Estimate, row.Spent, dash(row.ClosedOn),
			oneLine(row.Status), oneLine(row.Summary))
	}
	fmt.Fprintf(w, "# итого: оценка закрытых %s · списано %s · задач %d, закрыто %d\n",
		r.TotalEstimateClosed, r.TotalSpent, r.IssuesCount, r.ClosedCount)
	writeWarnings(w, r.Warnings)
}

type reportDay struct {
	Date  string   `json:"date"`
	Spent duration `json:"spent_h"`
}

// reportByDay — результат report --by day.
type reportByDay struct {
	Days       []reportDay `json:"days"`
	Month      string      `json:"month"`
	TotalSpent duration    `json:"total_spent_h"`
	User       string      `json:"user"`
	Warnings   []string    `json:"warnings"`
}

func newReportByDay(rep monthReport, month, user string) *reportByDay {
	r := &reportByDay{Month: month, User: user, Days: []reportDay{}, TotalSpent: rep.spent, Warnings: rep.warnings}
	for _, d := range rep.days {
		r.Days = append(r.Days, reportDay{Date: d.date, Spent: d.spent})
	}
	return r
}

func (r *reportByDay) text(w io.Writer) {
	fmt.Fprintf(w, "# %s · %s · списано по дням\n", r.Month, oneLine(r.User))
	for _, d := range r.Days {
		fmt.Fprintf(w, "%s\t%s\n", d.Date, d.Spent)
	}
	fmt.Fprintf(w, "# итого списано: %s\n", r.TotalSpent)
	writeWarnings(w, r.Warnings)
}

// writeWarnings: предупреждение — одна строка; в нём ключи задач, поэтому через oneLine.
func writeWarnings(w io.Writer, warnings []string) {
	for _, s := range warnings {
		fmt.Fprintf(w, "# предупреждение: %s\n", oneLine(s))
	}
}
