package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Модель jira vi тестируется без терминала: клавиши идут в update, cmd выполняется сразу,
// его результат возвращается в модель (press). Запросы — в фейк Jira, как у остальных команд.

func newViTest(t *testing.T, rs routes, s viScope, issues ...viIssue) (*viModel, *fakeJira) {
	t.Helper()
	fj := newFakeJira(t, rs)
	c := newClientWithToken(strings.TrimRight(fj.srv.URL, "/"), fakeToken)
	if s.n == 0 {
		s.n = viDefaultLimit
	}
	m := newViModel(c, s, issues, len(issues))
	m.open = func(string) error { return nil }
	m.preview = false // панель справа грузит подробности — её тесты включают её сами
	m.previewDelay = 0
	m.update(resizeMsg{120, 30})
	return m, fj
}

// settle выполняет cmd и все следующие за ней (batch — каждую), как цикл runProgram.
func settle(m *viModel, c cmd) {
	queue := []cmd{c}
	for len(queue) > 0 {
		c, queue = queue[0], queue[1:]
		if c == nil {
			continue
		}
		r := c()
		if b, ok := r.(batchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		queue = append(queue, m.update(r))
	}
}

func press(m *viModel, keys ...key) {
	for _, k := range keys {
		settle(m, m.update(k))
	}
}

func ch(r rune) key { return key{r: r} }

func typeText(m *viModel, s string) {
	for _, r := range s {
		press(m, ch(r))
	}
}

func viIss(k, status, statusID, cat string) viIssue {
	return viIssue{Key: k, Summary: "задача " + k, Project: "K", Status: status, StatusID: statusID, Category: cat}
}

var (
	isOpen  = viIss("K-1", "Открыта", "1", categoryNew)
	isWork  = viIss("K-2", "В работе", "3", categoryInProgress)
	isDone  = viIss("K-3", "Готово", "10001", categoryDone)
	isOpen2 = viIss("K-4", "Открыта", "1", categoryNew)
)

// writes — запросы, меняющие данные (всё, кроме GET).
func writes(fj *fakeJira) []string {
	var out []string
	for _, r := range fj.requests {
		if r.Method != "GET" {
			out = append(out, r.Method+" "+r.Path+" "+r.Body)
		}
	}
	return out
}

const k1Transitions = `{"transitions":[
	{"id":"11","name":"В работу","to":{"name":"В работе","statusCategory":{"key":"indeterminate"}}},
	{"id":"31","name":"Выполнить","to":{"name":"Готово","statusCategory":{"key":"done"}}}]}`

func issueJSON(k, status, statusID, cat string) string {
	return fmt.Sprintf(`{"key":%q,"fields":{"summary":"задача %s","project":{"key":"K"},
		"status":{"id":%q,"name":%q,"statusCategory":{"key":%q}},
		"timetracking":{"originalEstimateSeconds":28800}}}`, k, k, statusID, status, cat)
}

func TestViScopeJQL(t *testing.T) {
	open := "(statusCategory != Done OR updated >= -14d)"
	sprints := []Sprint{{ID: 5}, {ID: 7}}
	for _, tc := range []struct {
		s    viScope
		want string
	}{
		{viScope{}, "assignee = currentUser() AND " + open + " ORDER BY updated DESC"},
		{viScope{project: "K"}, `assignee = currentUser() AND project = "K" AND ` + open + " ORDER BY updated DESC"},
		{viScope{project: "K", team: true}, `project = "K" AND ` + open + " ORDER BY updated DESC"},
		{viScope{user: "ivan"}, `assignee = "ivan" AND ` + open + " ORDER BY updated DESC"},
		{viScope{sprints: sprints}, "sprint in (5, 7) AND assignee = currentUser() ORDER BY Rank ASC"},
		{viScope{sprints: sprints[:1], project: "K", team: true}, `sprint in (5) AND project = "K" ORDER BY Rank ASC`},
	} {
		if got := tc.s.jql(); got != tc.want {
			t.Errorf("%+v:\n got %s\nwant %s", tc.s, got, tc.want)
		}
	}
}

func TestViNeedsTerminalBeforeJira(t *testing.T) {
	prev := viTerminal
	viTerminal = func() bool { return false }
	defer func() { viTerminal = prev }()
	fj := newFakeJira(t, routes{})
	out, err := fj.run("vi", "--project", "K")
	if err == nil || !strings.Contains(err.Error(), "нужен терминал") {
		t.Fatalf("err = %v", err)
	}
	if out != "" || len(fj.requests) != 0 {
		t.Fatalf("out = %q, requests = %+v", out, fj.requests)
	}
}

const viBoardRoutes = `{"isLast":true,"values":[{"id":1,"name":"Доска"}]}`

func TestViResolveSprintNow(t *testing.T) {
	fj := newFakeJira(t, routes{
		"GET /rest/agile/1.0/board": {200, viBoardRoutes},
		"GET /rest/agile/1.0/board/1/sprint": {200, `{"isLast":true,"values":[
			{"id":5,"name":"С5","state":"active","startDate":"2026-11-15T12:00:00.000Z","endDate":"2026-11-25T12:00:00.000Z"},
			{"id":4,"name":"С4","state":"active","startDate":"2026-10-01T12:00:00.000Z","endDate":"2026-10-14T12:00:00.000Z"}]}`},
		"GET /rest/api/2/user": {200, `{"name":"ivan.p","displayName":"Иван"}`},
	})
	c := newClientWithToken(fj.srv.URL, fakeToken)
	s, _, err := resolveViScope(c, viOptions{user: "ivan.p", sprint: &sprintTarget{kind: targetCurrent}, n: 100}, "2026-11-20")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.sprints) != 1 || s.sprints[0].ID != 5 || s.sprintLabel != "спринт «С5»" || s.user != "ivan.p" {
		t.Fatalf("scope = %+v", s)
	}
	if want := `sprint in (5) AND assignee = "ivan.p" ORDER BY Rank ASC`; s.jql() != want {
		t.Fatalf("jql = %s", s.jql())
	}
	if _, _, err := resolveViScope(c, viOptions{sprint: &sprintTarget{kind: targetCurrent}}, "2026-12-01"); err == nil ||
		!strings.Contains(err.Error(), "нет текущего спринта") {
		t.Fatalf("без текущего спринта: err = %v", err)
	}
	s, _, err = resolveViScope(c, viOptions{sprint: &sprintTarget{kind: targetActive}}, "2026-12-01")
	if err != nil || len(s.sprints) != 2 {
		t.Fatalf("active: %+v, %v", s.sprints, err)
	}
}

func TestViColumnsByCategoryThenStatusID(t *testing.T) {
	review := viIss("K-5", "Ревью", "10000", categoryInProgress)
	backlog := viIss("K-6", "Бэклог", "10002", categoryNew)
	cols := viColumns([]viIssue{isDone, review, isWork, isOpen, backlog, isOpen2})
	var got []string
	for _, c := range cols {
		keys := []string{}
		for _, is := range c.issues {
			keys = append(keys, is.Key)
		}
		got = append(got, c.status+":"+strings.Join(keys, ","))
	}
	want := []string{"Открыта:K-1,K-4", "Бэклог:K-6", "В работе:K-2", "Ревью:K-5", "Готово:K-3"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestViDoneAsksAndTransitions(t *testing.T) {
	rs := routes{
		"GET /rest/api/2/issue/K-1/transitions":  {200, k1Transitions},
		"POST /rest/api/2/issue/K-1/transitions": {204, ""},
		"GET /rest/api/2/issue/K-1":              {200, issueJSON("K-1", "Готово", "10001", "done")},
	}
	m, fj := newViTest(t, rs, viScope{}, isOpen, isWork)
	press(m, ch('d'))
	if m.confirm == nil || !strings.Contains(m.confirm.text, "Закрыть K-1 → Готово?") {
		t.Fatalf("confirm = %+v", m.confirm)
	}
	press(m, key{name: keyEnter}) // Enter не подтверждает закрытие
	if len(writes(fj)) != 0 || m.confirm == nil {
		t.Fatalf("Enter закрыл задачу: %v", writes(fj))
	}
	press(m, ch('n'))
	if m.confirm != nil || len(writes(fj)) != 0 {
		t.Fatalf("n: confirm = %+v, writes = %v", m.confirm, writes(fj))
	}

	press(m, ch('в'), ch('н')) // d и y в русской раскладке
	if w := writes(fj); len(w) != 1 || w[0] != `POST /rest/api/2/issue/K-1/transitions {"transition":{"id":"31"}}` {
		t.Fatalf("writes = %v", w)
	}
	if is := m.issues[0]; is.Status != "Готово" || !is.done() {
		t.Fatalf("issue = %+v", is)
	}
	if m.status != "K-1 → Готово" || m.isErr || m.busy != "" {
		t.Fatalf("status = %q (err %v, busy %q)", m.status, m.isErr, m.busy)
	}
	// закрытая уходит вниз списка, выбор остаётся на той же строке — на следующей задаче
	if v := m.visible(); v[0].Key != "K-2" || v[1].Key != "K-1" || m.sel != "K-2" {
		t.Fatalf("visible = %v, sel = %s", v, m.sel)
	}
}

// Задача, выпавшая из поиска после действия, не остаётся выбранной: следующее действие ушло бы на неё.
func TestViUpdatedIssueLeavingFilterIsDeselected(t *testing.T) {
	m, _ := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1/transitions":  {200, k1Transitions},
		"POST /rest/api/2/issue/K-1/transitions": {204, ""},
		"GET /rest/api/2/issue/K-1":              {200, issueJSON("K-1", "В работе", "3", "indeterminate")},
	}, viScope{}, isOpen, isOpen2)
	m.filter = "открыта"
	press(m, ch('m'), key{name: keyEnter}) // K-1 → В работе
	if m.sel != "K-4" {
		t.Fatalf("sel = %s", m.sel)
	}
}

func TestViDoneChoosesAmongSeveral(t *testing.T) {
	rs := routes{
		"GET /rest/api/2/issue/K-1/transitions": {200, `{"transitions":[
			{"id":"31","name":"Выполнить","to":{"name":"Готово","statusCategory":{"key":"done"}}},
			{"id":"41","name":"Отклонить","to":{"name":"Отклонена","statusCategory":{"key":"done"}}},
			{"id":"11","name":"В работу","to":{"name":"В работе","statusCategory":{"key":"indeterminate"}}}]}`},
		"POST /rest/api/2/issue/K-1/transitions": {204, ""},
		"GET /rest/api/2/issue/K-1":              {200, issueJSON("K-1", "Отклонена", "10003", "done")},
	}
	m, fj := newViTest(t, rs, viScope{}, isOpen)
	press(m, ch('d'))
	if m.picker == nil || len(m.picker.items) != 2 {
		t.Fatalf("picker = %+v", m.picker)
	}
	press(m, ch('2'))
	if w := writes(fj); len(w) != 1 || !strings.Contains(w[0], `"id":"41"`) {
		t.Fatalf("writes = %v", w)
	}
}

func TestViDoneWithoutDoneTransition(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1/transitions": {200, `{"transitions":[{"id":"11","name":"В работу","to":{"name":"В работе","statusCategory":{"key":"indeterminate"}}}]}`},
	}, viScope{}, isOpen)
	press(m, ch('d'))
	if m.confirm != nil || m.picker != nil || !m.isErr || !strings.Contains(m.status, "нет перехода в закрытый статус") {
		t.Fatalf("status = %q, confirm = %v, picker = %v", m.status, m.confirm, m.picker)
	}
	if len(writes(fj)) != 0 {
		t.Fatal(writes(fj))
	}
}

func TestViMovePicksTransition(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1/transitions":  {200, k1Transitions},
		"POST /rest/api/2/issue/K-1/transitions": {204, ""},
		"GET /rest/api/2/issue/K-1":              {200, issueJSON("K-1", "В работе", "3", "indeterminate")},
	}, viScope{}, isOpen)
	press(m, ch('m'))
	if m.picker == nil || m.picker.items[0].label != "В работу → В работе" {
		t.Fatalf("picker = %+v", m.picker)
	}
	press(m, ch('j'), ch('k'), key{name: keyEnter}) // вниз, обратно вверх, выбрать первый
	if w := writes(fj); len(w) != 1 || !strings.Contains(w[0], `"id":"11"`) {
		t.Fatalf("writes = %v", w)
	}
	if m.issues[0].Status != "В работе" {
		t.Fatalf("issue = %+v", m.issues[0])
	}
}

func TestViBoardShiftCardToNextColumn(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1/transitions":  {200, k1Transitions},
		"POST /rest/api/2/issue/K-1/transitions": {204, ""},
		"GET /rest/api/2/issue/K-1":              {200, issueJSON("K-1", "В работе", "3", "indeterminate")},
	}, viScope{}, isOpen, isWork, isDone)
	press(m, ch('L')) // в списке не переносит
	if len(fj.requests) != 0 {
		t.Fatalf("requests в списке: %+v", fj.requests)
	}
	press(m, ch('v'), ch('L'))
	if w := writes(fj); len(w) != 1 || !strings.Contains(w[0], `"id":"11"`) {
		t.Fatalf("writes = %v", w)
	}
	cols := viColumns(m.visible())
	if ci, _ := m.boardPos(cols); cols[ci].status != "В работе" || m.sel != "K-1" {
		t.Fatalf("выбор после переноса: колонка %s, sel %s", cols[ci].status, m.sel)
	}
	// дальше вправо — «Готово»: закрытие только с подтверждением
	fj.routes["GET /rest/api/2/issue/K-1/transitions"] = reply{200, k1Transitions}
	press(m, key{name: keyShiftRight})
	if m.confirm == nil || len(writes(fj)) != 1 {
		t.Fatalf("confirm = %+v, writes = %v", m.confirm, writes(fj))
	}
}

func TestViBoardNavigation(t *testing.T) {
	m, _ := newViTest(t, routes{}, viScope{}, isOpen, isWork, isDone, isOpen2)
	press(m, ch('v'))
	if m.sel != "K-1" {
		t.Fatalf("sel = %s", m.sel)
	}
	press(m, ch('j')) // вниз по колонке «Открыта»
	if m.sel != "K-4" {
		t.Fatalf("j: sel = %s", m.sel)
	}
	press(m, ch('l')) // в «В работе» — одна карточка
	if m.sel != "K-2" {
		t.Fatalf("l: sel = %s", m.sel)
	}
	press(m, ch('l'), ch('l')) // упирается в последнюю колонку
	if m.sel != "K-3" {
		t.Fatalf("ll: sel = %s", m.sel)
	}
	press(m, ch('р'), ch('р'), ch('G')) // h h в русской раскладке, G — в конец колонки
	if m.sel != "K-4" {
		t.Fatalf("hhG: sel = %s", m.sel)
	}
}

func TestViEstimate(t *testing.T) {
	m, fj := newViTest(t, routes{
		"PUT /rest/api/2/issue/K-1": {204, ""},
		"GET /rest/api/2/issue/K-1": {200, issueJSON("K-1", "Открыта", "1", "new")},
	}, viScope{}, viIssue{Key: "K-1", Status: "Открыта", Category: categoryNew, Estimate: 7200})
	press(m, ch('e'))
	if m.input == nil || string(m.input.buf) != "2h" {
		t.Fatalf("input = %+v", m.input)
	}
	press(m, key{name: keyCtrlU})
	typeText(m, "абв")
	press(m, key{name: keyEnter})
	if m.input == nil || !m.isErr || len(fj.requests) != 0 {
		t.Fatalf("неверная длительность: input = %v, status = %q, requests = %v", m.input, m.status, fj.requests)
	}
	press(m, key{name: keyCtrlU})
	typeText(m, "1d4h")
	press(m, key{name: keyEnter})
	if w := writes(fj); len(w) != 1 || w[0] != `PUT /rest/api/2/issue/K-1 {"fields":{"timetracking":{"originalEstimate":"1d 4h"}}}` {
		t.Fatalf("writes = %v", w)
	}
	if m.input != nil || m.status != "K-1: оценка 1d 4h" || m.issues[0].Estimate != 8*3600 {
		t.Fatalf("input = %v, status = %q, issue = %+v", m.input, m.status, m.issues[0])
	}
}

func TestViWorklog(t *testing.T) {
	m, fj := newViTest(t, routes{
		"POST /rest/api/2/issue/K-1/worklog": {201, `{}`},
		"GET /rest/api/2/issue/K-1":          {200, issueJSON("K-1", "Открыта", "1", "new")},
	}, viScope{}, isOpen)
	press(m, ch('w'))
	typeText(m, "проверил")
	press(m, key{name: keyEnter})
	if m.input == nil || !strings.Contains(m.status, `длительность "проверил"`) {
		t.Fatalf("без длительности: input = %v, status = %q", m.input, m.status)
	}
	press(m, key{name: keyCtrlU})
	typeText(m, "1h 30m проверил тесты")
	press(m, key{name: keyEnter})
	w := writes(fj)
	if len(w) != 1 || !strings.HasPrefix(w[0], "POST /rest/api/2/issue/K-1/worklog ") {
		t.Fatalf("writes = %v", w)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(fj.requests[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["timeSpent"] != "1h 30m" || body["comment"] != "проверил тесты" || body["started"] == "" {
		t.Fatalf("body = %v", body)
	}
	if !strings.HasPrefix(m.status, "K-1: списано 1h 30m за ") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestViCommentAndAssignee(t *testing.T) {
	m, fj := newViTest(t, routes{
		"POST /rest/api/2/issue/K-1/comment": {201, `{}`},
		"GET /rest/api/2/myself":             {200, `{"name":"nizom","displayName":"Низом"}`},
		"PUT /rest/api/2/issue/K-1/assignee": {204, ""},
		"GET /rest/api/2/issue/K-1":          {200, issueJSON("K-1", "Открыта", "1", "new")},
	}, viScope{}, isOpen)
	press(m, ch('c'), key{name: keyEnter})
	if m.input == nil || m.status != "пустой комментарий" {
		t.Fatalf("пустой: input = %v, status = %q", m.input, m.status)
	}
	typeText(m, "готово, см. MR")
	press(m, key{name: keyEnter})
	press(m, ch('a'), key{name: keyEnter}) // по умолчанию — me
	want := []string{
		`POST /rest/api/2/issue/K-1/comment {"body":"готово, см. MR"}`,
		`PUT /rest/api/2/issue/K-1/assignee {"name":"nizom"}`,
	}
	if w := writes(fj); strings.Join(w, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes:\n%s", strings.Join(w, "\n"))
	}
	if m.status != "K-1: assignee → nizom (Низом)" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestViOneRequestAtATime(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1/transitions": {200, k1Transitions},
	}, viScope{}, isOpen)
	c := m.update(ch('d'))
	if c == nil || m.busy == "" {
		t.Fatalf("d: cmd = %v, busy = %q", c, m.busy)
	}
	if again := m.update(ch('m')); again != nil || !strings.HasPrefix(m.status, "подождите") {
		t.Fatalf("второй запрос: cmd = %v, status = %q", again, m.status)
	}
	press(m, ch('j')) // навигация во время запроса работает
	settle(m, c)
	if m.busy != "" || m.confirm == nil || len(fj.requests) != 1 {
		t.Fatalf("busy = %q, confirm = %v, requests = %d", m.busy, m.confirm, len(fj.requests))
	}
}

const viProjectsJSON = `[{"key":"AAA","name":"Альфа"},{"key":"BBB","name":"Бета"},{"key":"CCC","name":"Гамма"}]`

const viMineSearch = `{"total":3,"issues":[
	{"key":"BBB-1","fields":{"summary":"б1","project":{"key":"BBB"},"status":{"id":"1","name":"Открыта","statusCategory":{"key":"new"}}}},
	{"key":"BBB-2","fields":{"summary":"б2","project":{"key":"BBB"},"status":{"id":"1","name":"Открыта","statusCategory":{"key":"new"}}}},
	{"key":"CCC-1","fields":{"summary":"в1","project":{"key":"CCC"},"status":{"id":"1","name":"Открыта","statusCategory":{"key":"new"}}}}]}`

func TestViProjectPicker(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/project": {200, viProjectsJSON},
		"GET /rest/api/2/search":  {200, viMineSearch},
	}, viScope{}, isOpen)
	press(m, ch('p'))
	if m.picker == nil || !m.picker.search {
		t.Fatalf("picker = %+v", m.picker)
	}
	var got []string
	for _, it := range m.picker.items {
		got = append(got, it.label+"|"+it.note)
	}
	want := []string{"все проекты|", "BBB  Бета|2 моих", "CCC  Гамма|1 моих", "AAA  Альфа|"}
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("items = %v", got)
	}
	typeText(m, "бет") // в поиске буквы — текст, а не команды
	if s := m.picker.shown(); len(s) != 1 || s[0].value != "BBB" {
		t.Fatalf("shown = %+v", s)
	}
	press(m, key{name: keyEnter})
	if m.picker != nil || m.scope.project != "BBB" || len(m.issues) != 3 || m.status != "проект BBB" {
		t.Fatalf("scope = %+v, issues = %d, status = %q", m.scope, len(m.issues), m.status)
	}
	jqls := searchJQLs(t, fj)
	if len(jqls) != 2 || jqls[0] != "assignee = currentUser() AND statusCategory != Done" ||
		!strings.HasPrefix(jqls[1], `assignee = currentUser() AND project = "BBB" AND `) {
		t.Fatalf("jqls = %q", jqls)
	}
	// список проектов запомнен: второй раз без запросов, курсор — на текущем
	n := len(fj.requests)
	press(m, ch('p'))
	if len(fj.requests) != n || m.picker.items[m.picker.cur].value != "BBB" {
		t.Fatalf("requests %d → %d, cur = %d", n, len(fj.requests), m.picker.cur)
	}
}

func TestViTeamToggle(t *testing.T) {
	m, fj := newViTest(t, routes{"GET /rest/api/2/search": {200, `{"total":0,"issues":[]}`}}, viScope{}, isOpen)
	press(m, ch('u'))
	if len(fj.requests) != 0 || !strings.Contains(m.status, "только в проекте или спринте") {
		t.Fatalf("без проекта: requests = %v, status = %q", fj.requests, m.status)
	}
	m.scope.project = "K"
	press(m, ch('u'))
	if !m.scope.team || searchJQL(t, fj) != `project = "K" AND (statusCategory != Done OR updated >= -14d) ORDER BY updated DESC` {
		t.Fatalf("team = %v, jql = %s", m.scope.team, searchJQL(t, fj))
	}
	press(m, ch('u'))
	if m.scope.team || m.status != "только мои задачи" {
		t.Fatalf("team = %v, status = %q", m.scope.team, m.status)
	}
	// «все проекты» снимает «всю команду»
	m.scope.team = true
	m.projects = []pickItem{{label: "все проекты"}, {label: "K", value: "K"}}
	press(m, ch('p'), key{name: keyEnter})
	if m.scope.project != "K" { // курсор стоял на текущем проекте
		t.Fatalf("project = %q", m.scope.project)
	}
	press(m, ch('p'), key{name: keyUp}, key{name: keyEnter}) // курсор на K, выше — «все проекты»
	if m.scope.project != "" || m.scope.team {
		t.Fatalf("scope = %+v", m.scope)
	}
}

func TestViLoadErrorKeepsScope(t *testing.T) {
	m, _ := newViTest(t, routes{
		"GET /rest/api/2/search": {400, `{"errorMessages":["Значение 'ZZZ' не существует для поля 'project'."]}`},
	}, viScope{}, isOpen)
	m.projects = []pickItem{{label: "все проекты"}, {label: "ZZZ", value: "ZZZ"}}
	press(m, ch('p'), key{name: keyDown}, key{name: keyEnter})
	if m.scope.project != "" || len(m.issues) != 1 || !m.isErr || !strings.Contains(m.status, "ZZZ") {
		t.Fatalf("scope = %+v, issues = %d, status = %q", m.scope, len(m.issues), m.status)
	}
}

func TestViFilter(t *testing.T) {
	a := viIss("K-1", "Открыта", "1", categoryNew)
	a.Summary = "Отчёт за месяц"
	b := viIss("K-2", "Открыта", "1", categoryNew)
	b.Summary = "Логин через SSO"
	m, _ := newViTest(t, routes{}, viScope{}, a, b)
	press(m, ch('.')) // «/» в русской раскладке
	typeText(m, "sso")
	if v := m.visible(); len(v) != 1 || v[0].Key != "K-2" || m.sel != "K-2" {
		t.Fatalf("visible = %v, sel = %s", v, m.sel)
	}
	press(m, key{name: keyEnter})
	if m.input != nil || m.filter != "sso" {
		t.Fatalf("input = %v, filter = %q", m.input, m.filter)
	}
	press(m, key{name: keyEsc})
	if m.filter != "" || len(m.visible()) != 2 {
		t.Fatalf("filter = %q", m.filter)
	}
}

func TestViDetail(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1": {200, `{"key":"K-1","fields":{"summary":"Сделать отчёт","description":"строка 1\nстрока 2",
			"status":{"name":"Открыта"},"issuetype":{"name":"Задача"},"comment":{"total":1,"comments":[{"author":{"name":"ivan"},"body":"ок","created":"2026-11-19T10:00:00.000+0000"}]}}}`},
	}, viScope{}, isOpen)
	press(m, key{name: keyEnter})
	d := m.details["K-1"]
	if m.screen != scrDetail || d == nil || d.show == nil || d.show.Summary != "Сделать отчёт" {
		t.Fatalf("screen = %v, detail = %+v", m.screen, d)
	}
	if p := fj.requests[0].Path; !strings.Contains(p, "fields="+showFields) {
		t.Fatalf("path = %s", p)
	}
	text := plainFrame(m.view(120, 30))
	for _, want := range []string{"Сделать отчёт", " Открыта ", "строка 2", "── Комментарии · 1", "ivan · 2026-11-19", "  ок", "/browse/K-1"} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q:\n%s", want, text)
		}
	}
	press(m, key{name: keyEsc})
	if m.screen != scrMain {
		t.Fatalf("screen = %v", m.screen)
	}
}

// sgr — стили, которые ставит сама отрисовка.
var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainFrame — кадр текстом без стилей.
func plainFrame(f frame) string {
	return sgr.ReplaceAllString(strings.Join(f.lines, "\n"), "")
}

// Каждая строка кадра — ровно ширина окна; кроме стилей отрисовки, ESC в кадре нет — даже если
// в данных с сервера есть ESC-последовательности.
func TestViViewFitsWindowAndStripsControls(t *testing.T) {
	evil := viIss("K-1\x1b[2J", "В\x1b]0;x\aработе", "3", categoryInProgress)
	evil.Summary = "\x1b[31mкрасный\x1b[0m заголовок\nвторой строкой и очень длинный текст, чтобы перенестись"
	evil.Assignee = "ivan\x07"
	wide := viIss("K-2", "Открыта", "1", categoryNew)
	wide.Summary = "日本語のタイトル 😀 и кириллица"
	wide.Estimate, wide.Spent = 28800, 5400
	issues := []viIssue{evil, wide, isDone, isOpen2, viIss("K-7", "Ревью", "10000", categoryInProgress),
		viIss("K-8", "Тест", "10004", categoryInProgress), viIss("K-9", "Бэклог", "10002", categoryNew)}
	m, _ := newViTest(t, routes{}, viScope{project: "K", team: true}, issues...)
	m.detailKey = "K-1\x1b[2J"
	m.details[m.detailKey] = &viDet{show: &showResult{Key: m.detailKey, Type: "Задача\x1b[1m", Status: "В\x1b]0;x\aработе",
		Priority: "Высокий", Summary: "\x1b[31mзаголовок\nв две строки", Assignee: "ivan\x07", Reporter: "日本語",
		Estimate: 3600, Spent: 7200, Remaining: 1800, Created: "2026-11-01", Updated: "2026-11-19",
		Parent:      &showParent{Key: "K-0", Summary: "эпик\x1b[0m"},
		Description: "h1. Заголовок\x1b[2J\n* пункт [ссылка|https://e.x/\x1b]8;;]\n{code}\nкод\tс табом \x1b[31m 日本\n{code}\n|a|b|\nописание\tс табом",
		Comments:    []showComment{{Author: "petr\x1b[1m", Date: "2026-11-19", Body: "bq. цитата\x07"}}, CommentsTotal: 3}}
	m.details["K-2"] = &viDet{err: errors.New("нет доступа\x1b[2J")}
	setups := map[string]func(){
		"list":          func() { m.mode, m.screen, m.preview = viList, scrMain, false },
		"board":         func() { m.mode, m.screen = viBoard, scrMain },
		"list+preview":  func() { m.mode, m.preview, m.sel = viList, true, m.detailKey },
		"board+preview": func() { m.mode = viBoard },
		"preview-error": func() { m.mode, m.sel = viList, "K-2" },
		"preview-load":  func() { m.sel = "K-7" },
		"detail":        func() { m.screen = scrDetail },
		"help":          func() { m.screen = scrHelp },
		"picker": func() {
			m.screen = scrMain
			m.picker = &viPicker{title: "Проект", search: true, items: []pickItem{{label: "все проекты"}, {label: "K  Кей\x1b[1m", note: "3 моих"}}}
		},
		"input":   func() { m.picker = nil; m.ask("Комментарий к K-1: ", "текст 日本", nil) },
		"confirm": func() { m.input = nil; m.confirm = &viConfirm{text: "Закрыть K-1 → Готово?"} },
	}
	for _, name := range []string{"list", "board", "list+preview", "board+preview", "preview-error", "preview-load",
		"detail", "help", "picker", "input", "confirm"} {
		setups[name]()
		for _, size := range [][2]int{{40, 8}, {80, 24}, {120, 30}, {200, 50}, {30, 5}} {
			w, h := size[0], size[1]
			f := m.view(w, h)
			if len(f.lines) != h {
				t.Errorf("%s %dx%d: строк %d", name, w, h, len(f.lines))
			}
			for i, l := range f.lines {
				plain := sgr.ReplaceAllString(l, "")
				if strings.ContainsAny(plain, "\x1b\x07\n\t") {
					t.Errorf("%s %dx%d: строка %d с управляющими символами: %q", name, w, h, i, plain)
				}
				if tw := textWidth(plain); tw != w {
					t.Errorf("%s %dx%d: строка %d шириной %d: %q", name, w, h, i, tw, plain)
				}
			}
			if f.cursor != nil && (f.cursor.row >= h || f.cursor.col >= w) {
				t.Errorf("%s %dx%d: курсор вне окна: %+v", name, w, h, *f.cursor)
			}
		}
	}
}

func TestViBoardShowsColumnsAndCards(t *testing.T) {
	m, _ := newViTest(t, routes{}, viScope{}, isOpen, isWork, isDone, isOpen2)
	press(m, ch('v'))
	text := plainFrame(m.view(120, 20))
	for _, want := range []string{"Открыта (2)", "В работе (1)", "Готово (1)", "K-4", "задача K-2", "[канбан]"[1:7]} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q в кадре:\n%s", want, text)
		}
	}
	// узкое окно: колонки прокручиваются, выбранная видна
	press(m, ch('l'), ch('l'))
	text = plainFrame(m.view(50, 20))
	if !strings.Contains(text, "Готово (1)") || !strings.Contains(text, "‹") {
		t.Fatalf("узкое окно:\n%s", text)
	}
}

func TestViListScrollsToSelection(t *testing.T) {
	var issues []viIssue
	for i := 1; i <= 50; i++ {
		issues = append(issues, viIss(fmt.Sprintf("K-%d", i), "Открыта", "1", categoryNew))
	}
	m, _ := newViTest(t, routes{}, viScope{}, issues...)
	press(m, ch('G'))
	if m.sel != "K-50" || !strings.Contains(plainFrame(m.view(80, 20)), "K-50") {
		t.Fatalf("G: sel = %s", m.sel)
	}
	press(m, key{name: keyPgUp}, ch('g'))
	m.view(80, 20) // прокрутка поправляется при отрисовке
	if m.sel != "K-1" || m.listTop != 0 {
		t.Fatalf("g: sel = %s, top = %d", m.sel, m.listTop)
	}
}

func TestViQuit(t *testing.T) {
	for _, k := range []key{ch('q'), ch('й'), {name: keyCtrlC}} {
		m, _ := newViTest(t, routes{}, viScope{}, isOpen)
		press(m, k)
		if !m.quitting() {
			t.Errorf("%+v: не вышел", k)
		}
	}
	// в поле ввода q — буква, Ctrl+C — выход
	m, _ := newViTest(t, routes{}, viScope{}, isOpen)
	press(m, ch('c'), ch('q'))
	if m.quitting() || string(m.input.buf) != "q" {
		t.Fatalf("q в поле ввода: quit = %v", m.quitting())
	}
	press(m, key{name: keyCtrlC})
	if !m.quitting() {
		t.Fatal("Ctrl+C в поле ввода")
	}
}
