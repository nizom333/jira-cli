package main

// jira vi — интерактивный экран задач: список и канбан, выбор проекта, действия над задачей
// (закрыть, сменить статус, оценка, списание времени, комментарий, исполнитель).
// viModel — программа для runProgram (term.go): update меняет состояние и возвращает cmd — запрос
// к Jira в фоне; одновременно идёт не больше одного запроса (busy). Отрисовка — vi_view.go,
// поле ввода, выбор из списка и подтверждение — vi_input.go, подробности задачи — vi_detail.go,
// состояние между запусками — vi_state.go.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// viFields — поля задач для jira vi.
const viFields = "summary,status,timetracking,assignee,project"

const viDefaultLimit = 100

// Status category — ключи категорий статуса в Jira.
const (
	categoryNew        = "new"
	categoryInProgress = "indeterminate"
	categoryDone       = "done"
)

// categoryRank — порядок колонок канбана: к выполнению, в работе, готово; неизвестная — как «в работе».
func categoryRank(c string) int {
	switch c {
	case categoryNew:
		return 0
	case categoryDone:
		return 2
	}
	return 1
}

// viIssue — задача на экране. Строки — как пришли с сервера: чистятся при отрисовке.
type viIssue struct {
	Key, Summary, Project, Assignee string
	Status, StatusID, Category      string // Category — ключ Status category
	Estimate, Spent                 duration
}

func newViIssue(is Issue) viIssue {
	f := is.Fields
	v := viIssue{Key: is.Key, Summary: f.Summary, Project: f.Project.Key,
		Status: f.Status.Name, StatusID: f.Status.ID, Category: f.Status.StatusCategory.Key,
		Estimate: f.TimeTracking.OriginalEstimate, Spent: f.TimeTracking.TimeSpent}
	if f.Assignee != nil {
		v.Assignee = f.Assignee.Name
	}
	return v
}

func (is viIssue) done() bool { return is.Category == categoryDone }

// matches — задача подходит под поиск: каждое слово q есть в ключе, названии, статусе или исполнителе.
func (is viIssue) matches(q string) bool {
	hay := strings.ToLower(is.Key + " " + is.Summary + " " + is.Status + " " + is.Assignee)
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// ---------- что показываем ----------

// viScope — какие задачи загружены: проект, спринты и чьи.
type viScope struct {
	project     string   // "" — все проекты
	sprints     []Sprint // --sprint: задачи этих спринтов; nil — без спринта
	sprintLabel string
	user        string // исполнитель: "" — я, иначе username из --user
	team        bool   // все исполнители (только в проекте или спринте)
	n           int
}

// canTeam — «вся команда» имеет смысл только в проекте или спринте: иначе это вся Jira.
func (s viScope) canTeam() bool { return s.project != "" || len(s.sprints) > 0 }

// assigneeJQL — значение assignee в JQL для исполнителя области (без учёта team).
func (s viScope) assigneeJQL() string {
	if s.user == "" {
		return "currentUser()"
	}
	return jqlQuote(s.user)
}

// jql — запрос задач области. Без спринта — незакрытые и закрытые, обновлённые за 14 дней
// (только что закрытая задача остаётся в колонке «готово»); в спринте — все задачи спринта.
func (s viScope) jql() string {
	var parts []string
	if len(s.sprints) > 0 {
		ids := make([]string, len(s.sprints))
		for i, sp := range s.sprints {
			ids[i] = strconv.Itoa(sp.ID)
		}
		parts = append(parts, "sprint in ("+strings.Join(ids, ", ")+")")
	}
	if !s.team {
		parts = append(parts, "assignee = "+s.assigneeJQL())
	}
	if s.project != "" {
		parts = append(parts, "project = "+jqlQuote(s.project))
	}
	if len(s.sprints) > 0 {
		return strings.Join(parts, " AND ") + " ORDER BY Rank ASC"
	}
	parts = append(parts, "(statusCategory != Done OR updated >= -14d)")
	return strings.Join(parts, " AND ") + " ORDER BY updated DESC"
}

// label — область для шапки: проект · спринт · чьи.
func (s viScope) label() string {
	parts := []string{"все проекты"}
	if s.project != "" {
		parts[0] = s.project
	}
	if s.sprintLabel != "" {
		parts = append(parts, s.sprintLabel)
	}
	switch {
	case s.team:
		parts = append(parts, "вся команда")
	case s.user != "":
		parts = append(parts, s.user)
	default:
		parts = append(parts, "мои")
	}
	return strings.Join(parts, " · ")
}

// viOptions — аргументы jira vi, проверенные без Jira.
type viOptions struct {
	project, user string
	sprint        *sprintTarget
	n             int
}

// scoped — область задана флагами: тогда она на этот запуск, сохранённая не применяется.
func (o viOptions) scoped() bool { return o.project != "" || o.user != "" || o.sprint != nil }

func parseViOptions(a Args) (viOptions, error) {
	o := viOptions{project: a.get("project"), user: a.get("user")}
	n, err := limitOr(a, viDefaultLimit)
	if err != nil {
		return o, err
	}
	o.n = n
	if a.has("sprint") {
		t, err := parseSprintTarget(a.get("sprint"))
		if err != nil {
			return o, fmt.Errorf("--sprint: %w", err)
		}
		o.sprint = &t
	}
	return o, nil
}

// resolveViScope проверяет исполнителя и находит спринты --sprint (как jira sprint now|active|ID);
// warnings — доски, пропущенные из-за ошибки Jira.
func resolveViScope(c *Client, o viOptions, today string) (viScope, []string, error) {
	s := viScope{project: o.project, n: o.n}
	if !isSelfRef(o.user) {
		u, err := c.filterUser(o.user)
		if err != nil {
			return s, nil, err
		}
		s.user = u.Name
	}
	if o.sprint == nil {
		return s, nil, nil
	}
	var warnings []string
	if o.sprint.kind == targetID {
		sp, err := c.sprint(o.sprint.id)
		if err != nil {
			return s, nil, err
		}
		s.sprints = []Sprint{*sp}
	} else {
		found, ws, err := activeSprints(c, o.project, today)
		if err != nil {
			return s, nil, err
		}
		warnings = ws
		for _, bs := range found {
			if o.sprint.kind == targetActive || phaseOf(bs.sprint, today) == phaseCurrent {
				s.sprints = append(s.sprints, bs.sprint)
			}
		}
		if len(s.sprints) == 0 {
			if o.sprint.kind == targetCurrent {
				return s, nil, noCurrentSprint(found, ws, o.project, today)
			}
			return s, nil, noCurrentSprint(nil, ws, o.project, today)
		}
	}
	names := make([]string, len(s.sprints))
	for i, sp := range s.sprints {
		names[i] = "«" + sp.Name + "»"
	}
	s.sprintLabel = "спринт " + strings.Join(names, ", ")
	return s, warnings, nil
}

func viSearch(c *Client, jql string, n int) ([]viIssue, int, error) {
	r, err := c.search(jql, viFields, 0, n)
	if err != nil {
		return nil, 0, err
	}
	issues := make([]viIssue, 0, len(r.Issues))
	for _, is := range r.Issues {
		issues = append(issues, newViIssue(is))
	}
	return issues, r.Total, nil
}

// viTerminal — есть ли терминал, viRun — экран; тесты подменяют.
var (
	viTerminal = isTerminal
	viRun      = runProgram
)

// cmdVi: аргументы и терминал проверяются до запросов к Jira; первая загрузка — до входа в экран,
// так что её ошибка (токен, проект, спринт) печатается как у любой команды.
// Без флагов области экран открывается так, как его оставили (vi_state.go); выбранное в экране
// сохраняется при выходе.
func cmdVi(c *Client, a Args) (result, error) {
	o, err := parseViOptions(a)
	if err != nil {
		return nil, err
	}
	if !viTerminal() {
		return nil, errors.New("jira vi — интерактивная команда, нужен терминал (в скриптах — jira ls, jira q, jira sprint)")
	}
	st := loadViState(c.base)
	saved := st
	restored := !o.scoped() // область — как в прошлый раз
	if restored {
		o.project = st.Project
	}
	scope, warnings, err := resolveViScope(c, o, localToday())
	if err != nil {
		return nil, err
	}
	if restored {
		scope.team = st.Team && scope.canTeam()
	}
	issues, total, err := viSearch(c, scope.jql(), scope.n)
	if err != nil && restored && st.Project != "" {
		// проект из прошлого запуска удалён или недоступен — не мешать открыть экран
		warnings = append(warnings, fmt.Sprintf("проект %s из прошлого раза не загрузился (%v) — показаны все проекты", st.Project, err))
		scope.project, scope.team = "", false
		st.Project, st.Team = "", false
		issues, total, err = viSearch(c, scope.jql(), scope.n)
	}
	if err != nil {
		return nil, err
	}
	m := newViModel(c, scope, issues, total)
	m.restore(st)
	if len(warnings) > 0 {
		m.flashErr("предупреждение: " + strings.Join(warnings, "; "))
	}
	err = viRun(m)
	if m.st != saved {
		if serr := saveViState(m.st); serr != nil {
			fmt.Fprintf(os.Stderr, "внимание: вид jira vi не сохранён: %v\n", serr)
		}
	}
	return nil, err
}

// ---------- модель ----------

type viMode int

const (
	viList viMode = iota
	viBoard
)

type viScreen int

const (
	scrMain viScreen = iota
	scrDetail
	scrHelp
)

type viModel struct {
	c      *Client
	scope  viScope
	issues []viIssue
	total  int
	mode   viMode
	screen viScreen
	sel    string // ключ выбранной задачи: выбор переживает перезагрузку и смену вида
	filter string // поиск «/» по загруженным задачам

	// прокрутка — поправляется при отрисовке под размер окна
	listTop   int
	boardLeft int
	colTop    map[string]int // статус → первая видимая карточка колонки

	detailKey string // открытая подробно задача
	detailTop int

	// подробности задач — для подробного вида и панели справа (vi_detail.go)
	preview      bool              // панель справа включена (видна, если окно позволяет)
	details      map[string]*viDet // загруженные подробности по ключу задачи
	detailGen    int               // растёт при сбросе: ответ, запрошенный до сброса, не кладётся
	fetching     string            // чьи подробности загружаются; одновременно — одни
	waiting      string            // чьи подробности ждут паузы в листании
	previewDelay time.Duration     // пауза: пока выбор бежит по списку, запросы не идут

	st viState // что сохранить при выходе: выбранное в экране

	// панель внизу: не больше одной сразу
	input   *viInput
	picker  *viPicker
	confirm *viConfirm

	status   string
	isErr    bool
	busy     string // что сейчас делается в Jira; "" — ничего
	projects []pickItem
	w, h     int
	quit     bool
	open     func(url string) error // открыть ссылку в браузере
}

func newViModel(c *Client, s viScope, issues []viIssue, total int) *viModel {
	m := &viModel{c: c, scope: s, issues: issues, total: total, colTop: map[string]int{}, open: openBrowser,
		preview: true, details: map[string]*viDet{}, previewDelay: 150 * time.Millisecond,
		st: viState{URL: c.base}}
	m.fixSel()
	return m
}

// restore — вид и панель как в прошлый раз (область уже выбрана в cmdVi).
func (m *viModel) restore(st viState) {
	m.st = st
	m.mode = viList
	if st.View == viStateBoard {
		m.mode = viBoard
	}
	m.preview = !st.HidePreview
}

// setMode переключает список и канбан и запоминает выбор.
func (m *viModel) setMode(mode viMode) {
	m.mode = mode
	m.st.View = viStateList
	if mode == viBoard {
		m.st.View = viStateBoard
	}
}

func (m *viModel) quitting() bool { return m.quit }

func (m *viModel) flash(s string)    { m.status, m.isErr = s, false }
func (m *viModel) flashErr(s string) { m.status, m.isErr = s, true }

// ---------- сообщения ----------

// viLoaded — задачи области scope загружены (при ошибке область остаётся прежней);
// chosen — область выбрали в экране (проект, мои или все): её запомнить.
type viLoaded struct {
	scope  viScope
	issues []viIssue
	total  int
	note   string
	chosen bool
	err    error
}

// viUpdated — действие над задачей выполнено; issue — задача, перечитанная после него (nil — не менялась).
type viUpdated struct {
	key   string
	issue *viIssue
	note  string
	err   error
}

// transIntent — зачем запрошены переходы задачи.
type transIntent struct {
	kind   int    // intentDone, intentPick, intentTo
	status string // intentTo: целевой статус
}

const (
	intentDone = iota // закрыть: переход в статус категории done
	intentPick        // выбрать любой переход
	intentTo          // в статус соседней колонки канбана
)

type viTransitions struct {
	issue  viIssue
	ts     []transition
	intent transIntent
	err    error
}

// viDetail — подробности задачи загружены; gen — поколение кэша на момент запроса.
type viDetail struct {
	key  string
	gen  int
	show *showResult
	err  error
}

// viWait — пауза в листании прошла: если выбор всё ещё на key, загрузить его подробности.
type viWait struct{ key string }

type viProjects struct {
	items []pickItem
	err   error
}

// update — шаг модели и загрузка подробностей, которые после него нужны экрану.
func (m *viModel) update(ms msg) cmd {
	c := m.step(ms)
	return batch(c, m.prefetch())
}

func (m *viModel) step(ms msg) cmd {
	switch v := ms.(type) {
	case resizeMsg:
		m.w, m.h = v.w, v.h
	case key:
		return m.onKey(v)
	case viLoaded:
		m.busy = ""
		if v.err != nil {
			m.flashErr(v.err.Error())
			return nil
		}
		m.scope, m.issues, m.total = v.scope, v.issues, v.total
		m.colTop = map[string]int{}
		m.flash(v.note)
		m.fixSel()
		m.invalidate("")
		if v.chosen {
			m.st.Project, m.st.Team = v.scope.project, v.scope.team
		}
	case viUpdated:
		m.busy = ""
		if v.issue != nil {
			m.replace(*v.issue)
		}
		m.fixSel() // задача могла выпасть из поиска: действия не должны уходить на невидимую
		m.invalidate(v.key)
		if v.err != nil {
			m.flashErr(v.err.Error())
		} else {
			m.flash(v.note)
		}
	case viTransitions:
		m.busy = ""
		return m.onTransitions(v)
	case viDetail:
		m.fetching = ""
		if v.gen == m.detailGen { // иначе задача менялась, пока шёл запрос: ответ мог устареть
			m.details[v.key] = &viDet{show: v.show, err: v.err}
		}
	case viWait:
		if v.key != m.waiting {
			return nil // выбор ушёл дальше: ждёт уже другая задача
		}
		m.waiting = ""
		if m.wantDetail() == v.key && m.needDetail(v.key) && m.fetching == "" {
			return m.fetchDetail(v.key)
		}
	case viProjects:
		m.busy = ""
		if v.err != nil {
			m.flashErr("проекты: " + v.err.Error())
			return nil
		}
		m.projects = v.items
		m.pickProject()
	}
	return nil
}

// replace подменяет задачу перечитанной после действия. Если выбранная задача от этого переехала
// (закрытая уходит вниз списка), в списке выбор остаётся на той же строке — удобно закрывать задачи
// подряд; на канбане выбор идёт за карточкой в её новую колонку.
func (m *viModel) replace(is viIssue) {
	row := indexOf(m.visible(), m.sel)
	for i := range m.issues {
		if m.issues[i].Key == is.Key {
			m.issues[i] = is
		}
	}
	if m.mode != viList || is.Key != m.sel || row < 0 {
		return
	}
	if v := m.visible(); len(v) > 0 && indexOf(v, m.sel) != row {
		m.sel = v[min(row, len(v)-1)].Key
	}
}

// visible — задачи на экране: подходящие под поиск, незакрытые выше закрытых, иначе в порядке запроса.
func (m *viModel) visible() []viIssue {
	var open, done []viIssue
	for _, is := range m.issues {
		if m.filter != "" && !is.matches(m.filter) {
			continue
		}
		if is.done() {
			done = append(done, is)
		} else {
			open = append(open, is)
		}
	}
	return append(open, done...)
}

func indexOf(list []viIssue, k string) int {
	for i, is := range list {
		if is.Key == k {
			return i
		}
	}
	return -1
}

// fixSel — если выбранной задачи нет среди видимых, выбрать первую.
func (m *viModel) fixSel() {
	v := m.visible()
	if indexOf(v, m.sel) >= 0 {
		return
	}
	m.sel = ""
	if len(v) > 0 {
		m.sel = v[0].Key
	}
}

// current — задача, к которой относятся действия: открытая подробно или выбранная.
func (m *viModel) current() (viIssue, bool) {
	k := m.sel
	if m.screen == scrDetail {
		k = m.detailKey
	}
	for _, is := range m.issues {
		if is.Key == k {
			return is, true
		}
	}
	return viIssue{}, false
}

// ---------- канбан ----------

// viColumn — колонка канбана: один статус (Kanban column в GLOSSARY.md).
type viColumn struct {
	status, statusID, category string
	issues                     []viIssue
}

// viColumns группирует задачи по статусам. Колонки — по Status category (к выполнению, в работе,
// готово), внутри категории — по id статуса (порядок их создания в Jira), карточки — в порядке задач.
func viColumns(issues []viIssue) []viColumn {
	var cols []viColumn
	idx := map[string]int{}
	for _, is := range issues {
		i, ok := idx[is.Status]
		if !ok {
			i = len(cols)
			idx[is.Status] = i
			cols = append(cols, viColumn{status: is.Status, statusID: is.StatusID, category: is.Category})
		}
		cols[i].issues = append(cols[i].issues, is)
	}
	sort.SliceStable(cols, func(a, b int) bool {
		ca, cb := cols[a], cols[b]
		if ra, rb := categoryRank(ca.category), categoryRank(cb.category); ra != rb {
			return ra < rb
		}
		ia, ea := strconv.Atoi(ca.statusID)
		ib, eb := strconv.Atoi(cb.statusID)
		if ea == nil && eb == nil && ia != ib {
			return ia < ib
		}
		return ca.status < cb.status
	})
	return cols
}

// boardPos — колонка и строка выбранной задачи на канбане (0, 0, если её нет).
func (m *viModel) boardPos(cols []viColumn) (int, int) {
	for ci, c := range cols {
		if ri := indexOf(c.issues, m.sel); ri >= 0 {
			return ci, ri
		}
	}
	return 0, 0
}

// ---------- навигация ----------

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

const far = 1 << 20 // «в самый конец» для move

// move двигает выбор: в списке — на dr строк; на канбане — на dc колонок и dr карточек
// (в соседней колонке — карточка той же высоты или последняя).
func (m *viModel) move(dc, dr int) {
	v := m.visible()
	if len(v) == 0 {
		return
	}
	if m.mode == viList {
		i := max(0, indexOf(v, m.sel))
		m.sel = v[clamp(i+dr, 0, len(v)-1)].Key
		return
	}
	cols := viColumns(v)
	ci, ri := m.boardPos(cols)
	ci = clamp(ci+dc, 0, len(cols)-1)
	col := cols[ci].issues
	m.sel = col[clamp(ri+dr, 0, len(col)-1)].Key
}

// page — на сколько строк (в списке) или карточек (на канбане) листает PgUp/PgDn.
func (m *viModel) page() int {
	body := m.h - 4
	if m.mode == viBoard {
		return max(1, (body-1)/viCardH)
	}
	return max(1, body-1)
}

// ---------- клавиши ----------

func (m *viModel) onKey(k key) cmd {
	if k.name == keyCtrlC {
		m.quit = true
		return nil
	}
	switch {
	case m.input != nil:
		return m.inputKey(k)
	case m.picker != nil:
		return m.pickerKey(k)
	case m.confirm != nil:
		return m.confirmKey(latinKey(k))
	}
	k = latinKey(k)
	switch m.screen {
	case scrHelp:
		m.screen = scrMain
		return nil
	case scrDetail:
		return m.detailKeys(k)
	}
	return m.mainKey(k)
}

func (m *viModel) mainKey(k key) cmd {
	switch {
	case k.is('q'):
		m.quit = true
	case k.name == keyEsc:
		if m.filter != "" {
			m.filter = ""
			m.fixSel()
		} else {
			m.status = ""
		}
	case k.is('j') || k.name == keyDown:
		m.move(0, 1)
	case k.is('k') || k.name == keyUp:
		m.move(0, -1)
	case k.is('h') || k.name == keyLeft:
		m.move(-1, 0)
	case k.is('l') || k.name == keyRight:
		m.move(1, 0)
	case k.is('g') || k.name == keyHome:
		m.move(0, -far)
	case k.is('G') || k.name == keyEnd:
		m.move(0, far)
	case k.name == keyPgDown:
		m.move(0, m.page())
	case k.name == keyPgUp:
		m.move(0, -m.page())
	case k.is('v') || k.name == keyTab || k.name == keyShiftTab:
		m.setMode(1 - m.mode)
	case k.is('i'):
		m.togglePreview()
	case k.is('?'):
		m.screen = scrHelp
	case k.is('/'):
		m.askFilter()
	case k.is('H') || k.name == keyShiftLeft:
		return m.withIssue(func(is viIssue) cmd { return m.shiftCard(is, -1) })
	case k.is('L') || k.name == keyShiftRight:
		return m.withIssue(func(is viIssue) cmd { return m.shiftCard(is, 1) })
	case k.is('p'):
		return m.whenIdle(m.openProjects)
	case k.is('u'):
		return m.whenIdle(m.toggleTeam)
	case k.is('r'):
		return m.whenIdle(func() cmd {
			m.projects = nil
			return m.reload(m.scope, "обновлено")
		})
	case k.name == keyEnter || k.is(' '):
		if is, ok := m.current(); ok {
			m.screen, m.detailKey, m.detailTop = scrDetail, is.Key, 0
		}
	default:
		return m.actionKey(k)
	}
	return nil
}

// actionKey — действия над задачей: одинаковы в списке, на канбане и в подробном виде.
func (m *viModel) actionKey(k key) cmd {
	switch {
	case k.is('d'):
		return m.withIssue(func(is viIssue) cmd { return m.fetchTransitions(is, transIntent{kind: intentDone}) })
	case k.is('m'):
		return m.withIssue(func(is viIssue) cmd { return m.fetchTransitions(is, transIntent{kind: intentPick}) })
	case k.is('e'):
		return m.withIssue(m.askEstimate)
	case k.is('w'):
		return m.withIssue(m.askWorklog)
	case k.is('c'):
		return m.withIssue(m.askComment)
	case k.is('a'):
		return m.withIssue(m.askAssignee)
	case k.is('o'):
		if is, ok := m.current(); ok {
			u := m.c.base + "/browse/" + url.PathEscape(is.Key)
			if err := m.open(u); err != nil {
				m.flashErr("браузер: " + err.Error())
			} else {
				m.flash("открыто в браузере: " + u)
			}
		}
	}
	return nil
}

func (m *viModel) detailKeys(k key) cmd {
	switch {
	case k.is('q') || k.is('h') || k.name == keyEsc || k.name == keyLeft || k.name == keyEnter:
		m.screen = scrMain
	case k.is('j') || k.name == keyDown:
		m.detailTop++
	case k.is('k') || k.name == keyUp:
		m.detailTop--
	case k.name == keyPgDown || k.is(' '):
		m.detailTop += max(1, m.h-5)
	case k.name == keyPgUp:
		m.detailTop -= max(1, m.h-5)
	case k.is('g') || k.name == keyHome:
		m.detailTop = 0
	case k.is('G') || k.name == keyEnd:
		m.detailTop = far
	case k.is('?'):
		m.screen = scrHelp
	case k.is('r'):
		m.invalidate(m.detailKey)
	default:
		return m.actionKey(k)
	}
	return nil
}

// whenIdle выполняет f, если в Jira ничего не выполняется: запросы идут по одному.
func (m *viModel) whenIdle(f func() cmd) cmd {
	if m.busy != "" {
		m.flash("подождите: " + m.busy)
		return nil
	}
	return f()
}

// withIssue — whenIdle для действия над текущей задачей.
func (m *viModel) withIssue(f func(viIssue) cmd) cmd {
	return m.whenIdle(func() cmd {
		is, ok := m.current()
		if !ok {
			m.flash("нет выбранной задачи")
			return nil
		}
		return f(is)
	})
}

// run помечает модель занятой и возвращает запрос f.
func (m *viModel) run(label string, f func() msg) cmd {
	m.busy = label
	return f
}

// reload загружает задачи области s; область меняется, только если загрузка удалась.
func (m *viModel) reload(s viScope, note string) cmd { return m.load(s, note, false) }

// choose — reload области, выбранной в экране: после загрузки она запоминается до следующего запуска.
func (m *viModel) choose(s viScope, note string) cmd { return m.load(s, note, true) }

func (m *viModel) load(s viScope, note string, chosen bool) cmd {
	c, jql, n := m.c, s.jql(), s.n
	return m.run("загрузка", func() msg {
		issues, total, err := viSearch(c, jql, n)
		return viLoaded{scope: s, issues: issues, total: total, note: note, chosen: chosen, err: err}
	})
}

func (m *viModel) toggleTeam() cmd {
	if !m.scope.canTeam() {
		m.flash("вся команда — только в проекте или спринте: p — выбрать проект")
		return nil
	}
	s := m.scope
	s.team = !s.team
	note := "задачи всей команды"
	switch {
	case s.team:
	case s.user != "":
		note = "только задачи " + s.user
	default:
		note = "только мои задачи"
	}
	return m.choose(s, note)
}

// viRefetch перечитывает задачу после действия note.
func viRefetch(c *Client, key, note string) msg {
	is, err := c.getIssue(key, viFields)
	if err != nil {
		return viUpdated{key: key, note: note, err: fmt.Errorf("%s, но задачу не перечитать: %w", note, err)}
	}
	v := newViIssue(*is)
	return viUpdated{key: key, issue: &v, note: note}
}

// ---------- статусы ----------

func (m *viModel) fetchTransitions(is viIssue, in transIntent) cmd {
	c, k := m.c, is.Key
	return m.run("переходы "+k, func() msg {
		ts, err := c.transitions(k)
		return viTransitions{issue: is, ts: ts, intent: in, err: err}
	})
}

// shiftCard переносит карточку в соседнюю колонку канбана: переход в статус этой колонки.
func (m *viModel) shiftCard(is viIssue, dir int) cmd {
	if m.mode != viBoard {
		m.flash("H/L — перенос между колонками канбана (v — канбан); статус в списке — m")
		return nil
	}
	cols := viColumns(m.visible())
	ci, _ := m.boardPos(cols)
	if ci+dir < 0 || ci+dir >= len(cols) {
		m.flash("дальше колонок нет; любой статус — m")
		return nil
	}
	return m.fetchTransitions(is, transIntent{kind: intentTo, status: cols[ci+dir].status})
}

func (m *viModel) onTransitions(v viTransitions) cmd {
	if v.err != nil {
		m.flashErr(v.err.Error())
		return nil
	}
	is, ts := v.issue, v.ts
	var cand []transition
	switch v.intent.kind {
	case intentDone:
		for _, t := range ts {
			if t.To.StatusCategory.Key == categoryDone {
				cand = append(cand, t)
			}
		}
		if len(cand) == 0 {
			m.flashErr(fmt.Sprintf("из статуса «%s» нет перехода в закрытый статус — m: все переходы", is.Status))
			return nil
		}
		if len(cand) == 1 {
			t := cand[0]
			m.confirm = &viConfirm{text: fmt.Sprintf("Закрыть %s → %s?", is.Key, t.To.Name),
				yes: func() cmd { return m.doTransition(is, t) }}
			return nil
		}
		m.pickTransition("Закрыть "+is.Key, is, cand)
	case intentPick:
		if len(ts) == 0 {
			m.flashErr(fmt.Sprintf("из статуса «%s» переходов нет", is.Status))
			return nil
		}
		m.pickTransition("Статус "+is.Key+" ("+is.Status+")", is, ts)
	case intentTo:
		for _, t := range ts {
			if strings.EqualFold(strings.TrimSpace(t.To.Name), strings.TrimSpace(v.intent.status)) {
				cand = append(cand, t)
			}
		}
		switch {
		case len(cand) == 0:
			m.flashErr(fmt.Sprintf("из «%s» нет перехода в «%s» — m: доступные переходы", is.Status, v.intent.status))
		case len(cand) > 1:
			m.pickTransition("В «"+v.intent.status+"»: "+is.Key, is, cand)
		case cand[0].To.StatusCategory.Key == categoryDone: // закрытие — только с подтверждением
			t := cand[0]
			m.confirm = &viConfirm{text: fmt.Sprintf("Закрыть %s → %s?", is.Key, t.To.Name),
				yes: func() cmd { return m.doTransition(is, t) }}
		default:
			return m.doTransition(is, cand[0])
		}
	}
	return nil
}

func (m *viModel) pickTransition(title string, is viIssue, ts []transition) {
	items := make([]pickItem, len(ts))
	for i, t := range ts {
		items[i] = pickItem{label: t.Name + " → " + t.To.Name, value: strconv.Itoa(i)}
	}
	m.picker = &viPicker{title: title, items: items, choose: func(it pickItem) cmd {
		i, _ := strconv.Atoi(it.value)
		return m.doTransition(is, ts[i])
	}}
}

func (m *viModel) doTransition(is viIssue, t transition) cmd {
	c, k := m.c, is.Key
	note := fmt.Sprintf("%s → %s", k, t.To.Name)
	return m.run(note, func() msg {
		if err := c.applyTransition(k, t.ID); err != nil {
			return viUpdated{key: k, err: err}
		}
		return viRefetch(c, k, note)
	})
}

// ---------- ввод: оценка, списание, комментарий, исполнитель, поиск ----------

// ask открывает поле ввода; submit проверяет текст: ошибка остаётся в поле, nil — поле закрывается.
func (m *viModel) ask(prompt, initial string, submit func(text string) (cmd, error)) *viInput {
	m.status = ""
	m.input = &viInput{prompt: prompt, buf: []rune(initial), pos: len([]rune(initial)), submit: submit}
	return m.input
}

func (m *viModel) askEstimate(is viIssue) cmd {
	initial := ""
	if is.Estimate > 0 {
		initial = is.Estimate.String()
	}
	c, k := m.c, is.Key
	m.ask("Оценка "+k+" (1d 4h, 2h30m): ", initial, func(text string) (cmd, error) {
		est, err := parseDur(text)
		if err != nil {
			return nil, err
		}
		return m.run("оценка "+k, func() msg {
			if err := setEstimate(c, k, est); err != nil {
				return viUpdated{key: k, err: err}
			}
			return viRefetch(c, k, k+": оценка "+est)
		}), nil
	})
	return nil
}

func (m *viModel) askWorklog(is viIssue) cmd {
	c, k := m.c, is.Key
	m.ask("Списать на "+k+" (1h 30m [комментарий]): ", "", func(text string) (cmd, error) {
		durArgs, rest := splitDur(strings.Fields(text))
		if len(durArgs) == 0 && len(rest) > 0 {
			durArgs = rest[:1] // нет ведущей длительности — parseDur объяснит, что не так с первым словом
		}
		spent, err := parseDur(durArgs...)
		if err != nil {
			return nil, err
		}
		comment, started := strings.Join(rest, " "), time.Now()
		return m.run("списание "+k, func() msg {
			if err := c.addWorklog(k, spent, started, comment); err != nil {
				return viUpdated{key: k, err: err}
			}
			return viRefetch(c, k, fmt.Sprintf("%s: списано %s за %s", k, spent, started.Format(dayLayout)))
		}), nil
	})
	return nil
}

func (m *viModel) askComment(is viIssue) cmd {
	c, k := m.c, is.Key
	m.ask("Комментарий к "+k+": ", "", func(text string) (cmd, error) {
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("пустой комментарий")
		}
		return m.run("комментарий "+k, func() msg {
			if err := c.addComment(k, text); err != nil {
				return viUpdated{key: k, err: err}
			}
			return viUpdated{key: k, note: k + ": комментарий добавлен"}
		}), nil
	})
	return nil
}

func (m *viModel) askAssignee(is viIssue) cmd {
	c, k := m.c, is.Key
	m.ask("Исполнитель "+k+" (username, me, none): ", "me", func(text string) (cmd, error) {
		ref := strings.TrimSpace(text)
		if ref == "" {
			return nil, errors.New("укажите username, me или none")
		}
		return m.run("исполнитель "+k, func() msg {
			u, err := c.userRef(ref)
			if err != nil {
				return viUpdated{key: k, err: err}
			}
			if err := c.setAssignee(k, u); err != nil {
				return viUpdated{key: k, err: err}
			}
			return viRefetch(c, k, k+": assignee → "+userLabel(u))
		}), nil
	})
	return nil
}

// askFilter — поиск по загруженным задачам: фильтр меняется на каждую букву, Esc сбрасывает.
func (m *viModel) askFilter() {
	in := m.ask("/", m.filter, func(string) (cmd, error) { return nil, nil })
	in.change = func(text string) {
		m.filter = text
		m.fixSel()
	}
	in.cancel = func() {
		m.filter = ""
		m.fixSel()
	}
}

// ---------- проекты ----------

func (m *viModel) openProjects() cmd {
	if m.projects != nil {
		m.pickProject()
		return nil
	}
	c, assignee := m.c, m.scope.assigneeJQL()
	who := "моих"
	if m.scope.user != "" {
		who = "у " + m.scope.user
	}
	return m.run("проекты", func() msg { return loadViProjects(c, assignee, who) })
}

// loadViProjects — все проекты Jira; сверху — те, где у исполнителя есть незакрытые задачи (больше — выше).
func loadViProjects(c *Client, assignee, who string) msg {
	ps, err := c.projects()
	if err != nil {
		return viProjects{err: err}
	}
	open, err := c.searchAll("assignee = "+assignee+" AND statusCategory != Done", "project")
	if err != nil {
		return viProjects{err: err}
	}
	counts := map[string]int{}
	for _, is := range open {
		counts[is.Fields.Project.Key]++
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if ci, cj := counts[ps[i].Key], counts[ps[j].Key]; ci != cj {
			return ci > cj
		}
		return ps[i].Key < ps[j].Key
	})
	items := []pickItem{{label: "все проекты", value: ""}}
	for _, p := range ps {
		it := pickItem{label: p.Key + "  " + p.Name, value: p.Key}
		if n := counts[p.Key]; n > 0 {
			it.note = fmt.Sprintf("%d %s", n, who)
		}
		items = append(items, it)
	}
	return viProjects{items: items}
}

func (m *viModel) pickProject() {
	p := &viPicker{title: "Проект", items: m.projects, search: true, choose: func(it pickItem) cmd {
		s := m.scope
		s.project = it.value
		if !s.canTeam() {
			s.team = false
		}
		note := "все проекты"
		if it.value != "" {
			note = "проект " + it.value
		}
		return m.choose(s, note)
	}}
	for i, it := range m.projects {
		if it.value == m.scope.project {
			p.cur = i
		}
	}
	m.picker = p
}

// openBrowser открывает ссылку в браузере по умолчанию, не дожидаясь его.
func openBrowser(u string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	return nil
}
