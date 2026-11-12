// jira — минимальный CLI к Jira Data Center для людей и для Claude.
// Вывод по умолчанию компактный (TSV / короткий текст), чтобы экономить токены.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strings"
)

const usage = `jira — CLI к Jira Data Center

  jira login [URL]                   первая настройка: адрес Jira и PAT (токен — в системное хранилище)
  jira logout                        удалить токен из системного хранилища
  jira url [URL]                     показать адрес Jira и откуда он взят; с URL — сохранить
  jira type [ТИП]                    тип задачи для create без --type (изначально Task); с ТИП — сохранить
  jira me                            проверить токен
  jira version                       версия
  jira ls [--all] [--user U] [--project P] [-n 30]
                                     мои задачи (по умолчанию незакрытые)
  jira q 'JQL' [-n 30]               поиск
  jira show KEY [--comments 3] [--full]
  jira create --project P --summary S [--type T] [--desc D|-]
              [--estimate 4h] [--assignee U|me|none] [--yes]
  jira assign KEY U|me|none [--yes]
  jira move KEY [статус]             без статуса — список переходов
  jira comment KEY текст|-
  jira log KEY 1d 4h [комментарий] [--date YYYY-MM-DD]
  jira estimate KEY 1d 4h            длительность: 1d 4h, 1d4h, 2h30m (w d h m)
  jira report [YYYY-MM] [--project P] [--user U] [--by day]
                                     месяц по локальному времени; --by — только day;
                                     в report --json всегда warnings ([] если нет).

  --json   сжатый JSON вместо текста — только me, ls, q, show, report
           (команды записи --json не принимают: «неизвестный флаг»).
           show --json: + resolved, parent {key, summary}, description_truncated, comments ([] если нет).
  Ошибка до запроса к Jira: лишний или пустой аргумент, неизвестный или повторённый флаг,
           флаг без нужного значения, пустое значение флага или значение не того формата
           (--assignee "", -n 0, --date 2026-13-01), значение у флага-переключателя (--yes=false).
  Текст, начинающийся с «-», — после --: jira comment KEY -- "-1 баг".
  create/assign без --yes только показывают превью и ничего не меняют;
           исполнитель в превью — username (Display Name), по умолчанию в create — me.
  Вывод ls/q: KEY<TAB>STATUS<TAB>ESTIMATE<TAB>SPENT<TAB>SUMMARY
  Время в выводе (ls, q, show, report) — часы и минуты: 8h, 2h30m, 30m; - = пусто (1d = 8h).
  В --json — числа в часах: estimate_h, spent_h (+ remaining_h в show), 0 = пусто.
  Переменные JIRA_URL и JIRA_TOKEN важнее сохранённых адреса и токена (CI, скрипты).
`

type User struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type Comment struct {
	Author  User   `json:"author"`
	Body    string `json:"body"`
	Created string `json:"created"`
}

type Worklog struct {
	Author    User     `json:"author"`
	Started   string   `json:"started"`
	TimeSpent duration `json:"timeSpentSeconds"`
}

// TimeTracking — учёт времени задачи (поле timetracking).
type TimeTracking struct {
	OriginalEstimate  duration `json:"originalEstimateSeconds"`
	RemainingEstimate duration `json:"remainingEstimateSeconds"`
	TimeSpent         duration `json:"timeSpentSeconds"`
}

type Issue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string `json:"summary"`
		Description string `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority *struct {
			Name string `json:"name"`
		} `json:"priority"`
		Project struct {
			Key string `json:"key"`
		} `json:"project"`
		Assignee             *User        `json:"assignee"`
		Reporter             *User        `json:"reporter"`
		Created              string       `json:"created"`
		Updated              string       `json:"updated"`
		ResolutionDate       string       `json:"resolutiondate"`
		TimeTracking         TimeTracking `json:"timetracking"`
		TimeOriginalEstimate duration     `json:"timeoriginalestimate"`
		Parent               *struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
			} `json:"fields"`
		} `json:"parent"`
		Comment struct {
			Total    int       `json:"total"`
			Comments []Comment `json:"comments"`
		} `json:"comment"`
		Worklog struct {
			Total    int       `json:"total"`
			Worklogs []Worklog `json:"worklogs"`
		} `json:"worklog"`
	} `json:"fields"`
}

// ---------- аргументы ----------

type Args struct {
	pos   []string
	flags map[string]string
}

// flagSpec — флаги команды: имя → нужно ли значение.
type flagSpec map[string]bool

// flags строит flagSpec: "n=" — флаг со значением, "all" — булев.
func flags(names ...string) flagSpec {
	s := flagSpec{}
	for _, n := range names {
		name, withValue := strings.CutSuffix(n, "=")
		s[name] = withValue
	}
	return s
}

// flagName — флаг для сообщений: -n, --user.
func flagName(name string) string {
	if len([]rune(name)) == 1 {
		return "-" + name
	}
	return "--" + name
}

// parseArgs разбирает позиционные аргументы и флаги spec в любом порядке: --k v, --k=v, -n 10,
// булевы флаги (число дефисов не важно). Значение флага — следующий токен как есть.
// Всё после "--" — позиционные; "-" — позиционный (stdin).
// Ошибка — на неизвестный флаг, булев флаг со значением, флаг без нужного значения,
// пустое значение флага ("" — не «по умолчанию») и повтор флага.
func parseArgs(in []string, spec flagSpec) (Args, error) {
	a := Args{flags: map[string]string{}}
	for i := 0; i < len(in); i++ {
		s := in[i]
		if s == "--" {
			a.pos = append(a.pos, in[i+1:]...)
			break
		}
		if len(s) < 2 || s[0] != '-' {
			a.pos = append(a.pos, s)
			continue
		}
		name, v, hasValue := strings.Cut(strings.TrimLeft(s, "-"), "=")
		withValue, known := spec[name]
		switch {
		case !known:
			return a, fmt.Errorf("неизвестный флаг %s (см. jira help; текст с «-» в начале — после --)", s)
		case a.has(name):
			return a, fmt.Errorf("%s: указан дважды", flagName(name))
		case !withValue && hasValue:
			return a, fmt.Errorf("%s: значение не принимает", flagName(name))
		case !withValue:
			v = "1"
		case !hasValue:
			if i+1 >= len(in) {
				return a, fmt.Errorf("%s: нужно значение", flagName(name))
			}
			i++
			v = in[i]
		}
		if withValue && isBlank(v) {
			return a, fmt.Errorf("%s: пустое значение", flagName(name))
		}
		a.flags[name] = v
	}
	return a, nil
}

func (a Args) get(k string) string { return a.flags[k] }
func (a Args) has(k string) bool   { _, ok := a.flags[k]; return ok }

func (a Args) arg(i int) string {
	if i < len(a.pos) {
		return a.pos[i]
	}
	return ""
}

// textArg: "-" означает читать из stdin.
func textArg(s string) (string, error) {
	if s != "-" {
		return s, nil
	}
	b, err := io.ReadAll(os.Stdin)
	return strings.TrimRight(string(b), "\n"), err
}

// ---------- вывод ----------

// result — результат команды. text пишет текстовый вывод; у команд с JSON результат — struct
// с json-тегами, и JSON — сериализация той же структуры. Печатает результат только render.
// Поля JSON в этих struct — по алфавиту ключей, как было при выводе map.
type result interface{ text(w io.Writer) }

// message — готовый текст (команды записи, превью, список переходов move, login, help).
type message string

func (m message) text(w io.Writer) { io.WriteString(w, string(m)) }

// render — единственное место вывода результата. Текст идёт через textWriter (чистка управляющих
// символов). JSON пишется в сырой w: stripControls испортил бы данные. encoding/json экранирует C0,
// но DEL и C1 (U+0080–U+009F) выводит сырыми — их экранирует escapeDELC1, так что в выводе нет
// управляющих символов, а декодируется он в те же строки.
func render(w io.Writer, res result, asJSON bool) error {
	if !asJSON {
		res.text(textWriter{w})
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		return err
	}
	_, err := io.WriteString(w, escapeDELC1(buf.String()))
	return err
}

// escapeDELC1 заменяет DEL и C1 на \u007f, \u0080…\u009f. Вне строк валидный JSON
// таких символов не содержит, поэтому замена по всему тексту безопасна.
func escapeDELC1(s string) string {
	if !strings.ContainsFunc(s, isDELC1) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if isDELC1(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isDELC1(r rune) bool { return r >= 0x7f && r <= 0x9f }

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

func dash(s string) string {
	s = strings.ReplaceAll(s, " ", "")
	if s == "" || s == "0m" {
		return "-"
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func userName(u *User) string {
	if u == nil {
		return "-"
	}
	return u.Name
}

func day(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return dash(ts)
}

// ---------- main ----------

// version задаётся при сборке релиза: -ldflags "-X main.version=1.2.3".
var version = ""

// buildVersion — версия релиза; у сборки через go install — версия модуля; иначе "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

func cmdVersion(_ *Client, _ Args) (result, error) {
	return message("jira " + buildVersion() + "\n"), nil
}

func main() {
	name, rest := "help", []string(nil) // без аргументов — usage
	if len(os.Args) > 1 {
		name, rest = os.Args[1], os.Args[2:]
	}
	if err := run(os.Stdout, name, rest, newClient); err != nil {
		fmt.Fprintln(os.Stderr, errorLine(err))
		os.Exit(1)
	}
}

// errorLine — ошибка для stderr: одна строка без управляющих символов
// (в ошибке бывает текст с сервера, его \n подделал бы строку вывода).
func errorLine(err error) string {
	return "error: " + oneLine(stripControls(err.Error()))
}

// command — команда CLI и её спецификация: flags — флаги (--json добавляется сам, если json);
// args — сколько позиционных аргументов; json — принимает --json (только команды чтения);
// needsClient — нужен клиент Jira.
type command struct {
	fn          func(c *Client, a Args) (result, error)
	flags       flagSpec
	args        arity
	json        bool
	needsClient bool
}

// arity — число позиционных аргументов: от min до max (max = many — без ограничения).
// Первые min аргументов обязательны и не могут быть пустыми: "" — не KEY и не me.
type arity struct{ min, max int }

const many = -1

// commands — единственный список команд, их флагов, позиционных аргументов и того, у каких есть JSON.
// Текст usage пишется вручную; TestUsageMatchesCommandSpec сверяет его с этой таблицей.
var commands = map[string]command{
	"login":    {fn: cmdLogin, args: arity{0, 1}},
	"logout":   {fn: cmdLogout},
	"url":      {fn: cmdURL, args: arity{0, 1}},
	"type":     {fn: cmdType, args: arity{0, 1}},
	"version":  {fn: cmdVersion},
	"me":       {fn: cmdMe, json: true, needsClient: true},
	"ls":       {fn: cmdLs, flags: flags("all", "user=", "project=", "n="), json: true, needsClient: true},
	"q":        {fn: cmdQuery, flags: flags("n="), args: arity{1, many}, json: true, needsClient: true},
	"show":     {fn: cmdShow, flags: flags("comments=", "full"), args: arity{1, 1}, json: true, needsClient: true},
	"report":   {fn: cmdReport, flags: flags("project=", "user=", "by="), args: arity{0, 1}, json: true, needsClient: true},
	"create":   {fn: cmdCreate, flags: flags("project=", "summary=", "type=", "desc=", "estimate=", "assignee=", "yes"), needsClient: true},
	"assign":   {fn: cmdAssign, flags: flags("yes"), args: arity{2, 2}, needsClient: true},
	"move":     {fn: cmdMove, args: arity{1, many}, needsClient: true},
	"comment":  {fn: cmdComment, args: arity{2, many}, needsClient: true},
	"log":      {fn: cmdLog, flags: flags("date="), args: arity{2, many}, needsClient: true},
	"estimate": {fn: cmdEstimate, args: arity{2, many}, needsClient: true},
}

// parse разбирает аргументы команды name по её спецификации и проверяет число позиционных.
func (cmd command) parse(name string, in []string) (Args, error) {
	spec := flagSpec{}
	for k, v := range cmd.flags {
		spec[k] = v
	}
	if cmd.json {
		spec["json"] = false
	}
	a, err := parseArgs(in, spec)
	if err != nil {
		return a, err
	}
	if len(a.pos) < cmd.args.min || slices.ContainsFunc(a.pos[:cmd.args.min], isBlank) {
		return a, fmt.Errorf("использование: jira %s", synopsis(name))
	}
	if hi := cmd.args.max; hi != many && len(a.pos) > hi {
		return a, fmt.Errorf("лишний аргумент %q (использование: jira %s)", a.pos[hi], synopsis(name))
	}
	return a, nil
}

// synopsis — строка использования команды из usage: "show KEY [--comments 3] [--full]".
// Берётся первая строка команды до пояснения (два пробела) и строки-продолжения, начинающиеся с "[".
func synopsis(name string) string {
	var parts []string
	for _, line := range strings.Split(usage, "\n") {
		t := strings.TrimSpace(line)
		if parts == nil {
			if t == "jira "+name || strings.HasPrefix(t, "jira "+name+" ") {
				syn, _, _ := strings.Cut(strings.TrimPrefix(t, "jira "), "  ")
				parts = append(parts, syn)
			}
			continue
		}
		if !strings.HasPrefix(t, "[") {
			break
		}
		syn, _, _ := strings.Cut(t, "  ")
		parts = append(parts, syn)
	}
	return strings.Join(parts, " ")
}

// run выполняет команду и печатает её результат в w; connect создаёт клиент Jira
// (в main — newClient, в тестах — фейк). Аргументы разбираются по спецификации команды до connect.
// Частичный успех: если команда вернула и результат, и ошибку, результат печатается, затем — ошибка.
func run(w io.Writer, name string, args []string, connect func() (*Client, error)) error {
	switch name {
	case "help", "-h", "--help":
		return render(w, message(usage), false)
	case "--version":
		name = "version"
	}
	cmd, ok := commands[name]
	if !ok {
		return fmt.Errorf("неизвестная команда %q (см. jira help)", name)
	}
	a, err := cmd.parse(name, args)
	if err != nil {
		return err
	}
	var c *Client
	if cmd.needsClient {
		if c, err = connect(); err != nil {
			return err
		}
	}
	res, err := cmd.fn(c, a)
	if res != nil {
		if rerr := render(w, res, a.has("json")); rerr != nil && err == nil {
			err = rerr
		}
	}
	return err
}
