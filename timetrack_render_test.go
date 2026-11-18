package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Задача с оценкой 2d (57600 с), списано 2h30m, остаток 13h30m; вторая — без учёта времени.
const searchTimetracking = `{"total":2,"issues":[
 {"key":"K-1","fields":{"summary":"с оценкой","status":{"name":"Открыта"},
  "timetracking":{"originalEstimate":"2d","timeSpent":"2h 30m","remainingEstimate":"1d 5h 30m",
   "originalEstimateSeconds":57600,"timeSpentSeconds":9000,"remainingEstimateSeconds":48600}}},
 {"key":"K-2","fields":{"summary":"пустая","status":{"name":"Открыта"},"timetracking":{}}}]}`

func TestLsRendersDurationsAsHoursMinutes(t *testing.T) {
	fj := newFakeJira(t, routes{"GET /rest/api/2/search": {200, searchTimetracking}})
	want := "K-1\tОткрыта\t16h\t2h30m\tс оценкой\nK-2\tОткрыта\t-\t-\tпустая\n"
	for _, args := range [][]string{{"ls"}, {"q", "project = K"}} {
		out, err := fj.run(args[0], args[1:]...)
		if err != nil {
			t.Fatal(err)
		}
		if out != want {
			t.Fatalf("%s: out = %q, want %q", args[0], out, want)
		}
	}
}

func TestLsJSONGivesHoursAsNumbers(t *testing.T) {
	fj := newFakeJira(t, routes{"GET /rest/api/2/search": {200, searchTimetracking}})
	out, err := fj.run("ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"issues":[` +
		`{"key":"K-1","status":"Открыта","estimate_h":16,"spent_h":2.5,"summary":"с оценкой"},` +
		`{"key":"K-2","status":"Открыта","estimate_h":0,"spent_h":0,"summary":"пустая"}],"total":2}` + "\n"
	if out != want {
		t.Fatalf("out = %s\nwant  %s", out, want)
	}
}

// Отчёт за 2026-10: K-1 закрыта u с оценкой 2d (57600 с) и списанием u 2h30m; K-2 не закрыта,
// списание только чужое — в отчёт не входит; K-3 закрыта u без оценки и без списания.
const searchReport = `{"total":3,"issues":[
 {"key":"K-1","fields":{"summary":"с оценкой","status":{"name":"Выполнена"},"assignee":{"name":"u"},
  "resolutiondate":"2026-10-15T12:00:00.000+0500","timeoriginalestimate":57600,
  "worklog":{"total":1,"worklogs":[{"author":{"name":"u"},"started":"2026-10-10T12:00:00.000+0500","timeSpentSeconds":9000}]}}},
 {"key":"K-2","fields":{"summary":"чужое","status":{"name":"В работе"},"assignee":{"name":"u"},
  "worklog":{"total":1,"worklogs":[{"author":{"name":"other"},"started":"2026-10-10T12:00:00.000+0500","timeSpentSeconds":3600}]}}},
 {"key":"K-3","fields":{"summary":"пустая","status":{"name":"Выполнена"},"assignee":{"name":"u"},
  "resolutiondate":"2026-10-20T12:00:00.000+0500","worklog":{"total":0,"worklogs":[]}}}]}`

func TestReportRendersDurationsAsHoursMinutes(t *testing.T) {
	inLocal(t, zone05) // даты в searchReport — по +05
	fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, searchReport}}))
	out, err := fj.run("report", "2026-10", "--user", "u")
	if err != nil {
		t.Fatal(err)
	}
	want := "# 2026-10 · u · KEY ОЦЕНКА СПИСАНО ЗАКРЫТА STATUS SUMMARY\n" +
		"K-1\t16h\t2h30m\t2026-10-15\tВыполнена\tс оценкой\n" +
		"K-3\t-\t-\t2026-10-20\tВыполнена\tпустая\n" +
		"# итого: оценка закрытых 16h · списано 2h30m · задач 2, закрыто 2\n"
	if out != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
}

func TestReportJSONGivesHoursAsNumbers(t *testing.T) {
	inLocal(t, zone05) // даты в searchReport — по +05
	fj := newFakeJira(t, withUserU(routes{"GET /rest/api/2/search": {200, searchReport}}))
	out, err := fj.run("report", "2026-10", "--user", "u", "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"closed_count":2,"issues":[` +
		`{"key":"K-1","status":"Выполнена","estimate_h":16,"spent_h":2.5,"resolved":"2026-10-15","summary":"с оценкой"},` +
		`{"key":"K-3","status":"Выполнена","estimate_h":0,"spent_h":0,"resolved":"2026-10-20","summary":"пустая"}],` +
		`"issues_count":2,"month":"2026-10","total_estimate_closed_h":16,"total_spent_h":2.5,"user":"u","warnings":[]}` + "\n"
	if out != want {
		t.Fatalf("out = %s\nwant  %s", out, want)
	}
}

const showIssueEstimated = `{"key":"K-1","fields":{"summary":"с оценкой","status":{"name":"Открыта"},
 "issuetype":{"name":"Задача"},"created":"2026-11-01T10:00:00.000+0500","updated":"2026-11-02T10:00:00.000+0500",
 "timetracking":{"originalEstimate":"2d","timeSpent":"2h 30m","remainingEstimate":"1d 5h 30m",
  "originalEstimateSeconds":57600,"timeSpentSeconds":9000,"remainingEstimateSeconds":48600}}}`

const showIssueEmpty = `{"key":"K-2","fields":{"summary":"пустая","status":{"name":"Открыта"},
 "issuetype":{"name":"Задача"},"created":"2026-11-01T10:00:00.000+0500","updated":"2026-11-02T10:00:00.000+0500",
 "timetracking":{}}}`

func TestShowRendersDurationsAsHoursMinutes(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/issue/K-1": {200, showIssueEstimated},
		"GET /rest/api/2/issue/K-2": {200, showIssueEmpty},
	})
	for key, want := range map[string]string{
		"K-1": "estimate: 16h  spent: 2h30m  remaining: 13h30m\n",
		"K-2": "estimate: -  spent: -  remaining: -\n",
	} {
		out, err := fj.run("show", key)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("show %s: нет строки %q в\n%s", key, want, out)
		}
	}
}

func TestShowJSONGivesHoursAsNumbers(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/api/2/issue/K-1": {200, showIssueEstimated},
		"GET /rest/api/2/issue/K-2": {200, showIssueEmpty},
	})
	for key, want := range map[string][3]float64{"K-1": {16, 2.5, 13.5}, "K-2": {0, 0, 0}} {
		out, err := fj.run("show", key, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("show %s --json: %v\n%s", key, err, out)
		}
		for i, k := range []string{"estimate_h", "spent_h", "remaining_h"} {
			if v, ok := got[k].(float64); !ok || v != want[i] {
				t.Errorf("show %s --json: %s = %v, want %v", key, k, got[k], want[i])
			}
		}
		for _, k := range []string{"estimate", "spent", "remaining"} {
			if _, ok := got[k]; ok {
				t.Errorf("show %s --json: строковое поле %q больше не выводится", key, k)
			}
		}
	}
}
