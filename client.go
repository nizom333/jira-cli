package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

const keychainService = "jira-cli"

type Client struct {
	base  string
	token string
	http  *http.Client
	me    *User // кэш /myself на время одного запуска
}

// accountName — ключ записи в системном хранилище: хост Jira.
func accountName(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	return u.Host
}

var (
	errNoTokenStore  = errors.New("системное хранилище не поддерживается на этой ОС: используйте JIRA_TOKEN")
	errTokenNotFound = errors.New("токена в системном хранилище нет")
)

// credTarget — имя записи в Диспетчере учётных данных Windows.
func credTarget(base string) string { return keychainService + ":" + accountName(base) }

// loadToken: сначала JIRA_TOKEN, затем системное хранилище
// (macOS Keychain / Linux secret-tool / Диспетчер учётных данных Windows).
func loadToken(base string) (string, error) {
	if t := strings.TrimSpace(os.Getenv("JIRA_TOKEN")); t != "" {
		return t, nil
	}
	acct := accountName(base)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("security", "find-generic-password", "-s", keychainService, "-a", acct, "-w")
	case "linux":
		cmd = exec.Command("secret-tool", "lookup", "service", keychainService, "account", acct)
	case "windows":
		t, err := osLoadToken(credTarget(base))
		if err != nil && !errors.Is(err, errTokenNotFound) {
			return "", fmt.Errorf("чтение токена из Диспетчера учётных данных: %w", err)
		}
		if t = strings.TrimSpace(t); t == "" {
			return "", errors.New("нет токена: выполните `jira login` или задайте JIRA_TOKEN")
		}
		return t, nil
	default:
		return "", errors.New("нет токена: задайте переменную JIRA_TOKEN")
	}
	out, err := cmd.Output()
	t := strings.TrimSpace(string(out))
	if err != nil || t == "" {
		return "", errors.New("нет токена: выполните `jira login` или задайте JIRA_TOKEN")
	}
	return t, nil
}

func saveToken(base, token string) error {
	if runtime.GOOS == "windows" {
		if strings.ContainsAny(token, "\r\n") {
			return errors.New("токен не должен содержать перевод строки")
		}
		return osSaveToken(credTarget(base), token)
	}
	cmd, err := saveTokenCmd(runtime.GOOS, base, token)
	if err != nil {
		return err
	}
	return cmd.Run()
}

// maxKeychainSecret — `security` при чтении пароля из stdin молча обрезает его до 128 байт.
const maxKeychainSecret = 128

// saveTokenCmd собирает команду записи токена в системное хранилище.
// Токен передаётся только через stdin: аргументы процесса видны другим пользователям в `ps`.
func saveTokenCmd(goos, base, token string) (*exec.Cmd, error) {
	if strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("токен не должен содержать перевод строки")
	}
	acct := accountName(base)
	switch goos {
	case "darwin":
		if len(token) > maxKeychainSecret {
			return nil, fmt.Errorf("токен длиннее %d символов не поместится в Keychain: используйте JIRA_TOKEN", maxKeychainSecret)
		}
		// `-w` последним без значения: security читает пароль из stdin и просит повторить его,
		// поэтому токен подаётся дважды (иначе запишется пустой пароль с кодом 0).
		cmd := exec.Command("security", "add-generic-password", "-U",
			"-s", keychainService, "-a", acct, "-w")
		cmd.Stdin = strings.NewReader(token + "\n" + token + "\n")
		return cmd, nil
	case "linux":
		cmd := exec.Command("secret-tool", "store", "--label=jira-cli "+acct,
			"service", keychainService, "account", acct)
		cmd.Stdin = strings.NewReader(token)
		return cmd, nil
	}
	return nil, errNoTokenStore
}

func newClientWithToken(base, token string) *Client {
	return &Client{base: base, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

func newClient() (*Client, error) {
	base, err := baseURL()
	if err != nil {
		return nil, err
	}
	t, err := loadToken(base)
	if err != nil {
		return nil, err
	}
	return newClientWithToken(base, t), nil
}

// do выполняет запрос к REST API. body сериализуется в JSON, ответ — в out (если не nil).
func (c *Client) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("сеть: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// APIError — ответ Jira с HTTP-статусом >= 300. Структура (статус, ключи errors) сохраняется,
// чтобы решения принимались по ней, а не по тексту сообщения.
type APIError struct {
	Status        int               `json:"-"`
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"` // поле → сообщение
}

// Error сжимает ответ Jira об ошибке до одной строки.
func (e *APIError) Error() string {
	parts := append([]string{}, e.ErrorMessages...)
	keys := make([]string, 0, len(e.Errors))
	for k := range e.Errors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+": "+e.Errors[k])
	}
	msg := strings.Join(parts, "; ")
	switch {
	case e.Status == http.StatusUnauthorized:
		msg = "токен невалиден или истёк (jira login)"
	case msg == "":
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, msg)
}

// HasFieldError — Jira отклонила запрос с ошибкой по полю field.
func (e *APIError) HasFieldError(field string) bool {
	_, ok := e.Errors[field]
	return ok
}

func apiError(code int, data []byte) error {
	e := &APIError{Status: code}
	_ = json.Unmarshal(data, e)
	return e
}

func (c *Client) myself() (*User, error) {
	if c.me != nil {
		return c.me, nil
	}
	var u User
	if err := c.do("GET", "/rest/api/2/myself", nil, &u); err != nil {
		return nil, err
	}
	c.me = &u
	return &u, nil
}

// isSelfRef — единственное место, где решается, что ссылка на пользователя означает себя: "" и "me".
func isSelfRef(ref string) bool { return ref == "" || ref == "me" }

// userRef разрешает ссылку на пользователя: "none" → nil (без исполнителя),
// "" и "me" → текущий пользователь, иначе username проверяется через /user
// (несуществующий → «пользователь X не найден»; дальше используется канонический Name из ответа).
func (c *Client) userRef(ref string) (*User, error) {
	if ref == "none" {
		return nil, nil
	}
	if isSelfRef(ref) {
		return c.myself()
	}
	var u User
	if err := c.do("GET", "/rest/api/2/user?username="+url.QueryEscape(ref), nil, &u); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, fmt.Errorf("пользователь %s не найден", ref)
		}
		return nil, fmt.Errorf("пользователь %s: %w", ref, err)
	}
	return &u, nil
}

// filterUser — пользователь для фильтра --user в ls/report: как userRef, но "none" не допускается.
func (c *Client) filterUser(ref string) (*User, error) {
	if ref == "none" {
		return nil, errors.New("--user none: нет такого фильтра (без исполнителя — create --assignee none, assign KEY none)")
	}
	return c.userRef(ref)
}

// userLabel — пользователь для превью и итоговых сообщений: "username (Display Name)",
// "username" без отображаемого имени, или "none". Имена с сервера — в одну строку.
func userLabel(u *User) string {
	if u == nil {
		return "none"
	}
	if d := oneLine(u.DisplayName); d != "" {
		return oneLine(u.Name) + " (" + d + ")"
	}
	return oneLine(u.Name)
}

type searchResp struct {
	Total  int     `json:"total"`
	Issues []Issue `json:"issues"`
}

func (c *Client) search(jql, fields string, startAt, max int) (*searchResp, error) {
	q := url.Values{}
	q.Set("jql", jql)
	q.Set("fields", fields)
	q.Set("startAt", fmt.Sprint(startAt))
	q.Set("maxResults", fmt.Sprint(max))
	var r searchResp
	if err := c.do("GET", "/rest/api/2/search?"+q.Encode(), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) searchAll(jql, fields string) ([]Issue, error) {
	var all []Issue
	for {
		r, err := c.search(jql, fields, len(all), 100)
		if err != nil {
			return nil, err
		}
		all = append(all, r.Issues...)
		if len(r.Issues) == 0 || len(all) >= r.Total {
			return all, nil
		}
	}
}

// ---------- операции Jira ----------
// Пути REST, экранирование ключа и форма тел запросов живут только здесь;
// команды и модуль Time-tracking говорят операциями.

// issuePath — путь задачи key с суффиксом sub ("", "/comment", …).
func issuePath(key, sub string) string { return "/rest/api/2/issue/" + url.PathEscape(key) + sub }

// getIssue — задача с перечисленными через запятую полями.
func (c *Client) getIssue(key, fields string) (*Issue, error) {
	var is Issue
	if err := c.do("GET", issuePath(key, "")+"?fields="+fields, nil, &is); err != nil {
		return nil, err
	}
	return &is, nil
}

// issueDraft — поля новой задачи; Description и Assignee необязательны (nil — без исполнителя).
type issueDraft struct {
	Project, Type, Summary, Description string
	Assignee                            *User
}

// createIssue создаёт задачу и возвращает её ключ.
func (c *Client) createIssue(n issueDraft) (string, error) {
	fields := map[string]any{
		"project":   map[string]string{"key": n.Project},
		"issuetype": map[string]string{"name": n.Type},
		"summary":   n.Summary,
	}
	if n.Description != "" {
		fields["description"] = n.Description
	}
	if n.Assignee != nil {
		fields["assignee"] = map[string]string{"name": n.Assignee.Name}
	}
	var res struct {
		Key string `json:"key"`
	}
	if err := c.do("POST", "/rest/api/2/issue", map[string]any{"fields": fields}, &res); err != nil {
		return "", err
	}
	return res.Key, nil
}

// setAssignee назначает исполнителя; u == nil снимает его.
func (c *Client) setAssignee(key string, u *User) error {
	body := map[string]any{"name": nil}
	if u != nil {
		body["name"] = u.Name
	}
	return c.do("PUT", issuePath(key, "/assignee"), body, nil)
}

// transitions — переходы, доступные из текущего статуса задачи.
func (c *Client) transitions(key string) ([]transition, error) {
	var r struct {
		Transitions []transition `json:"transitions"`
	}
	if err := c.do("GET", issuePath(key, "/transitions"), nil, &r); err != nil {
		return nil, err
	}
	return r.Transitions, nil
}

// applyTransition выполняет переход с данным id.
func (c *Client) applyTransition(key, id string) error {
	return c.do("POST", issuePath(key, "/transitions"), map[string]any{"transition": map[string]string{"id": id}}, nil)
}

// addComment добавляет комментарий к задаче.
func (c *Client) addComment(key, body string) error {
	return c.do("POST", issuePath(key, "/comment"), map[string]string{"body": body}, nil)
}

// addWorklog списывает время spent (формат Jira: "1d 4h") с началом started; comment необязателен.
func (c *Client) addWorklog(key, spent string, started time.Time, comment string) error {
	body := map[string]any{"timeSpent": spent, "started": started.Format(jiraTime)}
	if comment != "" {
		body["comment"] = comment
	}
	return c.do("POST", issuePath(key, "/worklog"), body, nil)
}

// issueWorklogs — все worklog'и задачи (в поиске Jira отдаёт только первые 20).
func (c *Client) issueWorklogs(key string) ([]Worklog, error) {
	var r struct {
		Worklogs []Worklog `json:"worklogs"`
	}
	if err := c.do("GET", issuePath(key, "/worklog"), nil, &r); err != nil {
		return nil, err
	}
	return r.Worklogs, nil
}

// setOriginalEstimate ставит Original Estimate правкой задачи (поле timetracking).
func (c *Client) setOriginalEstimate(key, est string) error {
	body := map[string]any{"fields": map[string]any{"timetracking": map[string]string{"originalEstimate": est}}}
	return c.do("PUT", issuePath(key, ""), body, nil)
}

// projectBoards — id досок Agile проекта.
func (c *Client) projectBoards(project string) ([]int, error) {
	var r struct {
		Values []struct {
			ID int `json:"id"`
		} `json:"values"`
	}
	if err := c.do("GET", "/rest/agile/1.0/board?projectKeyOrId="+url.QueryEscape(project), nil, &r); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(r.Values))
	for _, v := range r.Values {
		ids = append(ids, v.ID)
	}
	return ids, nil
}

// boardEstimationField — поле, которым доска оценивает задачи (напр. timeoriginalestimate).
func (c *Client) boardEstimationField(board int) (string, error) {
	var cfg struct {
		Estimation struct {
			Field struct {
				FieldID string `json:"fieldId"`
			} `json:"field"`
		} `json:"estimation"`
	}
	if err := c.do("GET", fmt.Sprintf("/rest/agile/1.0/board/%d/configuration", board), nil, &cfg); err != nil {
		return "", err
	}
	return cfg.Estimation.Field.FieldID, nil
}

// setBoardEstimate ставит оценку задачи через Agile API доски — так же, как UI доски.
func (c *Client) setBoardEstimate(key string, board int, est string) error {
	return c.do("PUT", fmt.Sprintf("/rest/agile/1.0/issue/%s/estimation?boardId=%d", url.PathEscape(key), board),
		map[string]string{"value": est}, nil)
}

// Board — доска Agile.
type Board struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Sprint — спринт Agile; даты — ISO 8601 с сервера ("" у спринта без дат).
type Sprint struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"` // sprintFuture, sprintActive, sprintClosed
	StartDate     string `json:"startDate"`
	EndDate       string `json:"endDate"`
	Goal          string `json:"goal"`
	OriginBoardID int    `json:"originBoardId"`
}

// Состояния спринта в Jira.
const (
	sprintFuture = "future"
	sprintActive = "active"
	sprintClosed = "closed"
)

// agilePage — страница списка Agile API (isLast — последняя).
type agilePage[T any] struct {
	IsLast bool `json:"isLast"`
	Values []T  `json:"values"`
}

// agileAll собирает все страницы списка Agile API по пути path (с query или без).
func agileAll[T any](c *Client, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var all []T
	for {
		var p agilePage[T]
		if err := c.do("GET", fmt.Sprintf("%s%sstartAt=%d&maxResults=50", path, sep, len(all)), nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Values...)
		if p.IsLast || len(p.Values) == 0 {
			return all, nil
		}
	}
}

// scrumBoards — scrum-доски (у kanban спринтов нет); project != "" — только доски проекта.
func (c *Client) scrumBoards(project string) ([]Board, error) {
	q := url.Values{"type": {"scrum"}}
	if project != "" {
		q.Set("projectKeyOrId", project)
	}
	return agileAll[Board](c, "/rest/agile/1.0/board?"+q.Encode())
}

// boardActiveSprints — активные спринты доски.
func (c *Client) boardActiveSprints(board int) ([]Sprint, error) {
	return agileAll[Sprint](c, fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=%s", board, sprintActive))
}

// sprint — спринт по id; несуществующий → «спринт N не найден».
func (c *Client) sprint(id int) (*Sprint, error) {
	var s Sprint
	if err := c.do("GET", fmt.Sprintf("/rest/agile/1.0/sprint/%d", id), nil, &s); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, fmt.Errorf("спринт %d не найден", id)
		}
		return nil, err
	}
	return &s, nil
}
