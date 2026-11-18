package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sprint phase — положение спринта во времени. Для Active sprint — по календарным датам
// относительно сегодняшней, для остальных — его состояние в Jira (future, closed).
type sprintPhase string

const (
	phaseCurrent  sprintPhase = "current"     // активный, сегодня от даты начала до даты конца — Current sprint
	phaseOverdue  sprintPhase = "overdue"     // активный, дата конца прошла, а спринт не закрыли
	phaseNotStart sprintPhase = "not_started" // активный, дата начала ещё не наступила
	phaseUndated  sprintPhase = "undated"     // активный без дат или с неразборчивыми датами
	phaseFuture   sprintPhase = "future"
	phaseClosed   sprintPhase = "closed"
)

var phaseText = map[sprintPhase]string{
	phaseCurrent: "идёт", phaseOverdue: "просрочен", phaseNotStart: "не начат",
	phaseUndated: "без дат", phaseFuture: "будущий", phaseClosed: "закрыт",
}

func (p sprintPhase) text() string {
	if t, ok := phaseText[p]; ok {
		return t
	}
	return string(p)
}

// localToday — сегодняшняя дата в поясе пользователя, YYYY-MM-DD.
func localToday() string { return time.Now().In(time.Local).Format(dayLayout) }

// parseSprintTime — дата спринта с сервера (ISO 8601: 2026-11-18T10:07:00.000Z или с +0500).
func parseSprintTime(ts string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, jiraTime} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// localDay — дата спринта в поясе пользователя, YYYY-MM-DD; ok = false, если даты нет или она неразборчива.
func localDay(ts string) (string, bool) {
	t, ok := parseSprintTime(ts)
	if !ok {
		return "", false
	}
	return t.In(time.Local).Format(dayLayout), true
}

// sprintDay — дата спринта для вывода: localDay или "-".
func sprintDay(ts string) string {
	if d, ok := localDay(ts); ok {
		return d
	}
	return "-"
}

// phaseOf — Sprint phase спринта s в день today (YYYY-MM-DD в поясе пользователя). Сравниваются
// календарные даты: в день конца спринт ещё идёт, в день начала — уже идёт.
func phaseOf(s Sprint, today string) sprintPhase {
	switch s.State {
	case sprintFuture:
		return phaseFuture
	case sprintClosed:
		return phaseClosed
	}
	start, okStart := localDay(s.StartDate)
	end, okEnd := localDay(s.EndDate)
	switch {
	case !okStart || !okEnd:
		return phaseUndated
	case today < start: // YYYY-MM-DD: порядок строк — порядок дат
		return phaseNotStart
	case today > end:
		return phaseOverdue
	}
	return phaseCurrent
}

// sprintInfo — спринт в выводе: даты — в поясе пользователя, фаза — на сегодня.
type sprintInfo struct {
	End   string      `json:"end"`
	Goal  string      `json:"goal"`
	ID    int         `json:"id"`
	Name  string      `json:"name"`
	Phase sprintPhase `json:"phase"`
	Start string      `json:"start"`
}

func newSprintInfo(s Sprint, today string) sprintInfo {
	return sprintInfo{End: sprintDay(s.EndDate), Goal: s.Goal, ID: s.ID, Name: s.Name,
		Phase: phaseOf(s, today), Start: sprintDay(s.StartDate)}
}

// boardSprint — Active sprint и доска, с которой его показываем.
type boardSprint struct {
	board  Board
	sprint Sprint
}

// activeSprints — Active sprint'ы scrum-досок (project != "" — только досок проекта) без повторов:
// спринт, видный на нескольких досках, — один раз, с доской, где его создали.
// Порядок: сначала текущие, внутри — по началу, новые выше; без дат — в конце.
// Доска, на которой Jira ответила ошибкой, пропускается с предупреждением; ошибка — если так со всеми.
func activeSprints(c *Client, project, today string) ([]boardSprint, []string, error) {
	boards, err := c.scrumBoards(project)
	if err != nil {
		return nil, nil, err
	}
	var found []boardSprint
	warnings := []string{}
	index := map[int]int{} // id спринта → индекс в found
	failed := 0
	var lastErr error
	for _, b := range boards {
		sprints, err := c.boardActiveSprints(b.ID)
		if err != nil {
			boardLabel := fmt.Sprintf("доска %d (%s)", b.ID, oneLine(b.Name))
			failed++
			lastErr = fmt.Errorf("%s: %w", boardLabel, err)
			warnings = append(warnings, fmt.Sprintf("%s пропущена: %v", boardLabel, err))
			continue
		}
		for _, s := range sprints {
			bs := boardSprint{board: b, sprint: s}
			if i, seen := index[s.ID]; seen {
				if s.OriginBoardID == b.ID {
					found[i] = bs
				}
				continue
			}
			index[s.ID] = len(found)
			found = append(found, bs)
		}
	}
	if len(boards) > 0 && failed == len(boards) {
		return nil, nil, lastErr
	}
	type sortKey struct {
		current, dated bool
		start          time.Time
	}
	keys := map[int]sortKey{} // id спринта → ключ сортировки, считается один раз
	for _, bs := range found {
		start, dated := parseSprintTime(bs.sprint.StartDate)
		keys[bs.sprint.ID] = sortKey{phaseOf(bs.sprint, today) == phaseCurrent, dated, start}
	}
	sort.SliceStable(found, func(i, j int) bool {
		ki, kj := keys[found[i].sprint.ID], keys[found[j].sprint.ID]
		if ki.current != kj.current {
			return ki.current
		}
		if ki.dated != kj.dated {
			return ki.dated
		}
		return ki.start.After(kj.start)
	})
	return found, warnings, nil
}

// cmdSprint читает сегодняшнюю дату один раз, на границе команды, — дальше она идёт параметром.
func cmdSprint(c *Client, a Args) (result, error) {
	today := localToday()
	if len(a.pos) == 0 {
		return cmdSprintList(c, a, today)
	}
	return cmdSprintIssues(c, a, today)
}

// ---------- jira sprint: список Active sprint ----------

type sprintRow struct {
	Board   string `json:"board"`
	BoardID int    `json:"board_id"`
	sprintInfo
}

// sprintList — результат jira sprint без аргумента.
type sprintList struct {
	Sprints  []sprintRow `json:"sprints"`
	Warnings []string    `json:"warnings"`
}

func (l *sprintList) text(w io.Writer) {
	for _, r := range l.Sprints {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Start, r.End, r.Phase.text(), oneLine(r.Board), oneLine(r.Name))
	}
	if len(l.Sprints) == 0 {
		fmt.Fprintln(w, "# нет активных спринтов")
	}
	writeWarnings(w, l.Warnings)
}

func cmdSprintList(c *Client, a Args, today string) (result, error) {
	for _, f := range []string{"user", "open", "n"} {
		if a.has(f) {
			return nil, fmt.Errorf("%s: только для задач спринта — jira sprint now|active|ID %s", flagName(f), flagName(f))
		}
	}
	found, warnings, err := activeSprints(c, a.get("project"), today)
	if err != nil {
		return nil, err
	}
	l := &sprintList{Sprints: []sprintRow{}, Warnings: warnings}
	for _, bs := range found {
		l.Sprints = append(l.Sprints, sprintRow{Board: bs.board.Name, BoardID: bs.board.ID, sprintInfo: newSprintInfo(bs.sprint, today)})
	}
	return l, nil
}

// ---------- jira sprint now|active|ID: задачи спринтов ----------

// sprintWithIssues — спринт (поля как в sprintInfo) и его задачи. Поля перечислены, а не встроены:
// встроенный sprintInfo нарушил бы алфавитный порядок ключей JSON.
type sprintWithIssues struct {
	End    string      `json:"end"`
	Goal   string      `json:"goal"`
	ID     int         `json:"id"`
	Issues []issueRow  `json:"issues"`
	Name   string      `json:"name"`
	Phase  sprintPhase `json:"phase"`
	Start  string      `json:"start"`
	Total  int         `json:"total"`
}

// newSprintWithIssues — спринт i и его задачи l в одном объекте.
func newSprintWithIssues(i sprintInfo, l *issueList) sprintWithIssues {
	return sprintWithIssues{End: i.End, Goal: i.Goal, ID: i.ID, Issues: l.Issues, Name: i.Name,
		Phase: i.Phase, Start: i.Start, Total: l.Total}
}

// sprintIssueList — результат jira sprint now|active|ID: спринты, у каждого — его задачи.
type sprintIssueList struct {
	Sprints  []sprintWithIssues `json:"sprints"`
	Warnings []string           `json:"warnings"`
}

func (r *sprintIssueList) text(w io.Writer) {
	for _, s := range r.Sprints {
		fmt.Fprintf(w, "# %d %s · %s – %s · %s\n", s.ID, oneLine(s.Name), s.Start, s.End, s.Phase.text())
		(&issueList{Issues: s.Issues, Total: s.Total}).text(w)
	}
	if len(r.Sprints) == 0 {
		fmt.Fprintln(w, "# нет активных спринтов")
	}
	writeWarnings(w, r.Warnings)
}

// targetKind — чьи задачи показывает jira sprint now|active|ID.
type targetKind int

const (
	targetCurrent targetKind = iota // now — Current sprint'ы
	targetActive                    // active — все Active sprint'ы
	targetID                        // ID — спринт по номеру
)

type sprintTarget struct {
	kind targetKind
	id   int // для targetID
}

func parseSprintTarget(arg string) (sprintTarget, error) {
	switch arg {
	case "now":
		return sprintTarget{kind: targetCurrent}, nil
	case "active":
		return sprintTarget{kind: targetActive}, nil
	}
	id, err := strconv.Atoi(arg)
	if err != nil || id < 1 {
		return sprintTarget{}, fmt.Errorf("спринт: ожидается now, active или номер спринта (номера — в jira sprint), а не %q", arg)
	}
	return sprintTarget{kind: targetID, id: id}, nil
}

func cmdSprintIssues(c *Client, a Args, today string) (result, error) {
	// всё, что проверяется без Jira, — до запросов
	target, err := parseSprintTarget(a.arg(0))
	if err != nil {
		return nil, err
	}
	n, err := limit(a)
	if err != nil {
		return nil, err
	}
	project := a.get("project")
	var filters []string
	if a.has("user") {
		who, err := assigneeJQL(c, a.get("user"))
		if err != nil {
			return nil, err
		}
		filters = append(filters, "assignee = "+who)
	}
	if a.has("open") {
		filters = append(filters, "statusCategory != Done")
	}
	if project != "" {
		filters = append(filters, "project = "+jqlQuote(project))
	}

	res := &sprintIssueList{Sprints: []sprintWithIssues{}, Warnings: []string{}}
	var sprints []Sprint
	if target.kind == targetID {
		s, err := c.sprint(target.id)
		if err != nil {
			return nil, err
		}
		sprints = []Sprint{*s}
	} else {
		found, warnings, err := activeSprints(c, project, today)
		if err != nil {
			return nil, err
		}
		res.Warnings = warnings
		for _, bs := range found {
			if target.kind == targetActive || phaseOf(bs.sprint, today) == phaseCurrent {
				sprints = append(sprints, bs.sprint)
			}
		}
		if len(sprints) == 0 && target.kind == targetCurrent {
			return nil, noCurrentSprint(found, warnings, project, today)
		}
	}

	for _, s := range sprints {
		jql := strings.Join(append([]string{fmt.Sprintf("sprint = %d", s.ID)}, filters...), " AND ") + " ORDER BY Rank ASC"
		l, err := listIssues(c, jql, n)
		if err != nil {
			return nil, fmt.Errorf("задачи спринта %d: %w", s.ID, err)
		}
		res.Sprints = append(res.Sprints, newSprintWithIssues(newSprintInfo(s, today), l))
	}
	return res, nil
}

// noCurrentSprint — ошибка sprint now без Current sprint: какие Active sprint есть, как открыть их задачи
// и какие доски пропущены — текущий спринт мог быть на них.
func noCurrentSprint(found []boardSprint, warnings []string, project, today string) error {
	where, flag := "", ""
	if project != "" {
		where, flag = " в проекте "+project, " --project "+project
	}
	var b strings.Builder
	if len(found) == 0 {
		b.WriteString("нет активных спринтов" + where)
	} else {
		fmt.Fprintf(&b, "нет текущего спринта%s (активного, идущего сегодня по датам); активные:", where)
		for i, bs := range found {
			if i > 0 {
				b.WriteString(",")
			}
			s := newSprintInfo(bs.sprint, today)
			fmt.Fprintf(&b, " %d «%s» (%s, %s – %s)", s.ID, oneLine(s.Name), s.Phase.text(), s.Start, s.End)
		}
		fmt.Fprintf(&b, " — задачи: jira sprint %d или все активные: jira sprint active%s", found[0].sprint.ID, flag)
	}
	for _, w := range warnings {
		b.WriteString("; " + w)
	}
	return errors.New(b.String())
}
