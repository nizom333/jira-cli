package main

// Time-tracking module: разбор, валидация и рендер длительностей, установка Original Estimate.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ---------- ввод длительности ----------

// durWord — одно слово длительности: один или несколько токенов <целое><w|d|h|m> слитно ("1d4h").
// Регистр единиц не важен.
var (
	durWord = regexp.MustCompile(`(?i)^(\d+[wdhm])+$`)
	durTok  = regexp.MustCompile(`(?i)(\d+)([wdhm])`)
)

const durHint = "ожидается вида 1d 4h, 2h30m (единицы w d h m)"

// isDurWord: слово целиком состоит из токенов длительности.
func isDurWord(w string) bool { return durWord.MatchString(w) }

// parseDur разбирает длительность из слов ("1d4h", "1d 4h", "2H 30M") и нормализует
// в формат Jira "1d 4h"; нулевые токены отбрасываются ("1d 0h" → "1d").
// Невалидный или целиком нулевой ввод — ошибка.
func parseDur(words ...string) (string, error) {
	fields := strings.Fields(strings.Join(words, " "))
	if len(fields) == 0 {
		return "", fmt.Errorf("длительность не указана: %s", durHint)
	}
	var toks []string
	for _, w := range fields {
		if !isDurWord(w) {
			return "", fmt.Errorf("длительность %q: %s", strings.ToLower(w), durHint)
		}
		for _, m := range durTok.FindAllStringSubmatch(w, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return "", fmt.Errorf("длительность %q: %s", strings.ToLower(w), durHint)
			}
			if n > 0 {
				toks = append(toks, strconv.Itoa(n)+strings.ToLower(m[2]))
			}
		}
	}
	if len(toks) == 0 {
		return "", fmt.Errorf("длительность должна быть больше нуля")
	}
	return strings.Join(toks, " "), nil
}

// splitDur отделяет ведущие аргументы-длительности от остальных (комментария).
// Аргумент считается длительностью, если все его слова — токены длительности.
func splitDur(args []string) (dur, rest []string) {
	i := 0
	for ; i < len(args); i++ {
		fs := strings.Fields(args[i])
		if len(fs) == 0 {
			break
		}
		ok := true
		for _, f := range fs {
			if !isDurWord(f) {
				ok = false
				break
			}
		}
		if !ok {
			break
		}
	}
	return args[:i], args[i:]
}

// ---------- установка Original Estimate ----------

// setEstimate ставит Original Estimate. Если поля timetracking нет на экране редактирования
// (так бывают настроены проекты), ставит через Agile API доски проекта —
// так же, как это делает UI доски.
func setEstimate(c *Client, key, est string) error {
	err := c.setOriginalEstimate(key, est)
	if !timetrackingNotOnScreen(err) {
		return err
	}
	board, berr := estimationBoard(c, strings.SplitN(key, "-", 2)[0])
	if berr != nil {
		return fmt.Errorf("%v; через доску тоже нельзя: %w", err, berr)
	}
	return c.setBoardEstimate(key, board, est)
}

// timetrackingNotOnScreen — edit задачи отклонён (400) именно из-за поля timetracking.
func timetrackingNotOnScreen(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusBadRequest && ae.HasFieldError("timetracking")
}

// estimationBoard находит доску проекта, у которой оценка = timeoriginalestimate.
func estimationBoard(c *Client, project string) (int, error) {
	boards, err := c.projectBoards(project)
	if err != nil {
		return 0, err
	}
	for _, b := range boards {
		if f, err := c.boardEstimationField(b); err == nil && f == "timeoriginalestimate" {
			return b, nil
		}
	}
	return 0, fmt.Errorf("у проекта %s нет доски с оценкой по Original Estimate", project)
}

// ---------- рендер ----------
// Дни не показываем — 1d = 8h задаётся настройкой Jira, а не CLI.

// duration — длительность учёта времени в секундах. Из Jira читается как есть (целое число
// секунд); выводится в тексте часами и минутами (String), в JSON — часами числом (MarshalJSON).
// Тип асимметричен: структуры ответа Jira (Issue, Worklog, TimeTracking) не сериализовать —
// под ключами *Seconds окажутся часы. Тела запросов к Jira передают длительность строкой ("1d 4h").
type duration int

// String — "16h", "2h30m", "30m"; "-" для пустого/нуля.
func (d duration) String() string {
	if d <= 0 {
		return "-"
	}
	h, m := d/3600, (d%3600)/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", m)
}

// MarshalJSON — часы числом, две цифры после запятой с отбрасыванием; 0 для пустого.
func (d duration) MarshalJSON() ([]byte, error) {
	h := 0.0
	if d > 0 {
		h = float64(d*100/3600) / 100
	}
	return json.Marshal(h)
}
