package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Панель справа, загрузка подробностей, их оформление и сохранение вида между запусками.

func showJSON(k, summary, desc string) string {
	b, _ := json.Marshal(map[string]any{"key": k, "fields": map[string]any{
		"summary": summary, "description": desc, "status": map[string]any{"name": "Открыта"},
		"issuetype": map[string]any{"name": "Задача"}, "priority": map[string]any{"name": "Высокий"},
		"reporter": map[string]any{"name": "petr"}, "created": "2026-11-01T10:00:00.000+0000",
		"timetracking": map[string]any{"originalEstimateSeconds": 14400, "timeSpentSeconds": 7200},
	}})
	return string(b)
}

func detailGets(fj *fakeJira) []string {
	var out []string
	for _, r := range fj.requests {
		if r.Method == "GET" && strings.HasPrefix(r.Path, "/rest/api/2/issue/") && strings.Contains(r.Path, "description") {
			p, _, _ := strings.Cut(r.Path, "?")
			out = append(out, strings.TrimPrefix(p, "/rest/api/2/issue/"))
		}
	}
	return out
}

func TestViPreviewShowsSelectedIssue(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1": {200, showJSON("K-1", "Сделать отчёт", "h2. Цель\n* первое\n* второе")},
		"GET /rest/api/2/issue/K-2": {200, showJSON("K-2", "Починить вход", "")},
	}, viScope{}, isOpen, isWork)
	m.preview = true
	press(m, key{name: keyEsc}) // любой шаг — и панель грузит выбранную задачу
	text := plainFrame(m.view(120, 30))
	for _, want := range []string{"│ K-1  Задача · Высокий", "│ Сделать отчёт", "Автор      petr", "Время      2h из 4h",
		"50%", "── Описание", "│ Цель", "│ • первое"} {
		if !strings.Contains(text, want) {
			t.Errorf("нет %q:\n%s", want, text)
		}
	}
	press(m, ch('j'))
	if !strings.Contains(plainFrame(m.view(120, 30)), "Починить вход") {
		t.Fatalf("панель не перешла на K-2:\n%s", plainFrame(m.view(120, 30)))
	}
	press(m, ch('k')) // K-1 уже загружена — без нового запроса
	if got := detailGets(fj); strings.Join(got, ",") != "K-1,K-2" {
		t.Fatalf("запросы подробностей: %v", got)
	}
	// узкое окно — панели нет, список во всю ширину
	if text := plainFrame(m.view(80, 30)); strings.Contains(text, "Автор") {
		t.Fatalf("панель в узком окне:\n%s", text)
	}
}

// Пока выбор бежит по списку, подробности не грузятся: запрос — только для задачи, на которой остановились.
func TestViPreviewWaitsForPause(t *testing.T) {
	m, fj := newViTest(t, routes{"GET /rest/api/2/issue/K-4": {200, showJSON("K-4", "четвёртая", "")}},
		viScope{}, isOpen, isWork, isOpen2)
	m.preview, m.previewDelay = true, 1
	var waits []cmd
	for range 2 {
		waits = append(waits, m.update(ch('j'))) // K-2, затем K-4: ответы паузы ещё не пришли
	}
	if m.sel != "K-4" || waits[0] == nil || waits[1] == nil {
		t.Fatalf("sel = %s, waits = %v", m.sel, waits)
	}
	for _, w := range waits {
		settle(m, w)
	}
	if got := detailGets(fj); strings.Join(got, ",") != "K-4" {
		t.Fatalf("запросы подробностей: %v", got)
	}
}

// Загрузка подробностей не мешает действиям, а действие над задачей перезагружает её подробности;
// ответ, запрошенный до действия, не затирает новый.
func TestViDetailRefreshedAfterAction(t *testing.T) {
	m, fj := newViTest(t, routes{
		"GET /rest/api/2/issue/K-1":          {200, showJSON("K-1", "отчёт", "")},
		"POST /rest/api/2/issue/K-1/comment": {201, `{}`},
	}, viScope{}, isOpen)
	m.preview = true
	inflight := m.update(key{name: keyEsc}) // запрос подробностей начат, ответ ещё не пришёл
	if m.fetching != "K-1" || m.busy != "" {
		t.Fatalf("fetching = %q, busy = %q", m.fetching, m.busy)
	}
	press(m, ch('c'))
	typeText(m, "готово")
	press(m, key{name: keyEnter}) // комментарий добавлен: подробности K-1 устарели
	if w := writes(fj); len(w) != 1 {
		t.Fatalf("writes = %v", w)
	}
	settle(m, inflight) // старый ответ не кладётся, вместо него — новый запрос
	if got := detailGets(fj); len(got) != 2 {
		t.Fatalf("запросы подробностей: %v", got)
	}
	if d := m.details["K-1"]; d == nil || d.stale || d.show == nil {
		t.Fatalf("detail = %+v", d)
	}
	// r в подробном виде — загрузить заново
	press(m, key{name: keyEnter}, ch('r'))
	if got := detailGets(fj); len(got) != 3 {
		t.Fatalf("r: запросы подробностей: %v", got)
	}
}

func TestViDetailLoadError(t *testing.T) {
	m, _ := newViTest(t, routes{"GET /rest/api/2/issue/K-1": {404, `{"errorMessages":["Задача не найдена"]}`}}, viScope{}, isOpen)
	press(m, key{name: keyEnter})
	text := plainFrame(m.view(100, 30))
	if m.screen != scrDetail || !strings.Contains(text, "не загрузилось") || !strings.Contains(text, "r — повторить") ||
		!strings.Contains(text, "задача K-1") { // шапка — из списка
		t.Fatalf("screen = %v:\n%s", m.screen, text)
	}
}

func TestViTogglePreview(t *testing.T) {
	m, _ := newViTest(t, routes{}, viScope{}, isOpen)
	m.update(resizeMsg{80, 30})
	press(m, ch('ш')) // i в русской раскладке
	if !m.preview || m.st.HidePreview || !strings.Contains(m.status, "нужно хотя бы 94") {
		t.Fatalf("preview = %v, status = %q", m.preview, m.status)
	}
	press(m, ch('i'))
	if m.preview || !m.st.HidePreview {
		t.Fatalf("preview = %v, st = %+v", m.preview, m.st)
	}
}

func TestViMarkup(t *testing.T) {
	src := "h1. Заголовок\r\n\n\n" +
		"Текст *жирный* и *ещё* с [ссылкой|https://e.x] и [~ivan], {color:red}красный{color}, {{код}}.\n" +
		"# раз\n# два\n## вложенный\n# три\n" +
		"* пункт\n- тоже пункт\n" +
		"bq. цитата\n" +
		"{code:go}\nfunc main() {\n\treturn\n}\n{code}\n" +
		"||A||B||\n|1|[x|https://y]|\n" +
		"----\n!shot.png|thumbnail!\n\n"
	var got []string
	for _, l := range renderMarkup(parseMarkup(src), 60) {
		got = append(got, strings.TrimRight(sgr.ReplaceAllString(l, ""), " "))
	}
	want := []string{
		"Заголовок",
		"",
		"Текст жирный и ещё с ссылкой (https://e.x) и @ivan, красный,",
		"код.",
		"1. раз",
		"2. два",
		"  1. вложенный",
		"3. три",
		"• пункт",
		"• тоже пункт",
		"│ цитата",
		"│ func main() {",
		"│     return",
		"│ }",
		"A │ B",
		"1 │ x (https://y)",
		strings.Repeat("─", 60),
		"[изображение shot.png]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestViProgressBar(t *testing.T) {
	plain := func(s string) string { return sgr.ReplaceAllString(s, "") }
	if got := plain(progressBar(7200, 14400, 10)); got != "━━━━━─────  50%" {
		t.Errorf("половина: %q", got)
	}
	if got := plain(progressBar(21600, 14400, 10)); got != "━━━━━━━━━━ 150%" {
		t.Errorf("перерасход: %q", got)
	}
}

// ---------- состояние между запусками ----------

func readViState(t *testing.T) (viState, bool) {
	t.Helper()
	path, err := viStatePath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return viState{}, false
	}
	var st viState
	if err != nil || json.Unmarshal(b, &st) != nil {
		t.Fatalf("vi.json: %v %s", err, b)
	}
	return st, true
}

func swapViRun(t *testing.T, run func(*viModel)) {
	t.Helper()
	pt, pr := viTerminal, viRun
	viTerminal = func() bool { return true }
	viRun = func(p program) error {
		run(p.(*viModel))
		return nil
	}
	t.Cleanup(func() { viTerminal, viRun = pt, pr })
}

func TestViStateSavedBetweenRuns(t *testing.T) {
	search := `{"total":1,"issues":[` + issueJSON("K-1", "Открыта", "1", "new") + `]}`
	fj := newFakeJira(t, routes{"GET /rest/api/2/search": {200, search}})
	base := strings.TrimRight(fj.srv.URL, "/")

	// первый запуск — как по умолчанию; выбранное в экране сохраняется при выходе
	swapViRun(t, func(m *viModel) {
		if m.mode != viList || !m.preview || m.scope.project != "" {
			t.Errorf("первый запуск: mode = %v, preview = %v, scope = %+v", m.mode, m.preview, m.scope)
		}
		press(m, ch('v'), ch('i')) // размер окна не пришёл — панель не видна и ничего не грузит
		m.projects = []pickItem{{label: "все проекты"}, {label: "K", value: "K"}}
		press(m, ch('p'), key{name: keyDown}, key{name: keyEnter}, ch('u'))
	})
	if _, err := fj.run("vi"); err != nil {
		t.Fatal(err)
	}
	if st, ok := readViState(t); !ok || st != (viState{URL: base, View: viStateBoard, HidePreview: true, Project: "K", Team: true}) {
		t.Fatalf("vi.json = %+v (есть: %v)", st, ok)
	}

	// без флагов — как оставили
	var got *viModel
	swapViRun(t, func(m *viModel) { got = m })
	if _, err := fj.run("vi"); err != nil {
		t.Fatal(err)
	}
	jqls := searchJQLs(t, fj)
	if got.mode != viBoard || got.preview || got.scope.project != "K" || !got.scope.team ||
		jqls[len(jqls)-1] != `project = "K" AND (statusCategory != Done OR updated >= -14d) ORDER BY updated DESC` {
		t.Fatalf("второй запуск: mode = %v, scope = %+v, jql = %s", got.mode, got.scope, jqls[len(jqls)-1])
	}

	// флаги — на один запуск: сохранённая область не применяется и не меняется, вид — применяется
	path, _ := viStatePath()
	before, _ := os.ReadFile(path)
	if _, err := fj.run("vi", "--project", "Z"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if got.scope.project != "Z" || got.scope.team || got.mode != viBoard || string(before) != string(after) {
		t.Fatalf("--project: scope = %+v, mode = %v, файл %s → %s", got.scope, got.mode, before, after)
	}

	// состояние другой Jira: проект не берётся
	if err := saveViState(viState{URL: "https://other.example", View: viStateList, Project: "K", Team: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := fj.run("vi"); err != nil {
		t.Fatal(err)
	}
	if got.scope.project != "" || got.scope.team || got.mode != viList {
		t.Fatalf("другая Jira: scope = %+v, mode = %v", got.scope, got.mode)
	}
}

// Сохранённый проект не загрузился (удалён, нет доступа) — экран открывается со всеми проектами.
func TestViSavedProjectGone(t *testing.T) {
	q := func(jql string) string {
		return "GET /rest/api/2/search?" + url.Values{"jql": {jql}, "fields": {viFields}, "startAt": {"0"}, "maxResults": {"100"}}.Encode()
	}
	open := " AND (statusCategory != Done OR updated >= -14d) ORDER BY updated DESC"
	fj := newFakeJira(t, routes{
		q(`assignee = currentUser() AND project = "GONE"` + open): {400, `{"errorMessages":["Проекта 'GONE' нет"]}`},
		q(`assignee = currentUser()` + open):                      {200, `{"total":0,"issues":[]}`},
	})
	base := strings.TrimRight(fj.srv.URL, "/")
	if err := saveViState(viState{URL: base, Project: "GONE"}); err != nil {
		t.Fatal(err)
	}
	var got *viModel
	swapViRun(t, func(m *viModel) { got = m })
	if _, err := fj.run("vi"); err != nil {
		t.Fatal(err)
	}
	if got.scope.project != "" || !got.isErr || !strings.Contains(got.status, "GONE") {
		t.Fatalf("scope = %+v, status = %q", got.scope, got.status)
	}
	if st, _ := readViState(t); st.Project != "" {
		t.Fatalf("недоступный проект остался в vi.json: %+v", st)
	}
}

func TestViBrokenStateIgnored(t *testing.T) {
	tempConfig(t)
	path, _ := viStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := loadViState("https://j"); st != (viState{URL: "https://j"}) {
		t.Fatalf("st = %+v", st)
	}
}
