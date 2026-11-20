package main

// Виджеты нижней панели jira vi: поле ввода (оценка, списание, комментарий, исполнитель, поиск),
// выбор из списка (переходы, проекты) и подтверждение (закрытие задачи).

import "strings"

type viInput struct {
	prompt string
	buf    []rune
	pos    int
	submit func(text string) (cmd, error)
	change func(text string) // на каждое изменение текста (поиск)
	cancel func()            // Esc
}

func (m *viModel) inputKey(k key) cmd {
	in := m.input
	switch k.name {
	case keyEnter:
		c, err := in.submit(string(in.buf))
		if err != nil {
			m.flashErr(err.Error())
			return nil
		}
		m.input = nil
		return c
	case keyEsc:
		m.input = nil
		if in.cancel != nil {
			in.cancel()
		}
		return nil
	case keyLeft:
		in.pos = max(0, in.pos-1)
		return nil
	case keyRight:
		in.pos = min(len(in.buf), in.pos+1)
		return nil
	case keyHome, keyCtrlA:
		in.pos = 0
		return nil
	case keyEnd, keyCtrlE:
		in.pos = len(in.buf)
		return nil
	case keyBackspace:
		if in.pos == 0 {
			return nil
		}
		in.buf = append(in.buf[:in.pos-1], in.buf[in.pos:]...)
		in.pos--
	case keyDelete:
		if in.pos == len(in.buf) {
			return nil
		}
		in.buf = append(in.buf[:in.pos], in.buf[in.pos+1:]...)
	case keyCtrlU:
		in.buf, in.pos = append([]rune(nil), in.buf[in.pos:]...), 0
	case keyCtrlW:
		i := in.pos
		for i > 0 && in.buf[i-1] == ' ' {
			i--
		}
		for i > 0 && in.buf[i-1] != ' ' {
			i--
		}
		in.buf, in.pos = append(in.buf[:i], in.buf[in.pos:]...), i
	case keyRune:
		in.buf = append(in.buf[:in.pos], append([]rune{k.r}, in.buf[in.pos:]...)...)
		in.pos++
	default:
		return nil
	}
	m.status = ""
	if in.change != nil {
		in.change(string(in.buf))
	}
	return nil
}

type pickItem struct {
	label string
	note  string // приглушённый текст справа
	value string
}

// viPicker — выбор из списка. С search буквы набирают поиск, без него — j/k и цифры 1–9.
type viPicker struct {
	title  string
	items  []pickItem
	search bool
	query  []rune
	cur    int // в отфильтрованном списке
	top    int
	choose func(it pickItem) cmd
}

// shown — пункты, подходящие под поиск: каждое слово запроса есть в тексте пункта.
func (p *viPicker) shown() []pickItem {
	q := strings.Fields(strings.ToLower(string(p.query)))
	if len(q) == 0 {
		return p.items
	}
	var out []pickItem
	for _, it := range p.items {
		hay := strings.ToLower(it.label + " " + it.note)
		ok := true
		for _, w := range q {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

func (m *viModel) pickerKey(k key) cmd {
	p := m.picker
	items := p.shown()
	switch k.name {
	case keyEsc:
		m.picker = nil
		return nil
	case keyEnter:
		if len(items) == 0 {
			return nil
		}
		m.picker = nil
		return p.choose(items[clamp(p.cur, 0, len(items)-1)])
	case keyUp, keyCtrlP, keyShiftTab:
		p.cur--
	case keyDown, keyCtrlN, keyTab:
		p.cur++
	case keyPgUp:
		p.cur -= 10
	case keyPgDown:
		p.cur += 10
	case keyBackspace:
		if p.search && len(p.query) > 0 {
			p.query, p.cur = p.query[:len(p.query)-1], 0
		}
	case keyCtrlU:
		p.query, p.cur = nil, 0
	case keyRune:
		if p.search {
			p.query, p.cur = append(p.query, k.r), 0
			break
		}
		lk := latinKey(k)
		switch {
		case lk.is('j'):
			p.cur++
		case lk.is('k'):
			p.cur--
		case lk.is('q'):
			m.picker = nil
			return nil
		case k.r >= '1' && k.r <= '9' && int(k.r-'1') < len(items):
			m.picker = nil
			return p.choose(items[k.r-'1'])
		}
	}
	p.cur = clamp(p.cur, 0, max(0, len(p.shown())-1))
	return nil
}

type viConfirm struct {
	text string
	yes  func() cmd
}

// confirmKey — только y подтверждает (Enter — нет: закрытие задачи не должно случиться нечаянно).
func (m *viModel) confirmKey(k key) cmd {
	switch {
	case k.is('y'):
		f := m.confirm.yes
		m.confirm = nil
		return f()
	case k.is('n') || k.is('q') || k.name == keyEsc:
		m.confirm = nil
		m.flash("отменено")
	}
	return nil
}
