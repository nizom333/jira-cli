package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

const listFields = "summary,status,timetracking"

// meResult — результат me: JSON {name, displayName}.
type meResult User

func (u *meResult) text(w io.Writer) {
	fmt.Fprintf(w, "%s\t%s\n", oneLine(u.Name), oneLine(u.DisplayName))
}

func cmdMe(c *Client, a Args) (result, error) {
	u, err := c.myself()
	if err != nil {
		return nil, err
	}
	return (*meResult)(u), nil
}

func limit(a Args) (int, error) {
	if !a.has("n") {
		return 30, nil
	}
	n, err := strconv.Atoi(a.get("n"))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("-n: ожидается положительное число")
	}
	return n, nil
}

func jqlQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func cmdLs(c *Client, a Args) (result, error) {
	n, err := limit(a) // до запроса к /user
	if err != nil {
		return nil, err
	}
	who := "currentUser()" // свои задачи — без запроса к /myself
	if ref := a.get("user"); !isSelfRef(ref) {
		u, err := c.filterUser(ref)
		if err != nil {
			return nil, err
		}
		who = jqlQuote(u.Name)
	}
	parts := []string{"assignee = " + who}
	if !a.has("all") {
		parts = append(parts, "statusCategory != Done")
	}
	if p := a.get("project"); p != "" {
		parts = append(parts, "project = "+jqlQuote(p))
	}
	return listIssues(c, strings.Join(parts, " AND ")+" ORDER BY updated DESC", n)
}

func cmdQuery(c *Client, a Args) (result, error) {
	jql := strings.Join(a.pos, " ")
	if jql == "" {
		return nil, fmt.Errorf("пустой JQL")
	}
	n, err := limit(a)
	if err != nil {
		return nil, err
	}
	return listIssues(c, jql, n)
}

type issueRow struct {
	Key      string   `json:"key"`
	Status   string   `json:"status"`
	Estimate duration `json:"estimate_h"`
	Spent    duration `json:"spent_h"`
	Summary  string   `json:"summary"`
}

// issueList — результат ls и q.
type issueList struct {
	Issues []issueRow `json:"issues"`
	Total  int        `json:"total"`
}

func (l *issueList) text(w io.Writer) {
	for _, r := range l.Issues {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", oneLine(r.Key), oneLine(r.Status), r.Estimate, r.Spent, oneLine(r.Summary))
	}
	if l.Total > len(l.Issues) {
		fmt.Fprintf(w, "# показано %d из %d (-n)\n", len(l.Issues), l.Total)
	} else if l.Total == 0 {
		fmt.Fprintln(w, "# ничего не найдено")
	}
}

// listIssues — первые n задач по jql (n уже проверен вызывающим до любых запросов).
func listIssues(c *Client, jql string, n int) (result, error) {
	r, err := c.search(jql, listFields, 0, n)
	if err != nil {
		return nil, err
	}
	l := &issueList{Total: r.Total, Issues: make([]issueRow, 0, len(r.Issues))}
	for _, is := range r.Issues {
		f := is.Fields
		l.Issues = append(l.Issues, issueRow{Key: is.Key, Status: f.Status.Name, Summary: f.Summary,
			Estimate: f.TimeTracking.OriginalEstimate, Spent: f.TimeTracking.TimeSpent})
	}
	return l, nil
}

type showParent struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

type showComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
	Date   string `json:"date"`
}

// showResult — результат show.
type showResult struct {
	Assignee             string        `json:"assignee"`
	Comments             []showComment `json:"comments"` // последние --comments, [] если нет
	CommentsTotal        int           `json:"comments_total"`
	Created              string        `json:"created"`
	Description          string        `json:"description"`
	DescriptionTruncated bool          `json:"description_truncated"`
	Estimate             duration      `json:"estimate_h"`
	Key                  string        `json:"key"`
	Parent               *showParent   `json:"parent,omitempty"`
	Priority             string        `json:"priority"`
	Remaining            duration      `json:"remaining_h"`
	Reporter             string        `json:"reporter"`
	Resolved             string        `json:"resolved"` // "" — не закрыта
	Spent                duration      `json:"spent_h"`
	Status               string        `json:"status"`
	Summary              string        `json:"summary"`
	Type                 string        `json:"type"`
	Updated              string        `json:"updated"`
}

func (s *showResult) text(w io.Writer) {
	// Однострочные поля — через oneLine: \n в них не должен давать поддельную строку вывода.
	fmt.Fprintf(w, "%s [%s] %s · %s\n%s\n", oneLine(s.Key), oneLine(s.Type), oneLine(s.Status),
		oneLine(s.Priority), oneLine(s.Summary))
	fmt.Fprintf(w, "assignee: %s  reporter: %s\n", oneLine(s.Assignee), oneLine(s.Reporter))
	fmt.Fprintf(w, "estimate: %s  spent: %s  remaining: %s\n", s.Estimate, s.Spent, s.Remaining)
	fmt.Fprintf(w, "created: %s  updated: %s", s.Created, s.Updated)
	if s.Resolved != "" {
		fmt.Fprintf(w, "  resolved: %s", s.Resolved)
	}
	fmt.Fprintln(w)
	if s.Parent != nil {
		fmt.Fprintf(w, "parent: %s %s\n", oneLine(s.Parent.Key), oneLine(s.Parent.Summary))
	}
	if s.Description != "" {
		fmt.Fprintf(w, "\n%s\n", s.Description)
		if s.DescriptionTruncated {
			fmt.Fprintln(w, "… (обрезано, --full)")
		}
	}
	if len(s.Comments) > 0 {
		fmt.Fprintf(w, "\nкомментарии (%d из %d):\n", len(s.Comments), s.CommentsTotal)
		for _, cm := range s.Comments {
			fmt.Fprintf(w, "- %s %s: %s\n", oneLine(cm.Author), cm.Date, strings.TrimSpace(cm.Body))
		}
	}
}

// showFields — поля задачи, которые читает show.
const showFields = "summary,description,status,issuetype,priority,project,assignee,reporter,created,updated,resolutiondate,timetracking,parent,comment"

func cmdShow(c *Client, a Args) (result, error) {
	key := a.arg(0)
	nc := 3
	if a.has("comments") {
		v, err := strconv.Atoi(a.get("comments"))
		if err != nil || v < 0 {
			return nil, fmt.Errorf("--comments: ожидается число")
		}
		nc = v
	}
	is, err := c.getIssue(key, showFields)
	if err != nil {
		return nil, err
	}
	f := is.Fields
	cs := f.Comment.Comments
	if len(cs) > nc {
		cs = cs[len(cs)-nc:]
	}
	desc := strings.TrimSpace(f.Description)
	const maxDesc = 4000
	cut := false
	if !a.has("full") && len([]rune(desc)) > maxDesc {
		desc, cut = string([]rune(desc)[:maxDesc]), true
	}
	prio := "-"
	if f.Priority != nil {
		prio = f.Priority.Name
	}
	s := &showResult{
		Key: is.Key, Type: f.IssueType.Name, Status: f.Status.Name, Priority: prio,
		Summary: f.Summary, Assignee: userName(f.Assignee), Reporter: userName(f.Reporter),
		Estimate: f.TimeTracking.OriginalEstimate, Spent: f.TimeTracking.TimeSpent,
		Remaining: f.TimeTracking.RemainingEstimate,
		Created:   day(f.Created), Updated: day(f.Updated),
		Description: desc, DescriptionTruncated: cut, CommentsTotal: f.Comment.Total,
		Comments: make([]showComment, 0, len(cs)),
	}
	if f.ResolutionDate != "" {
		s.Resolved = day(f.ResolutionDate)
	}
	if f.Parent != nil {
		s.Parent = &showParent{Key: f.Parent.Key, Summary: f.Parent.Fields.Summary}
	}
	for _, cm := range cs {
		s.Comments = append(s.Comments, showComment{Author: cm.Author.Name, Date: day(cm.Created), Body: cm.Body})
	}
	return s, nil
}

func cmdCreate(c *Client, a Args) (result, error) {
	proj, summary := a.get("project"), strings.TrimSpace(a.get("summary"))
	if proj == "" || summary == "" {
		return nil, fmt.Errorf("нужны --project и --summary")
	}
	typ := a.get("type")
	if typ == "" {
		var err error
		if typ, _, err = defaultIssueType(); err != nil {
			return nil, err
		}
	}
	desc, err := textArg(a.get("desc"))
	if err != nil {
		return nil, err
	}
	est, estShown := "", "-"
	if a.has("estimate") {
		if est, err = parseDur(a.get("estimate")); err != nil {
			return nil, fmt.Errorf("--estimate: %w", err)
		}
		estShown = est
	}
	assignee, err := c.userRef(a.get("assignee"))
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	if !a.has("yes") {
		fmt.Fprintln(&b, "ПРЕВЬЮ — задача НЕ создана. Для создания повторите с --yes")
		fmt.Fprintf(&b, "project: %s\ntype: %s\nsummary: %s\nassignee: %s\nestimate: %s\n",
			proj, typ, summary, userLabel(assignee), estShown)
		if desc != "" {
			fmt.Fprintf(&b, "description:\n%s\n", desc)
		}
		return message(b.String()), nil
	}

	newKey, err := c.createIssue(issueDraft{Project: proj, Type: typ, Summary: summary, Description: desc, Assignee: assignee})
	if err != nil {
		return nil, err
	}
	key := oneLine(newKey)
	fmt.Fprintf(&b, "created %s %s/browse/%s\n", key, c.base, key)
	// Оценку ставим отдельным запросом: поле timetracking может отсутствовать на экране создания.
	if est != "" {
		if err := setEstimate(c, newKey, est); err != nil {
			return message(b.String()), fmt.Errorf("задача создана, но оценка не поставлена: %w", err)
		}
		fmt.Fprintf(&b, "%s: оценка %s\n", key, est)
	}
	return message(b.String()), nil
}

// assignPreviewFields — поля задачи для превью assign.
const assignPreviewFields = "assignee,summary"

func cmdAssign(c *Client, a Args) (result, error) {
	key, who := a.arg(0), a.arg(1)
	u, err := c.userRef(who)
	if err != nil {
		return nil, err
	}
	label := userLabel(u)
	if !a.has("yes") {
		is, err := c.getIssue(key, assignPreviewFields)
		if err != nil {
			return nil, err
		}
		return message(fmt.Sprintf("ПРЕВЬЮ — исполнитель НЕ изменён. Для применения повторите с --yes\n"+
			"%s %s\nassignee: %s → %s\n", oneLine(is.Key), oneLine(is.Fields.Summary),
			oneLine(userName(is.Fields.Assignee)), label)), nil
	}
	if err := c.setAssignee(key, u); err != nil {
		return nil, err
	}
	return message(fmt.Sprintf("%s: assignee → %s\n", key, label)), nil
}

type transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name string `json:"name"`
	} `json:"to"`
}

// matchTransition ищет переход по имени перехода или целевого статуса (без учёта регистра):
// сначала точное совпадение, затем подстрока. Возвращает всех кандидатов уровня, на котором нашлось.
func matchTransition(ts []transition, want string) []transition {
	w := strings.ToLower(strings.TrimSpace(want))
	var exact, partial []transition
	for _, t := range ts {
		n, s := strings.ToLower(t.Name), strings.ToLower(t.To.Name)
		switch {
		case n == w || s == w:
			exact = append(exact, t)
		case strings.Contains(n, w) || strings.Contains(s, w):
			partial = append(partial, t)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func cmdMove(c *Client, a Args) (result, error) {
	key := a.arg(0)
	want := strings.Join(a.pos[1:], " ")
	ts, err := c.transitions(key)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	list := func() {
		for _, t := range ts {
			fmt.Fprintf(&b, "%s\t→ %s\n", oneLine(t.Name), oneLine(t.To.Name))
		}
	}
	if want == "" {
		list()
		return message(b.String()), nil
	}
	m := matchTransition(ts, want)
	if len(m) != 1 {
		fmt.Fprintln(&b, "доступные переходы (переход → статус):")
		list()
		if len(m) == 0 {
			return message(b.String()), fmt.Errorf("нет перехода %q", want)
		}
		return message(b.String()), fmt.Errorf("неоднозначно %q: уточните", want)
	}
	if err := c.applyTransition(key, m[0].ID); err != nil {
		return nil, err
	}
	return message(fmt.Sprintf("%s → %s\n", key, oneLine(m[0].To.Name))), nil
}

func cmdComment(c *Client, a Args) (result, error) {
	key := a.arg(0)
	text, err := textArg(strings.Join(a.pos[1:], " "))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("пустой комментарий")
	}
	if err := c.addComment(key, text); err != nil {
		return nil, err
	}
	return message(fmt.Sprintf("%s: комментарий добавлен\n", key)), nil
}
