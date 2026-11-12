package main

import (
	"fmt"
	"strings"
	"testing"
)

// Форма запросов к Jira по командам: метод, путь с query и тело — байт в байт.
// Фиксирует REST-контракт, чтобы перенос операций в Client не менял ни одного запроса.

var shapeRoutes = routes{
	"GET /rest/api/2/myself":                 {200, `{"name":"me","displayName":"Me"}`},
	"GET /rest/api/2/user?username=ivan":     {200, `{"name":"ivan","displayName":"Иван"}`},
	"GET /rest/api/2/user?username=u":        {200, `{"name":"u"}`},
	"GET /rest/api/2/search":                 {200, `{"total":1,"issues":[{"key":"K-1","fields":{"summary":"s","status":{"name":"Готово"},"assignee":{"name":"u"},"resolutiondate":"2026-10-15T12:00:00.000+0000","timeoriginalestimate":3600,"worklog":{"total":2,"worklogs":[]}}}]}`},
	"GET /rest/api/2/issue/K-1/worklog":      {200, `{"worklogs":[{"author":{"name":"u"},"started":"2026-10-14T12:00:00.000+0000","timeSpentSeconds":1800}]}`},
	"GET /rest/api/2/issue/K-1":              {200, `{"key":"K-1","fields":{"summary":"s","status":{"name":"Открыта"},"issuetype":{"name":"Задача"},"comment":{"total":0,"comments":[]}}}`},
	"GET /rest/api/2/issue/K-1/transitions":  {200, `{"transitions":[{"id":"21","name":"В работе","to":{"name":"В работе"}}]}`},
	"POST /rest/api/2/issue/K-1/transitions": {204, ""},
	"POST /rest/api/2/issue/K-1/comment":     {201, `{}`},
	"POST /rest/api/2/issue/K-1/worklog":     {201, `{}`},
	"PUT /rest/api/2/issue/K-1":              {204, ""},
	"PUT /rest/api/2/issue/K-1/assignee":     {204, ""},
	"POST /rest/api/2/issue":                 {201, `{"key":"K-2"}`},
	"PUT /rest/api/2/issue/K-2":              {204, ""},
}

func TestRequestShapes(t *testing.T) {
	reportInUTC(t) // started у log и окно report — в UTC, независимо от машины
	for _, tc := range []struct {
		args  []string
		want  string // по строке на запрос: "МЕТОД путь тело"
		extra routes // поверх shapeRoutes
	}{
		{[]string{"me"},
			"GET /rest/api/2/myself \n", nil},
		{[]string{"ls"},
			"GET /rest/api/2/search?fields=summary%2Cstatus%2Ctimetracking&jql=assignee+%3D+currentUser%28%29+AND+statusCategory+%21%3D+Done+ORDER+BY+updated+DESC&maxResults=30&startAt=0 \n", nil},
		{[]string{"ls", "--user", "ivan", "--all", "--project", "P", "-n", "5"},
			"GET /rest/api/2/user?username=ivan \nGET /rest/api/2/search?fields=summary%2Cstatus%2Ctimetracking&jql=assignee+%3D+%22ivan%22+AND+project+%3D+%22P%22+ORDER+BY+updated+DESC&maxResults=5&startAt=0 \n", nil},
		{[]string{"q", "project = K", "-n", "3"},
			"GET /rest/api/2/search?fields=summary%2Cstatus%2Ctimetracking&jql=project+%3D+K&maxResults=3&startAt=0 \n", nil},
		{[]string{"show", "K-1"},
			"GET /rest/api/2/issue/K-1?fields=summary,description,status,issuetype,priority,project,assignee,reporter,created,updated,resolutiondate,timetracking,parent,comment \n", nil},
		{[]string{"show", "K-1", "--comments", "2"},
			"GET /rest/api/2/issue/K-1?fields=summary,description,status,issuetype,priority,project,assignee,reporter,created,updated,resolutiondate,timetracking,parent,comment \n", nil},
		{[]string{"create", "--project", "K", "--summary", "S", "--desc", "D", "--estimate", "1h", "--yes"},
			"GET /rest/api/2/myself \nPOST /rest/api/2/issue {\"fields\":{\"assignee\":{\"name\":\"me\"},\"description\":\"D\",\"issuetype\":{\"name\":\"Task\"},\"project\":{\"key\":\"K\"},\"summary\":\"S\"}}\nPUT /rest/api/2/issue/K-2 {\"fields\":{\"timetracking\":{\"originalEstimate\":\"1h\"}}}\n", nil},
		{[]string{"create", "--project", "K", "--summary", "S", "--assignee", "none"}, "", nil},
		{[]string{"create", "--project", "K", "--summary", "S", "--assignee", "none", "--yes"},
			"POST /rest/api/2/issue {\"fields\":{\"issuetype\":{\"name\":\"Task\"},\"project\":{\"key\":\"K\"},\"summary\":\"S\"}}\n", nil},
		{[]string{"assign", "K-1", "ivan"},
			"GET /rest/api/2/user?username=ivan \nGET /rest/api/2/issue/K-1?fields=assignee,summary \n", nil},
		{[]string{"assign", "K-1", "ivan", "--yes"},
			"GET /rest/api/2/user?username=ivan \nPUT /rest/api/2/issue/K-1/assignee {\"name\":\"ivan\"}\n", nil},
		{[]string{"assign", "K-1", "none", "--yes"},
			"PUT /rest/api/2/issue/K-1/assignee {\"name\":null}\n", nil},
		{[]string{"move", "K-1"},
			"GET /rest/api/2/issue/K-1/transitions \n", nil},
		{[]string{"move", "K-1", "в работе"},
			"GET /rest/api/2/issue/K-1/transitions \nPOST /rest/api/2/issue/K-1/transitions {\"transition\":{\"id\":\"21\"}}\n", nil},
		{[]string{"comment", "K-1", "текст"},
			"POST /rest/api/2/issue/K-1/comment {\"body\":\"текст\"}\n", nil},
		{[]string{"log", "K-1", "2h", "сделал", "--date", "2026-11-10"},
			"POST /rest/api/2/issue/K-1/worklog {\"comment\":\"сделал\",\"started\":\"2026-11-10T09:00:00.000+0000\",\"timeSpent\":\"2h\"}\n", nil},
		{[]string{"log", "K-1", "30m", "--date", "2026-11-10"},
			"POST /rest/api/2/issue/K-1/worklog {\"started\":\"2026-11-10T09:00:00.000+0000\",\"timeSpent\":\"30m\"}\n", nil},
		{[]string{"estimate", "K-1", "1h"}, // поле timetracking не на экране → оценка через доску
			"PUT /rest/api/2/issue/K-1 {\"fields\":{\"timetracking\":{\"originalEstimate\":\"1h\"}}}\n" +
				"GET /rest/agile/1.0/board?projectKeyOrId=K \n" +
				"GET /rest/agile/1.0/board/7/configuration \n" +
				"PUT /rest/agile/1.0/issue/K-1/estimation?boardId=7 {\"value\":\"1h\"}\n", routes{
				"PUT /rest/api/2/issue/K-1":                          {400, `{"errors":{"timetracking":"Field 'timetracking' cannot be set."}}`},
				"GET /rest/agile/1.0/board":                          {200, `{"values":[{"id":7}]}`},
				"GET /rest/agile/1.0/board/7/configuration":          {200, `{"estimation":{"field":{"fieldId":"timeoriginalestimate"}}}`},
				"PUT /rest/agile/1.0/issue/K-1/estimation?boardId=7": {200, `{}`},
			}},
		{[]string{"estimate", "K-1", "4h"},
			"PUT /rest/api/2/issue/K-1 {\"fields\":{\"timetracking\":{\"originalEstimate\":\"4h\"}}}\n", nil},
		{[]string{"report", "2026-10", "--user", "u"},
			"GET /rest/api/2/user?username=u \nGET /rest/api/2/search?fields=summary%2Cstatus%2Cassignee%2Cresolutiondate%2Ctimeoriginalestimate%2Cworklog&jql=%28%28assignee+%3D+%22u%22+AND+resolved+%3E%3D+%222026-09-30%22+AND+resolved+%3C+%222026-11-02%22%29+OR+%28worklogAuthor+%3D+%22u%22+AND+worklogDate+%3E%3D+%222026-09-30%22+AND+worklogDate+%3C+%222026-11-02%22%29%29+ORDER+BY+key&maxResults=100&startAt=0 \nGET /rest/api/2/issue/K-1/worklog \n", nil},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			rs := routes{}
			for k, v := range shapeRoutes {
				rs[k] = v
			}
			for k, v := range tc.extra {
				rs[k] = v
			}
			fj := newFakeJira(t, rs)
			if _, err := fj.run(tc.args[0], tc.args[1:]...); err != nil {
				t.Fatalf("%v", err)
			}
			var got strings.Builder
			for _, r := range fj.requests {
				fmt.Fprintf(&got, "%s %s %s\n", r.Method, r.Path, r.Body)
			}
			if got.String() != tc.want {
				t.Errorf("\n got: %q\nwant: %q", got.String(), tc.want)
			}
		})
	}
}
