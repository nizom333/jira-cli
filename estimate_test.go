package main

import (
	"strings"
	"testing"
)

// Установка Original Estimate: edit задачи, при отказе по полю timetracking — через Agile-доску.

const timetrackingRejected = `{"errorMessages":[],"errors":{"timetracking":"Field 'timetracking' cannot be set. It is not on the appropriate screen, or unknown."}}`

func paths(rs []request) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

func TestEstimateSetsOriginalEstimateByEditingIssue(t *testing.T) {
	fj := newFakeJira(t, routes{
		"PUT /rest/api/2/issue/K-1": {204, ""},
	})
	out, err := fj.run("estimate", "K-1", "4h")
	if err != nil {
		t.Fatal(err)
	}
	if out != "K-1: оценка 4h\n" {
		t.Fatalf("out = %q", out)
	}
	if len(fj.requests) != 1 {
		t.Fatalf("requests = %v", paths(fj.requests))
	}
	if got, want := fj.requests[0].Body, `{"fields":{"timetracking":{"originalEstimate":"4h"}}}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestEstimateFallsBackToBoardWhenTimetrackingNotOnScreen(t *testing.T) {
	fj := newFakeJira(t, routes{
		"PUT /rest/api/2/issue/K-1":                          {400, timetrackingRejected},
		"GET /rest/agile/1.0/board?projectKeyOrId=K":         {200, `{"values":[{"id":7}]}`},
		"GET /rest/agile/1.0/board/7/configuration":          {200, `{"estimation":{"field":{"fieldId":"timeoriginalestimate"}}}`},
		"PUT /rest/agile/1.0/issue/K-1/estimation?boardId=7": {200, `{"fieldId":"timeoriginalestimate","value":"4h"}`},
	})
	out, err := fj.run("estimate", "K-1", "4h")
	if err != nil {
		t.Fatal(err)
	}
	if out != "K-1: оценка 4h\n" {
		t.Fatalf("out = %q", out)
	}
	want := []string{
		"PUT /rest/api/2/issue/K-1",
		"GET /rest/agile/1.0/board?projectKeyOrId=K",
		"GET /rest/agile/1.0/board/7/configuration",
		"PUT /rest/agile/1.0/issue/K-1/estimation?boardId=7",
	}
	if got := paths(fj.requests); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	if got := fj.requests[3].Body; got != `{"value":"4h"}` {
		t.Fatalf("estimation body = %s", got)
	}
}

func TestEstimateSkipsBoardsWithOtherEstimationOrBrokenConfig(t *testing.T) {
	fj := newFakeJira(t, routes{
		"PUT /rest/api/2/issue/K-1":                          {400, timetrackingRejected},
		"GET /rest/agile/1.0/board?projectKeyOrId=K":         {200, `{"values":[{"id":1},{"id":2},{"id":3}]}`},
		"GET /rest/agile/1.0/board/1/configuration":          {500, `{"errorMessages":["boom"]}`},
		"GET /rest/agile/1.0/board/2/configuration":          {200, `{"estimation":{"field":{"fieldId":"customfield_10002"}}}`},
		"GET /rest/agile/1.0/board/3/configuration":          {200, `{"estimation":{"field":{"fieldId":"timeoriginalestimate"}}}`},
		"PUT /rest/agile/1.0/issue/K-1/estimation?boardId=3": {200, `{}`},
	})
	out, err := fj.run("estimate", "K-1", "4h")
	if err != nil {
		t.Fatal(err)
	}
	if out != "K-1: оценка 4h\n" {
		t.Fatalf("out = %q", out)
	}
	if last := fj.requests[len(fj.requests)-1]; last.Method+" "+last.Path != "PUT /rest/agile/1.0/issue/K-1/estimation?boardId=3" {
		t.Fatalf("requests = %v", paths(fj.requests))
	}
}

func TestEstimateWithoutSuitableBoardReportsBothReasons(t *testing.T) {
	fj := newFakeJira(t, routes{
		"PUT /rest/api/2/issue/K-1":                  {400, timetrackingRejected},
		"GET /rest/agile/1.0/board?projectKeyOrId=K": {200, `{"values":[{"id":2}]}`},
		"GET /rest/agile/1.0/board/2/configuration":  {200, `{"estimation":{"field":{"fieldId":"customfield_10002"}}}`},
	})
	out, err := fj.run("estimate", "K-1", "4h")
	if err == nil {
		t.Fatalf("ожидалась ошибка, out = %q", out)
	}
	msg := err.Error()
	for _, want := range []string{
		"HTTP 400: timetracking: Field 'timetracking' cannot be set",
		"у проекта K нет доски с оценкой по Original Estimate",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("err = %q, нет %q", msg, want)
		}
	}
	if out != "" {
		t.Fatalf("out = %q", out)
	}
}

func TestEstimateOtherEditErrorsDoNotTouchAgileAPI(t *testing.T) {
	cases := map[string]struct {
		rep  reply
		want string
	}{
		"400 по другому полю": {
			reply{400, `{"errors":{"summary":"timetracking тут ни при чём"}}`},
			"HTTP 400: summary: timetracking тут ни при чём",
		},
		"403": {
			reply{403, `{"errorMessages":["You do not have permission to edit timetracking"]}`},
			"HTTP 403: You do not have permission to edit timetracking",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fj := newFakeJira(t, routes{"PUT /rest/api/2/issue/K-1": tc.rep})
			_, err := fj.run("estimate", "K-1", "4h")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(fj.requests) != 1 {
				t.Fatalf("requests = %v", paths(fj.requests))
			}
		})
	}
}

func TestCreateWithEstimateUsesBoardFallback(t *testing.T) {
	fj := newFakeJira(t, routes{
		"POST /rest/api/2/issue":                             {201, `{"key":"K-2"}`},
		"PUT /rest/api/2/issue/K-2":                          {400, timetrackingRejected},
		"GET /rest/agile/1.0/board?projectKeyOrId=K":         {200, `{"values":[{"id":7}]}`},
		"GET /rest/agile/1.0/board/7/configuration":          {200, `{"estimation":{"field":{"fieldId":"timeoriginalestimate"}}}`},
		"PUT /rest/agile/1.0/issue/K-2/estimation?boardId=7": {200, `{}`},
	})
	out, err := fj.run("create", "--project", "K", "--summary", "S", "--assignee", "none", "--estimate", "4h", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "K-2: оценка 4h\n") {
		t.Fatalf("out = %q", out)
	}
	if len(fj.requests) != 5 {
		t.Fatalf("requests = %v", paths(fj.requests))
	}
}

// Частичный успех: задача создана, оценка — нет. Вывод «created …» не теряется, и команда падает.
func TestCreateKeepsCreatedLineWhenEstimateFails(t *testing.T) {
	fj := newFakeJira(t, routes{
		"POST /rest/api/2/issue":    {201, `{"key":"K-2"}`},
		"PUT /rest/api/2/issue/K-2": {403, `{"errorMessages":["нет прав"]}`},
	})
	out, err := fj.run("create", "--project", "K", "--summary", "S", "--assignee", "none", "--estimate", "4h", "--yes")
	if want := "created K-2 " + fj.srv.URL + "/browse/K-2\n"; out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	if err == nil || err.Error() != "задача создана, но оценка не поставлена: HTTP 403: нет прав" {
		t.Fatalf("err = %v", err)
	}
}
