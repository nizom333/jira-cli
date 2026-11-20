package main

// Отрисовка jira vi: кадр — шапка, тело (список, канбан, подробно или справка) и низ (выбор из списка
// или подтверждение, строка состояния, поле ввода, подсказка по клавишам). Каждая строка — ровно
// ширина окна; всё, что пришло с сервера, проходит через fit/clean (без управляющих символов).
// view поправляет прокрутку (listTop, boardLeft, colTop, detailTop) под размер окна.

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	viMinW    = 40
	viMinH    = 8
	viCardH   = 4  // карточка канбана: 3 строки и промежуток
	viMinColW = 24 // уже — колонки канбана прокручиваются вбок
)

func (m *viModel) view(w, h int) frame {
	if w < viMinW || h < viMinH {
		lines := fill(nil, w, h)
		if h > 0 {
			lines[0] = fit(fmt.Sprintf("окно мало: нужно хотя бы %d×%d (q — выход)", viMinW, viMinH), w)
		}
		return frame{lines: lines}
	}
	bottom, cur := m.viewBottom(w, h)
	bodyH := h - 1 - len(bottom)
	var body []string
	switch {
	case m.screen == scrHelp:
		body = viewHelp(w)
	case m.screen == scrDetail:
		body = m.viewDetail(w, bodyH)
	case m.mode == viBoard:
		body = m.viewBoard(w, bodyH)
	default:
		body = m.viewList(w, bodyH)
	}
	lines := append([]string{m.viewHeader(w)}, fill(body, w, bodyH)...)
	lines = append(lines, bottom...)
	if cur != nil {
		cur.row += 1 + bodyH
	}
	return frame{lines: lines, cursor: cur}
}

// fill обрезает строки до h или дополняет пустыми строками ширины w.
func fill(lines []string, w, h int) []string {
	if len(lines) > h {
		return lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines
}

// scrollTo — первая видимая строка окна из rows строк, при которой строка i видна.
func scrollTo(top, i, rows, n int) int {
	if i < top {
		top = i
	}
	if i >= top+rows {
		top = i - rows + 1
	}
	return clamp(top, 0, max(0, n-rows))
}

// catColor — цвет Status category: к выполнению — синий, в работе — жёлтый, готово — зелёный.
func catColor(c string) string {
	switch c {
	case categoryNew:
		return color("94")
	case categoryDone:
		return color("32")
	}
	return color("33")
}

// plural — форма слова для числа n: 1 задача, 2 задачи, 5 задач.
func plural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// ---------- шапка ----------

func (m *viModel) viewHeader(w int) string {
	left := " jira vi · " + m.scope.label() + " · " + m.countLabel()
	switch m.screen {
	case scrDetail:
		left = " jira vi · " + m.detailKey
	case scrHelp:
		left = " jira vi · клавиши"
	}
	const tabsW = 18
	if w < 60 || m.screen != scrMain {
		return paint(styleBold, fit(left, w))
	}
	tab := func(name string, on bool) string {
		if on {
			return paint(styleReverse+styleBold, " "+name+" ")
		}
		return paint(styleDim, " "+name+" ")
	}
	return paint(styleBold, fit(left, w-tabsW)) + tab("список", m.mode == viList) + " " + tab("канбан", m.mode == viBoard) + " "
}

func (m *viModel) countLabel() string {
	n := len(m.issues)
	s := fmt.Sprintf("%d %s", n, plural(n, "задача", "задачи", "задач"))
	if m.total > n {
		s = fmt.Sprintf("показано %d из %d (-n)", n, m.total)
	}
	if m.filter != "" {
		s += fmt.Sprintf(" · /%s: %d", m.filter, len(m.visible()))
	}
	return s
}

// ---------- список ----------

func (m *viModel) viewList(w, h int) []string {
	v := m.visible()
	if len(v) == 0 {
		return m.viewEmpty(w)
	}
	team := m.scope.team
	kw, sw, aw := 3, 6, 11
	for _, is := range v {
		kw = max(kw, textWidth(clean(is.Key)))
		sw = max(sw, textWidth(clean(is.Status)))
		aw = max(aw, textWidth(clean(is.Assignee)))
	}
	kw, sw, aw = min(kw, 16), min(sw, 20), min(aw, 16)
	const tw, minSum = 7, 16 // ширина ОЦЕНКА/СПИСАНО; меньше названию не оставляем
	times := true
	fixed := func() int {
		n := 1 + kw + 1 + sw + 1
		if times {
			n += 2 * (tw + 1)
		}
		if team {
			n += aw + 1
		}
		return n
	}
	if short := minSum - (w - fixed()); short > 0 {
		sw = max(6, sw-short)
	}
	if w-fixed() < minSum {
		times = false
	}
	if short := minSum - (w - fixed()); short > 0 && team {
		aw = max(6, aw-short)
	}
	sumW := max(1, w-fixed())
	row := func(mark, k, status, est, spent, who, sum string) []string {
		cells := []string{mark, fit(k, kw), " ", fit(status, sw), " "}
		if times {
			cells = append(cells, fitRight(est, tw), " ", fitRight(spent, tw), " ")
		}
		if team {
			cells = append(cells, fit(who, aw), " ")
		}
		return append(cells, fit(sum, sumW))
	}
	lines := []string{paint(styleDim, strings.Join(row(" ", "KEY", "СТАТУС", "ОЦЕНКА", "СПИСАНО", "ИСПОЛНИТЕЛЬ", "ЗАДАЧА"), ""))}
	rows := max(1, h-1)
	sel := max(0, indexOf(v, m.sel))
	m.listTop = scrollTo(m.listTop, sel, rows, len(v))
	for i := m.listTop; i < min(len(v), m.listTop+rows); i++ {
		is := v[i]
		who := is.Assignee
		if who == "" {
			who = "-"
		}
		mark := " "
		if i == sel {
			mark = "▸"
		}
		cells := row(mark, is.Key, is.Status, is.Estimate.String(), is.Spent.String(), who, is.Summary)
		switch {
		case i == sel:
			lines = append(lines, paint(styleReverse, strings.Join(cells, "")))
		case is.done():
			lines = append(lines, paint(styleDim, strings.Join(cells, "")))
		default:
			cells[1] = paint(styleBold, cells[1])
			cells[3] = paint(catColor(is.Category), cells[3])
			lines = append(lines, strings.Join(cells, ""))
		}
	}
	return lines
}

func (m *viModel) viewEmpty(w int) []string {
	text := "нет задач"
	if m.filter != "" {
		text = "ничего не найдено по «" + m.filter + "» — Esc: сбросить поиск"
	}
	return []string{fit("", w), fit("  "+text, w),
		paint(styleDim, fit("  p — другой проект · u — вся команда · r — обновить", w))}
}

// ---------- канбан ----------

func (m *viModel) viewBoard(w, h int) []string {
	cols := viColumns(m.visible())
	if len(cols) == 0 {
		return m.viewEmpty(w)
	}
	ci, ri := m.boardPos(cols)
	n := len(cols)
	shown := n
	colW := (w - (n - 1)) / n
	if colW < viMinColW {
		shown = clamp((w+1)/(viMinColW+1), 1, n)
		colW = (w - (shown - 1)) / shown
	}
	m.boardLeft = clamp(clamp(m.boardLeft, ci-shown+1, ci), 0, n-shown) // колонка выбора — на экране
	extra := w - (shown*colW + shown - 1)                               // остаток ширины — последней колонке
	grid := make([][]string, shown)
	for j := range grid {
		c := m.boardLeft + j
		cw := colW
		if j == shown-1 {
			cw += extra
		}
		grid[j] = m.viewColumn(cols[c], c == ci, ri, cw, h, j == 0 && m.boardLeft > 0, j == shown-1 && c < n-1)
	}
	sep := paint(styleDim, "│")
	lines := make([]string, h)
	parts := make([]string, shown)
	for r := range lines {
		for j := range grid {
			parts[j] = grid[j][r]
		}
		lines[r] = strings.Join(parts, sep)
	}
	return lines
}

// cardsIn — сколько карточек помещается в rows строк (у последней промежуток не нужен).
func cardsIn(rows int) int { return max(1, (rows+1)/viCardH) }

// viewColumn — колонка: заголовок, черта (или «↑ ещё N»), карточки и «↓ ещё N»; ровно h строк ширины w.
func (m *viModel) viewColumn(col viColumn, active bool, selRow, w, h int, moreLeft, moreRight bool) []string {
	title := fmt.Sprintf("%s (%d)", col.status, len(col.issues))
	if moreLeft {
		title = "‹ " + title
	}
	if moreRight {
		title += " ›"
	}
	lines := []string{paint(styleBold+catColor(col.category), fit(" "+title, w))}
	total := len(col.issues)
	k := cardsIn(h - 2)
	if total > k {
		k = cardsIn(h - 3) // нужна строка «↓ ещё N»
	}
	top := clamp(m.colTop[col.status], 0, max(0, total-k))
	if active {
		top = scrollTo(top, selRow, k, total)
	}
	m.colTop[col.status] = top
	if top > 0 {
		lines = append(lines, paint(styleDim, fit(fmt.Sprintf(" ↑ ещё %d", top), w)))
	} else {
		lines = append(lines, paint(styleDim, strings.Repeat("─", w)))
	}
	for i := top; i < min(total, top+k); i++ {
		if i > top {
			lines = append(lines, strings.Repeat(" ", w))
		}
		lines = append(lines, m.card(col.issues[i], w, active && i == selRow)...)
	}
	if below := total - (top + k); below > 0 {
		lines = append(fill(lines, w, h-1), paint(styleDim, fit(fmt.Sprintf(" ↓ ещё %d", below), w)))
	}
	return fill(lines, w, h)
}

// cardTimes — оценка/списано на карточке: "4h/1h30m", "4h", "-/2h" или "".
func cardTimes(is viIssue) string {
	switch {
	case is.Estimate > 0 && is.Spent > 0:
		return is.Estimate.String() + "/" + is.Spent.String()
	case is.Estimate > 0:
		return is.Estimate.String()
	case is.Spent > 0:
		return "-/" + is.Spent.String()
	}
	return ""
}

// card — три строки карточки: ключ и время, название в две строки (во «всей команде» — в одну
// и исполнитель).
func (m *viModel) card(is viIssue, w int, selected bool) []string {
	cw := max(1, w-2) // по пробелу с краёв
	l1 := fit(is.Key, cw)
	if t := cardTimes(is); t != "" && textWidth(t)+2 < cw {
		l1 = fit(is.Key, cw-textWidth(t)-1) + " " + t
	}
	sum := wrap(is.Summary, cw)
	l2, l3 := fit(sum[0], cw), fit(strings.Join(sum[1:], " "), cw)
	if m.scope.team {
		who := is.Assignee
		if who == "" {
			who = "без исполнителя"
		}
		l3 = fit("→ "+who, cw)
	}
	lines := []string{" " + l1 + " ", " " + l2 + " ", " " + l3 + " "}
	for i, l := range lines {
		switch {
		case selected:
			lines[i] = paint(styleReverse, l)
		case is.done() || (i == 2 && m.scope.team):
			lines[i] = paint(styleDim, l)
		case i == 0:
			lines[i] = paint(styleBold, l)
		}
	}
	return lines
}

// ---------- подробно ----------

// detailLines — текст jira show, перенесённый по ширине w, и ссылка на задачу; nil — ещё загружается.
func (m *viModel) detailLines(w int) []string {
	if m.detailText == "" {
		return nil
	}
	var out []string
	for i, para := range strings.Split(strings.TrimRight(m.detailText, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, fit("", w))
			continue
		}
		for _, l := range wrap(para, w-2) {
			l = fit(" "+l, w)
			if i < 2 { // ключ, тип, статус и название
				l = paint(styleBold, l)
			}
			out = append(out, l)
		}
	}
	return append(out, fit("", w), paint(styleDim, fit(" "+m.c.base+"/browse/"+m.detailKey, w)))
}

func (m *viModel) viewDetail(w, h int) []string {
	lines := m.detailLines(w)
	if lines == nil {
		return []string{fit("", w), paint(styleDim, fit("  загрузка…", w))}
	}
	m.detailTop = clamp(m.detailTop, 0, max(0, len(lines)-h))
	return lines[m.detailTop:min(len(lines), m.detailTop+h)]
}

// ---------- справка ----------

var viHelp = []string{
	"# Вид",
	"  v Tab        список ↔ канбан           j k ↑ ↓   вверх, вниз",
	"  h l ← →      колонки канбана           g G       в начало, в конец",
	"  Enter        подробно (Esc — назад)    /         поиск (Esc — сбросить)",
	"  p            проект: набирайте ключ или название; «все проекты» — сброс",
	"  u            мои ↔ вся команда (в проекте или спринте)",
	"  r            обновить                  q         выход",
	"# Задача",
	"  d            закрыть: переход в статус категории «готово», подтвердить — y",
	"  m            сменить статус: список переходов",
	"  H L ⇧← ⇧→    перенести карточку в соседнюю колонку канбана",
	"  e            оценка (Original Estimate): 1d 4h, 2h30m",
	"  w            списать время за сегодня: 1h 30m [комментарий]",
	"  c            комментарий",
	"  a            исполнитель: username, me или none",
	"  o            открыть в браузере",
	"",
	"  Клавиши работают и в русской раскладке. Любая клавиша — назад.",
}

func viewHelp(w int) []string {
	lines := []string{fit("", w)}
	for _, l := range viHelp {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			lines = append(lines, paint(styleBold, fit(" "+t, w)))
			continue
		}
		lines = append(lines, fit(l, w))
	}
	return lines
}

// ---------- низ ----------

func (m *viModel) viewBottom(w, h int) ([]string, *cursorPos) {
	var lines []string
	var cur *cursorPos
	room := h - 1 - 2 - 3 // шапка, состояние и подсказки, хотя бы 3 строки тела
	if m.input != nil {
		room--
	}
	switch {
	case m.picker != nil:
		lines, cur = m.viewPicker(w, max(2, room))
	case m.confirm != nil:
		lines = []string{paint(styleBold+color("33"), fit(" "+m.confirm.text+"  y — да · n — нет", w))}
	}
	lines = append(lines, m.viewStatus(w))
	if m.input != nil {
		line, col := m.viewInput(w)
		cur = &cursorPos{row: len(lines), col: col}
		lines = append(lines, line)
	}
	return append(lines, paint(styleDim, fit(" "+m.hints(), w))), cur
}

func (m *viModel) viewPicker(w, maxH int) ([]string, *cursorPos) {
	p := m.picker
	items := p.shown()
	title := " " + p.title
	if p.search {
		title += fmt.Sprintf(" · %d из %d", len(items), len(p.items))
	}
	lines := []string{paint(styleBold, fit(title, w))}
	var cur *cursorPos
	if p.search {
		q := " поиск: " + clean(string(p.query))
		lines = append(lines, fit(q, w))
		cur = &cursorPos{row: 1, col: min(w-1, textWidth(q))}
	}
	if len(items) == 0 {
		return append(lines, paint(styleDim, fit("   ничего не найдено", w))), cur
	}
	rows := clamp(maxH-len(lines), 1, len(items))
	p.cur = clamp(p.cur, 0, len(items)-1)
	p.top = scrollTo(p.top, p.cur, rows, len(items))
	for i := p.top; i < p.top+rows; i++ {
		it := items[i]
		num := "  "
		if !p.search && i < 9 {
			num = strconv.Itoa(i+1) + " "
		}
		note := truncate(clean(it.note), w/3)
		labelW := w
		if note != "" {
			labelW = w - textWidth(note) - 1
		}
		label := fit(" "+num+it.label, labelW)
		switch {
		case i == p.cur && note != "":
			lines = append(lines, paint(styleReverse, label+" "+note))
		case i == p.cur:
			lines = append(lines, paint(styleReverse, label))
		case note != "":
			lines = append(lines, label+" "+paint(styleDim, note))
		default:
			lines = append(lines, label)
		}
	}
	return lines, cur
}

func (m *viModel) viewStatus(w int) string {
	switch {
	case m.busy != "":
		return paint(color("33"), fit(" ⋯ "+m.busy+"…", w))
	case m.status == "":
		return fit("", w)
	case m.isErr:
		return paint(color("31"), fit(" ✗ "+m.status, w))
	}
	return paint(color("32"), fit(" "+m.status, w))
}

// viewInput — строка ввода и колонка курсора; длинный текст прокручивается так, чтобы курсор был виден.
func (m *viModel) viewInput(w int) (string, int) {
	in := m.input
	prompt := truncate(" "+clean(in.prompt), w/2)
	pw := textWidth(prompt)
	avail := max(1, w-pw-1)
	start := 0
	for start < in.pos && textWidth(clean(string(in.buf[start:in.pos]))) > avail {
		start++
	}
	col := pw + textWidth(clean(string(in.buf[start:in.pos])))
	return paint(styleBold, prompt) + fit(string(in.buf[start:]), w-pw), col
}

func (m *viModel) hints() string {
	switch {
	case m.input != nil:
		return "Enter — готово · Esc — отмена · ←→ Ctrl+U Ctrl+W — правка"
	case m.picker != nil && m.picker.search:
		return "набирайте для поиска · ↑↓ — выбор · Enter — выбрать · Esc — отмена"
	case m.picker != nil:
		return "j/k ↑↓ или 1–9 — выбор · Enter — выбрать · Esc — отмена"
	case m.confirm != nil:
		return "y — да · n или Esc — нет"
	case m.screen == scrHelp:
		return "любая клавиша — назад"
	case m.screen == scrDetail:
		return "j/k листать · d закрыть · m статус · e оценка · w списать · c коммент · a исполнитель · o браузер · Esc назад"
	case m.mode == viBoard:
		return "hjkl ←↓↑→ · H/L перенести · d закрыть · m статус · e оценка · w списать · c коммент · a исполнитель · Enter подробно · p проект · u мои/все · v список · / поиск · ? справка · q выход"
	}
	return "j/k ↑↓ · d закрыть · m статус · e оценка · w списать · c коммент · a исполнитель · Enter подробно · p проект · u мои/все · v канбан · / поиск · ? справка · q выход"
}
