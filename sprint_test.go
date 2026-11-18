package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Sprint phase — по календарным датам в поясе пользователя: день конца спринта — ещё «идёт».
func TestPhaseOf(t *testing.T) {
	inLocal(t, time.FixedZone("+05", 5*3600))
	sp := func(state, start, end string) Sprint { return Sprint{State: state, StartDate: start, EndDate: end} }
	const today = "2026-11-20"
	for _, tc := range []struct {
		s    Sprint
		want sprintPhase
	}{
		{sp("active", "2026-11-18T10:00:00.000Z", "2026-11-25T10:00:00.000Z"), phaseCurrent},
		{sp("active", "2026-11-18T10:00:00.000+0500", "2026-11-25T10:00:00.000+0500"), phaseCurrent},
		// конец сегодня утром, начало сегодня вечером — сегодня спринт идёт весь день
		{sp("active", "2026-11-13T06:00:00.000Z", "2026-11-20T06:00:00.000Z"), phaseCurrent},
		{sp("active", "2026-11-20T13:00:00.000Z", "2026-11-27T13:00:00.000Z"), phaseCurrent},
		// 19:30 UTC 19-го — это уже 20-е по +05: день считается в поясе пользователя
		{sp("active", "2026-11-12T10:00:00.000Z", "2026-11-19T19:30:00.000Z"), phaseCurrent},
		{sp("active", "2026-11-12T10:00:00.000Z", "2026-11-19T18:30:00.000Z"), phaseOverdue},
		{sp("active", "2026-08-18T10:00:00.000Z", "2026-09-01T10:00:00.000Z"), phaseOverdue},
		{sp("active", "2026-11-21T10:00:00.000Z", "2026-11-28T10:00:00.000Z"), phaseNotStart},
		{sp("active", "", "2026-11-25T10:00:00.000Z"), phaseUndated},
		{sp("active", "2026-11-18T10:00:00.000Z", "завтра"), phaseUndated},
		{sp("future", "", ""), phaseFuture},
		{sp("closed", "2026-11-18T10:00:00.000Z", "2026-11-25T10:00:00.000Z"), phaseClosed},
	} {
		if got := phaseOf(tc.s, today); got != tc.want {
			t.Errorf("phaseOf(%s – %s) = %s, want %s", tc.s.StartDate, tc.s.EndDate, got, tc.want)
		}
	}
}

// Тесты команды идут в UTC (reportInUTC): локальная дата = дата UTC, даты спринтов — от сегодня.
// Сегодня — момент запуска тестов: команда сама читает часы.
var testToday = time.Now().UTC()

// newSprintJira — фейк Jira для тестов sprint, пояс — UTC.
func newSprintJira(t *testing.T, rs routes) *fakeJira {
	reportInUTC(t)
	return newFakeJira(t, rs)
}

// dayFromToday — дата через d дней от testToday (d < 0 — в прошлом), YYYY-MM-DD.
func dayFromToday(d int) string { return testToday.AddDate(0, 0, d).Format(dayLayout) }

// sprintReply — спринт в ответе Agile API.
func sprintReply(s Sprint) string {
	b, _ := json.Marshal(s) // у Sprint только строки и числа — ошибки не бывает
	return string(b)
}

// sprintJSON — активный спринт id с началом и концом через start и end дней от сегодня (в 10:00 UTC).
func sprintJSON(id int, name string, start, end, origin int) string {
	return sprintReply(Sprint{ID: id, Name: name, State: sprintActive, OriginBoardID: origin,
		StartDate: dayFromToday(start) + "T10:00:00.000Z", EndDate: dayFromToday(end) + "T10:00:00.000Z"})
}

// agileReply — последняя страница списка Agile API с данными values.
func agileReply(values ...string) reply {
	return reply{200, `{"isLast":true,"values":[` + strings.Join(values, ",") + `]}`}
}

// Две scrum-доски: спринт 5 виден на обеих (создан на доске 2), 3 просрочен, 7 — идёт.
func sprintRoutes() routes {
	return routes{
		"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска A"}`, `{"id":2,"name":"Доска B"}`),
		"GET /rest/agile/1.0/board/1/sprint": agileReply(sprintJSON(3, "K Спринт 3", -94, -80, 1), sprintJSON(5, "K Спринт 5", -2, 5, 2)),
		"GET /rest/agile/1.0/board/2/sprint": agileReply(sprintJSON(5, "K Спринт 5", -2, 5, 2), sprintJSON(7, "L Спринт 7", -4, 10, 2)),
		"GET /rest/api/2/search":             oneIssueSearch,
	}
}

var oneIssueSearch = reply{200, `{"total":1,"issues":[{"key":"K-1","fields":{"summary":"задача","status":{"name":"Открыта"}}}]}`}

const issueLine = "K-1\tОткрыта\t-\t-\tзадача\n"

// sprintHeader — строка-заголовок спринта в выводе задач.
func sprintHeader(id int, name string, start, end int, phase string) string {
	return fmt.Sprintf("# %d %s · %s – %s · %s\n", id, name, dayFromToday(start), dayFromToday(end), phase)
}

func TestSprintListsActiveSprintsCurrentFirstWithoutDuplicates(t *testing.T) {
	fj := newSprintJira(t, sprintRoutes())
	out, err := fj.run("sprint")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("5\t%s\t%s\tидёт\tДоска B\tK Спринт 5\n", dayFromToday(-2), dayFromToday(5)) +
		fmt.Sprintf("7\t%s\t%s\tидёт\tДоска B\tL Спринт 7\n", dayFromToday(-4), dayFromToday(10)) +
		fmt.Sprintf("3\t%s\t%s\tпросрочен\tДоска A\tK Спринт 3\n", dayFromToday(-94), dayFromToday(-80))
	if out != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
	if q := fj.requests[0].Path; !strings.Contains(q, "type=scrum") || strings.Contains(q, "projectKeyOrId") {
		t.Errorf("доски: %s", q)
	}
}

func TestSprintProjectFiltersBoards(t *testing.T) {
	fj := newSprintJira(t, sprintRoutes())
	if _, err := fj.run("sprint", "--project", "K"); err != nil {
		t.Fatal(err)
	}
	if q := fj.requests[0].Path; !strings.Contains(q, "projectKeyOrId=K") || !strings.Contains(q, "type=scrum") {
		t.Errorf("доски: %s", q)
	}
}

func TestSprintListEmpty(t *testing.T) {
	fj := newSprintJira(t, routes{"GET /rest/agile/1.0/board": agileReply()})
	out, err := fj.run("sprint")
	if err != nil || out != "# нет активных спринтов\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestSprintNowShowsIssuesOfEachCurrentSprint(t *testing.T) {
	fj := newSprintJira(t, sprintRoutes())
	out, err := fj.run("sprint", "now")
	if err != nil {
		t.Fatal(err)
	}
	if want := sprintHeader(5, "K Спринт 5", -2, 5, "идёт") + issueLine + sprintHeader(7, "L Спринт 7", -4, 10, "идёт") + issueLine; out != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
	if got := searchJQLs(t, fj); len(got) != 2 || got[0] != "sprint = 5 ORDER BY Rank ASC" || got[1] != "sprint = 7 ORDER BY Rank ASC" {
		t.Fatalf("jql = %q", got)
	}
}

// Спринт, который кончается сегодня в первую секунду дня, — ещё текущий: сравниваются даты, а не моменты.
func TestSprintNowIncludesSprintEndingToday(t *testing.T) {
	ending := sprintReply(Sprint{ID: 9, Name: "K Спринт 9", State: sprintActive, OriginBoardID: 1,
		StartDate: dayFromToday(-7) + "T00:00:00.000Z", EndDate: dayFromToday(0) + "T00:00:01.000Z"})
	fj := newSprintJira(t, routes{
		"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска A"}`),
		"GET /rest/agile/1.0/board/1/sprint": agileReply(ending),
		"GET /rest/api/2/search":             oneIssueSearch,
	})
	out, err := fj.run("sprint", "now")
	if err != nil {
		t.Fatal(err)
	}
	if want := sprintHeader(9, "K Спринт 9", -7, 0, "идёт") + issueLine; out != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
}

// active — задачи всех Active sprint, и просроченных тоже; порядок — как в списке спринтов.
func TestSprintActiveShowsIssuesOfEveryActiveSprint(t *testing.T) {
	fj := newSprintJira(t, sprintRoutes())
	out, err := fj.run("sprint", "active", "--open")
	if err != nil {
		t.Fatal(err)
	}
	want := sprintHeader(5, "K Спринт 5", -2, 5, "идёт") + issueLine + sprintHeader(7, "L Спринт 7", -4, 10, "идёт") + issueLine +
		sprintHeader(3, "K Спринт 3", -94, -80, "просрочен") + issueLine
	if out != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
	wantJQL := []string{"sprint = 5 AND statusCategory != Done ORDER BY Rank ASC",
		"sprint = 7 AND statusCategory != Done ORDER BY Rank ASC", "sprint = 3 AND statusCategory != Done ORDER BY Rank ASC"}
	if got := searchJQLs(t, fj); strings.Join(got, "|") != strings.Join(wantJQL, "|") {
		t.Fatalf("jql = %q", got)
	}
}

func TestSprintActiveWithoutActiveSprints(t *testing.T) {
	fj := newSprintJira(t, routes{"GET /rest/agile/1.0/board": agileReply()})
	out, err := fj.run("sprint", "active")
	if err != nil || out != "# нет активных спринтов\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if js, err := fj.run("sprint", "active", "--json"); err != nil || js != "{\"sprints\":[],\"warnings\":[]}\n" {
		t.Fatalf("json = %q, err = %v", js, err)
	}
}

func TestSprintFiltersIssuesByUserOpenAndProject(t *testing.T) {
	rs := routes{
		"GET /rest/agile/1.0/sprint/5": {200, sprintJSON(5, "K Спринт 5", -2, 5, 1)},
		"GET /rest/api/2/user":         {200, `{"name":"ivan.p","displayName":"Иван"}`},
		"GET /rest/api/2/search":       oneIssueSearch,
	}
	for _, tc := range []struct {
		args []string
		jql  string
	}{
		{[]string{"5", "--user", "me"}, "sprint = 5 AND assignee = currentUser() ORDER BY Rank ASC"},
		{[]string{"5", "--user", "ivan.p", "--open", "--project", "K"},
			`sprint = 5 AND assignee = "ivan.p" AND statusCategory != Done AND project = "K" ORDER BY Rank ASC`},
	} {
		fj := newSprintJira(t, rs)
		if _, err := fj.run("sprint", tc.args...); err != nil {
			t.Fatal(err)
		}
		if got := searchJQLs(t, fj); len(got) != 1 || got[0] != tc.jql {
			t.Errorf("%v: jql = %q, want %q", tc.args, got, tc.jql)
		}
	}
}

func TestSprintNowWithoutCurrentSprintPointsToActiveOnes(t *testing.T) {
	fj := newSprintJira(t, routes{
		"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска A"}`, `{"id":2,"name":"Доска B"}`),
		"GET /rest/agile/1.0/board/1/sprint": agileReply(sprintJSON(3, "K Спринт 3", -94, -80, 1)),
		"GET /rest/agile/1.0/board/2/sprint": agileReply(),
	})
	out, err := fj.run("sprint", "now", "--project", "K")
	want := fmt.Sprintf("нет текущего спринта в проекте K (активного, идущего сегодня по датам); активные: "+
		"3 «K Спринт 3» (просрочен, %s – %s) — задачи: jira sprint 3 или все активные: jira sprint active --project K",
		dayFromToday(-94), dayFromToday(-80))
	if err == nil || err.Error() != want || out != "" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if len(searchJQLs(t, fj)) != 0 {
		t.Fatal("без текущего спринта задачи не запрашиваются")
	}
}

// Текущий спринт мог быть на пропущенной доске: ошибка now называет пропущенные доски.
func TestSprintNowWithoutCurrentSprintKeepsBoardWarnings(t *testing.T) {
	broken := reply{500, `{"errorMessages":["boom"]}`}
	for _, tc := range []struct {
		board2 reply
		want   string
	}{
		{agileReply(sprintJSON(3, "K Спринт 3", -94, -80, 2)), fmt.Sprintf("нет текущего спринта (активного, идущего сегодня по датам); "+
			"активные: 3 «K Спринт 3» (просрочен, %s – %s) — задачи: jira sprint 3 или все активные: jira sprint active; "+
			"доска 1 (Доска A) пропущена: HTTP 500: boom", dayFromToday(-94), dayFromToday(-80))},
		{agileReply(), "нет активных спринтов; доска 1 (Доска A) пропущена: HTTP 500: boom"},
	} {
		fj := newSprintJira(t, routes{
			"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска A"}`, `{"id":2,"name":"Доска B"}`),
			"GET /rest/agile/1.0/board/1/sprint": broken,
			"GET /rest/agile/1.0/board/2/sprint": tc.board2,
		})
		if _, err := fj.run("sprint", "now"); err == nil || err.Error() != tc.want {
			t.Errorf("err = %v\nwant  %s", err, tc.want)
		}
	}
}

func TestSprintUnknownID(t *testing.T) {
	fj := newSprintJira(t, routes{"GET /rest/agile/1.0/sprint/9": {404, `{"errorMessages":["Спринт не существует."]}`}})
	if _, err := fj.run("sprint", "9"); err == nil || err.Error() != "спринт 9 не найден" {
		t.Fatalf("err = %v", err)
	}
}

// Сломанная доска (нет прав, удалён фильтр) не обрывает команду: её спринты пропускаются с предупреждением.
func TestSprintSkipsBrokenBoardWithWarning(t *testing.T) {
	reportInUTC(t)
	rs := routes{
		"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска A"}`, `{"id":2,"name":"Доска B"}`),
		"GET /rest/agile/1.0/board/1/sprint": {500, `{"errorMessages":["boom"]}`},
		"GET /rest/agile/1.0/board/2/sprint": agileReply(),
	}
	text, js := runBoth(t, rs, "sprint")
	if want := "# нет активных спринтов\n# предупреждение: доска 1 (Доска A) пропущена: HTTP 500: boom\n"; text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	if w, _ := js["warnings"].([]any); len(w) != 1 || w[0] != "доска 1 (Доска A) пропущена: HTTP 500: boom" {
		t.Errorf("warnings = %v", js["warnings"])
	}
}

func TestSprintFailsWhenEveryBoardFails(t *testing.T) {
	fj := newSprintJira(t, routes{
		"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска A"}`),
		"GET /rest/agile/1.0/board/1/sprint": {500, `{"errorMessages":["boom"]}`},
	})
	if _, err := fj.run("sprint", "now"); err == nil || err.Error() != "доска 1 (Доска A): HTTP 500: boom" {
		t.Fatalf("err = %v", err)
	}
}

// Ошибки аргументов — до любого запроса к Jira.
func TestSprintArgErrorsBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--user", "me"}, "--user: только для задач спринта"},
		{[]string{"--open"}, "--open: только для задач спринта"},
		{[]string{"-n", "5"}, "-n: только для задач спринта"},
		{[]string{"abc"}, "ожидается now, active или номер спринта"},
		{[]string{"0"}, "ожидается now, active или номер спринта"},
		{[]string{"now", "-n", "0"}, "-n: ожидается положительное число"},
		{[]string{"now", "5"}, "лишний аргумент"},
		{[]string{"now", "--user", "none"}, "--user none: нет такого фильтра"},
	} {
		fj := newSprintJira(t, routes{})
		_, err := fj.run("sprint", tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
		if len(fj.requests) != 0 {
			t.Errorf("%v: запросы %+v", tc.args, fj.requests)
		}
	}
}

func TestSprintParityMultilineNames(t *testing.T) {
	reportInUTC(t)
	multiline := sprintReply(Sprint{ID: 5, Name: "K\nСпринт 5", Goal: "цель\nдва", State: sprintActive, OriginBoardID: 1,
		StartDate: dayFromToday(-2) + "T10:00:00.000Z", EndDate: dayFromToday(5) + "T10:00:00.000Z"})
	rs := routes{
		"GET /rest/agile/1.0/board":          agileReply(`{"id":1,"name":"Доска\nA"}`),
		"GET /rest/agile/1.0/board/1/sprint": agileReply(multiline),
		"GET /rest/api/2/search":             {200, multilineSearch},
	}
	sprintFields := map[string]string{"name": "K\nСпринт 5", "goal": "цель\nдва", "phase": "current",
		"start": dayFromToday(-2), "end": dayFromToday(5)}

	text, js := runBoth(t, rs, "sprint")
	if want := fmt.Sprintf("5\t%s\t%s\tидёт\tДоска A\tK Спринт 5\n", dayFromToday(-2), dayFromToday(5)); text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
	sprints, _ := js["sprints"].([]any)
	if len(sprints) != 1 {
		t.Fatalf("sprints = %v", js["sprints"])
	}
	wantFields(t, "sprint.sprints[0]", sprints[0], sprintFields)
	wantFields(t, "sprint.sprints[0]", sprints[0], map[string]string{"board": "Доска\nA"})

	text, js = runBoth(t, rs, "sprint", "now")
	if want := sprintHeader(5, "K Спринт 5", -2, 5, "идёт") + "K-1 X\tВ работе\t1h\t-\ta b c\n"; text != want {
		t.Errorf("now text = %q, want %q", text, want)
	}
	sprints, _ = js["sprints"].([]any)
	if len(sprints) != 1 {
		t.Fatalf("now sprints = %v", js["sprints"])
	}
	s := sprints[0].(map[string]any)
	wantFields(t, "sprint now.sprints[0]", s, sprintFields)
	if _, nested := s["sprint"]; nested {
		t.Errorf("now: спринт и задачи — в одном объекте, без вложенного sprint: %v", s)
	}
	issues, _ := s["issues"].([]any)
	if s["total"] != float64(1) || len(issues) != 1 {
		t.Fatalf("now: %v", s)
	}
	wantFields(t, "sprint now.issues[0]", issues[0], map[string]string{"key": "K-1\nX", "summary": "a\nb\tc"})
}

// sprintWithIssues повторяет поля sprintInfo (ради алфавитного порядка ключей): новое поле — в обоих.
func TestSprintWithIssuesHasEverySprintInfoField(t *testing.T) {
	keys := func(v any) map[string]bool {
		b, _ := json.Marshal(v)
		m := map[string]any{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for k := range m {
			out[k] = true
		}
		return out
	}
	with := keys(newSprintWithIssues(sprintInfo{}, &issueList{}))
	for k := range keys(sprintInfo{}) {
		if !with[k] {
			t.Errorf("в sprintWithIssues нет поля %q из sprintInfo", k)
		}
	}
}
