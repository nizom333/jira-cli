package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Пояс пользователя в тестах Month report: +05 (Ташкент).
var zone05 = time.FixedZone("+05", 5*3600)

func octoberWindow(t *testing.T) reportWindow {
	t.Helper()
	w, err := reportWindowFor("2026-10", time.Date(2026, 11, 11, 18, 0, 0, 0, zone05), zone05)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func wl(author, started string, sec int) Worklog {
	return Worklog{Author: User{Name: author}, Started: started, TimeSpent: duration(sec)}
}

// issue — Report candidate: assignee/resolved пустые, если не заданы.
func issue(key, assignee, resolved string, est int, wls ...Worklog) Issue {
	var is Issue
	is.Key = key
	if assignee != "" {
		is.Fields.Assignee = &User{Name: assignee}
	}
	is.Fields.ResolutionDate = resolved
	is.Fields.TimeOriginalEstimate = duration(est)
	is.Fields.Worklog.Total = len(wls)
	is.Fields.Worklog.Worklogs = wls
	return is
}

func row(key, resolved string, est, spent int) reportRow {
	return reportRow{Key: key, ClosedOn: resolved, Estimate: duration(est), Spent: duration(spent)}
}

func TestBuildMonthReport(t *testing.T) {
	cases := []struct {
		name   string
		issues []Issue
		want   monthReport
	}{
		{
			name: "Spent in month: worklog 23:30 последнего дня по +05 входит, 00:10 первого числа следующего — нет",
			issues: []Issue{issue("K-1", "", "", 0,
				wl("u", "2026-10-31T23:30:00.000+0500", 1800),
				wl("u", "2026-11-01T00:10:00.000+0500", 600))},
			want: monthReport{
				rows:  []reportRow{row("K-1", "", 0, 1800)},
				days:  []daySpent{{"2026-10-31", 1800}},
				spent: 1800,
			},
		},
		{
			name: "Spent in month: чужой worklog не считается",
			issues: []Issue{issue("K-1", "", "", 0,
				wl("other", "2026-10-10T12:00:00.000+0500", 3600),
				wl("u", "2026-10-10T13:00:00.000+0500", 900))},
			want: monthReport{
				rows:  []reportRow{row("K-1", "", 0, 900)},
				days:  []daySpent{{"2026-10-10", 900}},
				spent: 900,
			},
		},
		{
			name: "Closed in month: закрытая другим исполнителем не входит в Estimate of closed и closed",
			issues: []Issue{
				issue("K-1", "other", "2026-10-15T12:00:00.000+0500", 28800,
					wl("u", "2026-10-14T12:00:00.000+0500", 3600)),
				issue("K-2", "u", "2026-10-16T12:00:00.000+0500", 57600),
			},
			want: monthReport{
				rows: []reportRow{
					row("K-1", "", 28800, 3600),
					row("K-2", "2026-10-16", 57600, 0),
				},
				days:           []daySpent{{"2026-10-14", 3600}},
				estimateClosed: 57600,
				spent:          3600,
				closed:         1,
			},
		},
		{
			name: "Closed in month: момент закрытия по +05, граница окна",
			issues: []Issue{
				// 2026-09-30 19:30 UTC = 2026-10-01 00:30 +05 — в окне.
				issue("K-1", "u", "2026-09-30T19:30:00.000+0000", 3600),
				// 2026-10-31 19:30 UTC = 2026-11-01 00:30 +05 — вне окна.
				issue("K-2", "u", "2026-10-31T19:30:00.000+0000", 7200),
			},
			want: monthReport{
				rows:           []reportRow{row("K-1", "2026-10-01", 3600, 0)},
				estimateClosed: 3600,
				closed:         1,
			},
		},
		{
			name: "username без учёта регистра: исполнитель и автор worklog'а",
			issues: []Issue{issue("K-1", "U", "2026-10-20T12:00:00.000+0500", 3600,
				wl("U", "2026-10-20T10:00:00.000+0500", 1800))},
			want: monthReport{
				rows:           []reportRow{row("K-1", "2026-10-20", 3600, 1800)},
				days:           []daySpent{{"2026-10-20", 1800}},
				estimateClosed: 3600,
				spent:          1800,
				closed:         1,
			},
		},
		{
			name: "разбивка по дням: по локальной дате, суммируется по задачам, по возрастанию",
			issues: []Issue{
				issue("K-1", "", "", 0,
					// 2026-10-04 20:00 UTC = 2026-10-05 01:00 +05.
					wl("u", "2026-10-04T20:00:00.000+0000", 3600),
					wl("u", "2026-10-02T10:00:00.000+0500", 600)),
				issue("K-2", "", "", 0,
					wl("u", "2026-10-05T09:00:00.000+0500", 1800)),
			},
			want: monthReport{
				rows: []reportRow{
					row("K-1", "", 0, 4200),
					row("K-2", "", 0, 1800),
				},
				days:  []daySpent{{"2026-10-02", 600}, {"2026-10-05", 5400}},
				spent: 6000,
			},
		},
		{
			name: "Report candidate без Closed in month и без Spent in month выпадает",
			issues: []Issue{
				// Попал в JQL из-за расширения дат: закрыт 2026-11-01 по +05 и worklog 2026-09-30 по +05.
				issue("K-1", "u", "2026-10-31T20:00:00.000+0000", 3600,
					wl("u", "2026-09-30T23:00:00.000+0500", 600)),
				// Только чужой worklog, не закрыта.
				issue("K-2", "u", "", 0, wl("other", "2026-10-10T12:00:00.000+0500", 3600)),
				issue("K-3", "", "", 0, wl("u", "2026-10-10T12:00:00.000+0500", 60)),
			},
			want: monthReport{
				rows:  []reportRow{row("K-3", "", 0, 60)},
				days:  []daySpent{{"2026-10-10", 60}},
				spent: 60,
			},
		},
		{
			name:   "Closed in month: закрыта 1-го в 02:00 по +05 (по UTC — последний день прошлого месяца) входит",
			issues: []Issue{issue("K-1", "u", "2026-09-30T21:00:00.000+0000", 3600)},
			want: monthReport{
				rows:           []reportRow{row("K-1", "2026-10-01", 3600, 0)},
				estimateClosed: 3600,
				closed:         1,
			},
		},
		{
			name: "Unreadable date: предупреждение с ключами без повторов, отчёт строится",
			issues: []Issue{
				issue("K-1", "u", "", 0,
					wl("u", "вчера", 600),
					wl("u", "2026-10-10", 600),
					wl("u", "2026-10-10T12:00:00.000+0500", 900)),
				issue("K-2", "u", "15.10.2026", 3600),
				// Чужой worklog и чужое закрытие на отчёт не влияют — без предупреждения.
				issue("K-3", "other", "плохо", 0, wl("other", "плохо", 60)),
			},
			want: monthReport{
				rows:     []reportRow{row("K-1", "", 0, 900)},
				days:     []daySpent{{"2026-10-10", 900}},
				spent:    900,
				warnings: []string{"записей с неразборчивой датой: 3 (K-1, K-2)"},
			},
		},
		{
			name: "Unreadable date: ключи без повторов и при входе не по порядку",
			issues: []Issue{
				issue("K-2", "", "", 0, wl("u", "вчера", 60)),
				issue("K-1", "u", "плохо", 0),
				issue("K-2", "", "", 0, wl("u", "позавчера", 60)),
			},
			want: monthReport{warnings: []string{"записей с неразборчивой датой: 3 (K-2, K-1)"}},
		},
		{
			name: "пустой месяц: списки пустые, не nil",
			want: monthReport{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// nil в want означает пустой список: buildMonthReport всегда отдаёт non-nil (в JSON — массивы).
			if c.want.rows == nil {
				c.want.rows = []reportRow{}
			}
			if c.want.days == nil {
				c.want.days = []daySpent{}
			}
			if c.want.warnings == nil {
				c.want.warnings = []string{}
			}
			got := buildMonthReport(c.issues, "u", octoberWindow(t))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

// JQL расширен на ±1 день вокруг окна: точную границу по поясу проводит расчёт.
func TestReportJQLWidensWindowByOneDay(t *testing.T) {
	fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, `{"total":0,"issues":[]}`}}))
	if _, err := fj.run("report", "2026-10", "--user", "u", "--project", "P"); err != nil {
		t.Fatal(err)
	}
	want := `((assignee = "u" AND resolved >= "2026-09-30" AND resolved < "2026-11-02") OR ` +
		`(worklogAuthor = "u" AND worklogDate >= "2026-09-30" AND worklogDate < "2026-11-02")) ` +
		`AND project = "P" ORDER BY key`
	if got := searchJQL(t, fj); got != want {
		t.Fatalf("jql = %s\nwant  %s", got, want)
	}
}

func TestReportByAcceptsOnlyDay(t *testing.T) {
	for _, by := range []string{"issue", "week"} {
		fj := newFakeJira(t, routes{})
		_, err := fj.run("report", "2026-10", "--user", "u", "--by", by)
		if err == nil || err.Error() != "--by: только day" {
			t.Errorf("--by %s: err = %v", by, err)
		}
		if len(fj.requests) != 0 {
			t.Errorf("--by %s: лишние запросы %v", by, fj.requests)
		}
	}
}

func TestReportJSONWarningsAlwaysArray(t *testing.T) {
	for _, args := range [][]string{nil, {"--by", "day"}} {
		fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, `{"total":0,"issues":[]}`}}))
		out, err := fj.run("report", append([]string{"2026-10", "--user", "u", "--json"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"warnings":[]`) {
			t.Errorf("%v: нет warnings: [] в %s", args, out)
		}
	}
}

// Пустой месяц: в JSON списки — пустые массивы, не null.
func TestReportJSONEmptyMonthGivesEmptyArrays(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, `"issues":[]`},
		{[]string{"--by", "day"}, `"days":[]`},
	} {
		fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, `{"total":0,"issues":[]}`}}))
		out, err := fj.run("report", append([]string{"2026-10", "--user", "u", "--json"}, tc.args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v: нет %s в %s", tc.args, tc.want, out)
		}
	}
}

// K-1: один worklog u с Unreadable date и один в окне.
const searchUnreadable = `{"total":1,"issues":[
 {"key":"K-1","fields":{"summary":"S","status":{"name":"В работе"},"assignee":{"name":"u"},
  "worklog":{"total":2,"worklogs":[{"author":{"name":"u"},"started":"вчера","timeSpentSeconds":600},
   {"author":{"name":"u"},"started":"2026-10-10T12:00:00.000+0500","timeSpentSeconds":1800}]}}}]}`

func TestReportWarnsAboutUnreadableDates(t *testing.T) {
	const line = "# предупреждение: записей с неразборчивой датой: 1 (K-1)\n"
	const field = `"warnings":["записей с неразборчивой датой: 1 (K-1)"]`
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, line},
		{[]string{"--by", "day"}, line},
		{[]string{"--json"}, field},
		{[]string{"--by", "day", "--json"}, field},
	} {
		fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, searchUnreadable}}))
		out, err := fj.run("report", append([]string{"2026-10", "--user", "u"}, tc.args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%v: нет %q в\n%s", tc.args, tc.want, out)
		}
	}
}

// Ключ задачи с \n не должен разрывать строку предупреждения в тексте.
func TestReportWarningKeyStaysOnOneLine(t *testing.T) {
	search := strings.Replace(searchUnreadable, `"key":"K-1"`, `"key":"K\n-1"`, 1)
	const line = "# предупреждение: записей с неразборчивой датой: 1 (K -1)\n"
	for _, args := range [][]string{nil, {"--by", "day"}} {
		fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, search}}))
		out, err := fj.run("report", append([]string{"2026-10", "--user", "u"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, line) {
			t.Errorf("%v: нет %q в\n%s", args, line, out)
		}
	}
}

// withUserU добавляет к маршрутам фейка проверку пользователя "u" — для отчётов с --user u.
func withUserU(rs routes) routes {
	rs["GET /rest/api/2/user?username=u"] = reply{200, `{"name":"u"}`}
	return rs
}
