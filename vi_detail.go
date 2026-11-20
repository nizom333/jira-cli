package main

// Подробности задачи в jira vi: подробный вид (Enter) и панель справа от списка или канбана (i).
// Шапка рисуется сразу из того, что известно по списку (ключ, название, статус, исполнитель, время);
// тип, приоритет, автор, даты, описание и комментарии — после загрузки (loadShow).
// Загрузки не занимают busy: действия над задачей во время них доступны. Загруженное хранится
// в details; после действия над задачей или перезагрузки списка оно помечается устаревшим
// и показывается, пока грузится новое.
// Описание и комментарии — в разметке Jira (wiki markup): заголовки, списки, цитаты, код, таблицы
// и ссылки показываются по строкам; стили внутри строки не переносятся — только чистый текст.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	viComments   = 20  // сколько последних комментариев загружать
	viDetailMaxW = 100 // шире текст подробного вида не растягивается: длинные строки трудно читать
	viLabelW     = 11  // колонка названий полей
	viListMinW   = 56  // уже — список не делит окно с панелью
)

// viDet — подробности задачи: загруженные или ошибка загрузки.
type viDet struct {
	show  *showResult
	err   error
	stale bool // задача менялась: показывается это, пока грузится новое
}

// ---------- загрузка ----------

// wantDetail — чьи подробности нужны экрану: открытой задачи или выбранной (панель справа).
func (m *viModel) wantDetail() string {
	switch {
	case m.screen == scrDetail:
		return m.detailKey
	case m.screen == scrMain && m.previewW(m.w) > 0:
		return m.sel
	}
	return ""
}

func (m *viModel) needDetail(k string) bool {
	d := m.details[k]
	return d == nil || d.stale
}

// prefetch загружает подробности, нужные экрану. Для панели — после паузы в листании: пока выбор
// бежит по списку, запросов нет. Одновременно — одна загрузка; следующая — после её ответа.
func (m *viModel) prefetch() cmd {
	k := m.wantDetail()
	if k == "" || !m.needDetail(k) || m.fetching != "" {
		return nil
	}
	if m.screen == scrDetail || m.previewDelay <= 0 {
		m.waiting = ""
		return m.fetchDetail(k)
	}
	if m.waiting == k {
		return nil
	}
	m.waiting = k
	d := m.previewDelay
	return func() msg {
		time.Sleep(d)
		return viWait{key: k}
	}
}

func (m *viModel) fetchDetail(k string) cmd {
	c, gen := m.c, m.detailGen
	m.fetching = k
	return func() msg {
		s, err := loadShow(c, k, viComments, true)
		return viDetail{key: k, gen: gen, show: s, err: err}
	}
}

// invalidate помечает подробности задачи k устаревшими ("" — всех задач): экран перезагрузит их,
// а ответы на запросы, начатые раньше, не попадут в кэш.
func (m *viModel) invalidate(k string) {
	m.detailGen++
	for key, d := range m.details {
		if k == "" || key == k {
			d.stale = true
		}
	}
}

// ---------- панель справа ----------

// previewW — ширина панели справа в окне шириной w; 0 — панель выключена или не помещается
// (список должен остаться не уже viListMinW, канбан — с тремя колонками).
func (m *viModel) previewW(w int) int {
	if !m.preview {
		return 0
	}
	pw := clamp(w*2/5, 36, 72)
	need := viListMinW
	if m.mode == viBoard {
		need = 3*viMinColW + 2
	}
	if w-pw-1 < need {
		return 0
	}
	return pw
}

// previewMinW — самое узкое окно, где панель помещается в текущем виде.
func (m *viModel) previewMinW() int {
	on := *m
	on.preview = true
	for w := viMinW; w < 1000; w++ {
		if on.previewW(w) > 0 {
			return w
		}
	}
	return 0
}

func (m *viModel) togglePreview() {
	m.preview = !m.preview
	m.st.HidePreview = !m.preview
	switch {
	case !m.preview:
		m.flash("панель справа скрыта — i: показать")
	case m.previewW(m.w) == 0:
		m.flash(fmt.Sprintf("панель справа включена, но окно узкое: нужно хотя бы %d символов в ширину", m.previewMinW()))
	default:
		m.flash("панель справа — i: скрыть")
	}
}

// viewPreview — панель справа: подробности выбранной задачи; не помещающееся — в подробном виде.
func (m *viModel) viewPreview(w, h int) []string {
	if m.sel == "" {
		return fill(nil, w, h)
	}
	lines := m.detailLines(m.sel, w, false)
	if len(lines) > h {
		lines = append(lines[:h-1], paint(styleDim, fit(" ↓ дальше — Enter", w)))
	}
	return fill(lines, w, h)
}

// sideBySide ставит рядом left (строки шириной lw) и right через вертикальную черту.
func sideBySide(left []string, lw int, right []string, h int) []string {
	left = fill(left, lw, h)
	sep := paint(styleDim, "│")
	out := make([]string, h)
	for i := range out {
		out[i] = left[i] + sep + right[i]
	}
	return out
}

// ---------- подробности ----------

func (m *viModel) issue(k string) (viIssue, bool) {
	for _, is := range m.issues {
		if is.Key == k {
			return is, true
		}
	}
	return viIssue{}, false
}

// seg — текст в стиле style ровно шириной w.
func seg(style, s string, w int) string { return paint(style, fit(s, w)) }

// detailLines — подробности задачи k: строки ровно шириной w. В подробном виде (full) текст
// не шире viDetailMaxW и в конце — ссылка на задачу.
func (m *viModel) detailLines(k string, w int, full bool) []string {
	iw := w - 2 // по пробелу с краёв
	if full {
		iw = min(iw, viDetailMaxW)
	}
	var out []string
	row := func(parts ...string) {
		out = append(out, " "+strings.Join(parts, "")+strings.Repeat(" ", w-1-iw))
	}
	blank := func() { out = append(out, strings.Repeat(" ", w)) }
	field := func(label, value, style string) {
		vw := iw - viLabelW
		for i, l := range wrap(value, vw) {
			if i > 0 {
				label = ""
			}
			row(seg(styleDim, label, viLabelW), seg(style, l, vw))
		}
	}
	section := func(title string) {
		t := truncate(clean(" "+title+" "), iw-2)
		row(paint(styleDim, "──"), paint(styleBold, t), paint(styleDim, strings.Repeat("─", max(0, iw-2-textWidth(t)))))
	}

	is, _ := m.issue(k)
	is.Key = k
	var s *showResult
	d := m.details[k]
	if d != nil {
		s = d.show
	}
	if s != nil {
		is.Summary, is.Status, is.Estimate, is.Spent = s.Summary, s.Status, s.Estimate, s.Spent
		is.Assignee = strings.TrimPrefix(s.Assignee, "-")
	}

	// ключ, тип и приоритет; название
	meta := ""
	if s != nil {
		meta = s.Type
		if s.Priority != "" && s.Priority != "-" {
			meta += " · " + s.Priority
		}
	}
	kw := min(textWidth(clean(k)), iw)
	row(seg(styleBold+color("36"), k, kw), seg(styleDim, "  "+meta, iw-kw))
	for _, l := range wrap(is.Summary, iw) {
		row(seg(styleBold, l, iw))
	}
	blank()

	// статус и исполнитель
	badge := " " + truncate(clean(is.Status), iw/2) + " "
	bw := textWidth(badge)
	who, whoStyle := "→ "+is.Assignee, ""
	if is.Assignee == "" {
		who, whoStyle = "не назначена", styleDim
	}
	row(paint(styleReverse+styleBold+catColor(is.Category), badge), "  ", seg(whoStyle, who, iw-bw-2))
	blank()

	// поля
	if s != nil && s.Reporter != "" && s.Reporter != "-" {
		field("Автор", s.Reporter, "")
	}
	m.timeField(row, is, s, iw)
	if s != nil {
		dates := "создана " + s.Created
		if s.Updated != "" && s.Updated != s.Created {
			dates += " · обновлена " + s.Updated
		}
		if s.Resolved != "" {
			dates += " · закрыта " + s.Resolved
		}
		field("Даты", dates, "")
		if s.Parent != nil {
			field("Родитель", s.Parent.Key+" "+s.Parent.Summary, "")
		}
	}

	switch {
	case s == nil && d != nil && d.err != nil:
		blank()
		for _, l := range wrap("не загрузилось: "+d.err.Error(), iw) {
			row(seg(color("31"), l, iw))
		}
		row(seg(styleDim, "r — повторить", iw))
		return out
	case s == nil:
		blank()
		row(seg(styleDim, "загрузка…", iw))
		return out
	}

	blank()
	section("Описание")
	if strings.TrimSpace(s.Description) == "" {
		row(seg(styleDim, "нет описания", iw))
	} else {
		for _, l := range renderMarkup(parseMarkup(s.Description), iw) {
			row(l)
		}
		if s.DescriptionTruncated {
			row(seg(styleDim, "… описание обрезано", iw))
		}
	}

	if len(s.Comments) > 0 {
		blank()
		title := fmt.Sprintf("Комментарии · %d", s.CommentsTotal)
		if s.CommentsTotal > len(s.Comments) {
			title = fmt.Sprintf("Комментарии · последние %d из %d", len(s.Comments), s.CommentsTotal)
		}
		section(title)
		for i, cm := range s.Comments {
			if i > 0 {
				blank()
			}
			aw := min(textWidth(clean(cm.Author)), iw)
			row(seg(styleBold+color("35"), cm.Author, aw), seg(styleDim, " · "+cm.Date, iw-aw))
			for _, l := range renderMarkup(parseMarkup(cm.Body), iw-2) {
				row("  ", l)
			}
		}
	}
	if full {
		blank()
		row(seg(styleDim, m.c.base+"/browse/"+k, iw))
	}
	return out
}

// timeField — строка «Время»: списано из оценки с полосой, или что из этого есть.
func (m *viModel) timeField(row func(...string), is viIssue, s *showResult, iw int) {
	est, spent := is.Estimate, is.Spent
	var parts []string
	switch {
	case est > 0:
		parts = append(parts, spent.String()+" из "+est.String())
		if spent <= 0 {
			parts[0] = "оценка " + est.String()
		}
	case spent > 0:
		parts = append(parts, "списано "+spent.String()+", без оценки")
	default:
		parts = append(parts, "без оценки")
	}
	if s != nil && s.Remaining > 0 && est > 0 && s.Remaining != est-spent {
		parts = append(parts, "осталось "+s.Remaining.String())
	}
	text := strings.Join(parts, " · ")
	vw := iw - viLabelW
	style := ""
	if est <= 0 && spent <= 0 {
		style = styleDim
	}
	tw := min(textWidth(clean(text)), vw)
	barW := min(16, vw-tw-7)
	if est <= 0 || barW < 4 {
		row(seg(styleDim, "Время", viLabelW), seg(style, text, vw))
		return
	}
	row(seg(styleDim, "Время", viLabelW), seg("", text, tw), "  ", progressBar(spent, est, barW), seg("", "", vw-tw-2-barW-5))
}

// progressBar — списано из оценки: полоса шириной w и процент (ещё 5 символов); перерасход — красным.
func progressBar(spent, est duration, w int) string {
	pct := int(spent) * 100 / int(est)
	filled := min(w, (int(spent)*w+int(est)/2)/int(est))
	c := color("32")
	if spent > est {
		c = color("31")
	}
	return paint(c, strings.Repeat("━", filled)) + paint(styleDim, strings.Repeat("─", w-filled)) +
		paint(c, fitRight(strconv.Itoa(pct)+"%", 5))
}

// ---------- разметка Jira ----------

// markupLine — строка описания или комментария после разбора разметки.
type markupLine struct {
	text      string // без разметки
	style     string
	mark      string // начало первой строки: «• », «1. », «│ »
	cont      string // начало строк переноса (той же ширины, что mark)
	markStyle string
	code      bool // код: строки не переносятся по словам, длинные обрезаются
	rule      bool // горизонтальная черта
}

var (
	reHeading = regexp.MustCompile(`^h([1-6])\.\s+(.*)$`)
	reList    = regexp.MustCompile(`^([*#-]+)\s+(.*)$`)
	reBlock   = regexp.MustCompile(`^\{(code|noformat)(:[^}]*)?\}(.*)$`)
	reLink    = regexp.MustCompile(`\[([^\[\]|]+)\|([^\[\]|]+)\]`)
	reBare    = regexp.MustCompile(`\[((?:https?|mailto|file):[^\[\]|]+)\]`)
	reMention = regexp.MustCompile(`\[~([^\[\]]+)\]`)
	reColor   = regexp.MustCompile(`\{color(:[^}]*)?\}`)
	reMono    = regexp.MustCompile(`\{\{(.+?)\}\}`)
	reImage   = regexp.MustCompile(`!([^!\s|]+\.[A-Za-z0-9]+)(\|[^!]*)?!`)
	reBold    = regexp.MustCompile(`(^|[\s(\[])\*(\S|\S[^*]*?\S)\*($|[\s.,;:!?)\]])`)
)

// inlineMarkup убирает разметку внутри строки: ссылки, упоминания, цвет, моноширинный и жирный текст.
func inlineMarkup(s string) string {
	s = reColor.ReplaceAllString(s, "")
	s = reMention.ReplaceAllString(s, "@$1")
	s = reLink.ReplaceAllStringFunc(s, func(l string) string {
		p := reLink.FindStringSubmatch(l)
		if strings.TrimSpace(p[1]) == strings.TrimSpace(p[2]) {
			return p[2]
		}
		return p[1] + " (" + p[2] + ")"
	})
	s = reBare.ReplaceAllString(s, "$1")
	s = reImage.ReplaceAllString(s, "[изображение $1]")
	s = reMono.ReplaceAllString(s, "$1")
	for range 2 { // соседние *a* *b*: пробел между ними — граница для обоих
		s = reBold.ReplaceAllString(s, "$1$2$3")
	}
	return s
}

// parseMarkup разбирает текст в разметке Jira по строкам; подряд идущие пустые строки — одна.
func parseMarkup(s string) []markupLine {
	var out []markupLine
	var nums []int // счётчики нумерованного списка по уровням
	closing := ""  // закрывающий тег блока кода, внутри которого мы
	quote := false // внутри {quote}
	codeLine := func(t string) markupLine {
		return markupLine{text: t, style: color("36"), mark: "│ ", cont: "│ ", markStyle: styleDim, code: true}
	}
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if closing != "" {
			if i := strings.Index(line, closing); i >= 0 {
				if strings.TrimSpace(line[:i]) != "" {
					out = append(out, codeLine(line[:i]))
				}
				closing = ""
				continue
			}
			out = append(out, codeLine(strings.ReplaceAll(line, "\t", "    ")))
			continue
		}
		t := strings.TrimSpace(line)
		if p := reBlock.FindStringSubmatch(t); p != nil {
			tag, rest := "{"+p[1]+"}", p[3]
			if i := strings.Index(rest, tag); i >= 0 { // {code}x{code} в одной строке
				out = append(out, codeLine(rest[:i]))
				continue
			}
			closing = tag
			if strings.TrimSpace(rest) != "" {
				out = append(out, codeLine(rest))
			}
			continue
		}
		if t == "{quote}" {
			quote = !quote
			continue
		}
		var ml markupLine
		list := reList.FindStringSubmatch(t)
		if list == nil {
			nums = nil // нумерация — заново в следующем списке
		}
		switch {
		case t == "":
		case t == "----":
			ml.rule = true
		case reHeading.MatchString(t):
			ml = markupLine{text: inlineMarkup(reHeading.FindStringSubmatch(t)[2]), style: styleBold}
		case strings.HasPrefix(t, "bq. "):
			ml = markupLine{text: inlineMarkup(t[4:]), mark: "│ ", cont: "│ ", markStyle: styleDim}
		case list != nil:
			marks := list[1]
			depth := len(marks)
			indent := strings.Repeat("  ", depth-1)
			if marks[depth-1] == '#' {
				for len(nums) < depth {
					nums = append(nums, 0)
				}
				nums = nums[:depth]
				nums[depth-1]++
				ml.mark = indent + strconv.Itoa(nums[depth-1]) + ". "
			} else {
				if len(nums) > depth-1 {
					nums = nums[:depth-1]
				}
				ml.mark = indent + "• "
			}
			ml.text, ml.cont, ml.markStyle = inlineMarkup(list[2]), strings.Repeat(" ", textWidth(ml.mark)), color("36")
		case strings.HasPrefix(t, "|"):
			ml = tableRow(t)
		default:
			ml.text = inlineMarkup(t)
		}
		if quote && !ml.rule {
			ml.mark, ml.cont, ml.markStyle = "│ "+ml.mark, "│ "+ml.cont, styleDim
		}
		if ml.text == "" && ml.mark == "" && !ml.rule && (len(out) == 0 || out[len(out)-1].blank()) {
			continue // пустые строки подряд и в начале — одна или ни одной
		}
		out = append(out, ml)
	}
	for len(out) > 0 && out[len(out)-1].blank() {
		out = out[:len(out)-1]
	}
	return out
}

func (l markupLine) blank() bool { return l.text == "" && l.mark == "" && !l.rule && !l.code }

// tableRow — строка таблицы: ||заголовок||…|| жирным, |ячейка|…| — через «│».
func tableRow(t string) markupLine {
	head := strings.HasPrefix(t, "||")
	t = inlineMarkup(t) // ссылки [a|b] — до деления на ячейки по «|»
	sep := "|"
	if head {
		sep = "||"
	}
	var cells []string
	for _, c := range strings.Split(strings.Trim(t, "|"), sep) {
		cells = append(cells, strings.TrimSpace(c))
	}
	ml := markupLine{text: strings.Join(cells, " │ ")}
	if head {
		ml.style = styleBold
	}
	return ml
}

// renderMarkup — строки разметки ровно шириной w: перенос по словам с отступом маркера.
func renderMarkup(lines []markupLine, w int) []string {
	var out []string
	for _, l := range lines {
		mw := min(textWidth(clean(l.mark)), w/2)
		switch {
		case l.rule:
			out = append(out, paint(styleDim, strings.Repeat("─", w)))
		case l.blank():
			out = append(out, strings.Repeat(" ", w))
		case l.code:
			out = append(out, seg(l.markStyle, l.mark, mw)+seg(l.style, l.text, w-mw))
		default:
			for i, part := range wrap(l.text, w-mw) {
				mark := l.mark
				if i > 0 {
					mark = l.cont
				}
				out = append(out, seg(l.markStyle, mark, mw)+seg(l.style, part, w-mw))
			}
		}
	}
	return out
}
