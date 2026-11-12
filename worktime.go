package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	jiraTime  = "2006-01-02T15:04:05.000-0700"
	dayLayout = "2006-01-02" // дата YYYY-MM-DD: --date, Month report, JQL
)

func cmdEstimate(c *Client, a Args) (result, error) {
	key := a.arg(0)
	est, err := parseDur(a.pos[1:]...)
	if err != nil {
		return nil, err
	}
	if err := setEstimate(c, key, est); err != nil {
		return nil, err
	}
	return message(fmt.Sprintf("%s: оценка %s\n", key, est)), nil
}

func cmdLog(c *Client, a Args) (result, error) {
	key := a.arg(0)
	durArgs, rest := splitDur(a.pos[1:])
	if len(durArgs) == 0 {
		durArgs = rest[:1] // нет ведущей длительности — parseDur объяснит, что не так с первым словом
	}
	spent, err := parseDur(durArgs...)
	if err != nil {
		return nil, err
	}
	comment := strings.Join(rest, " ")
	started := time.Now()
	if d := a.get("date"); d != "" {
		t, err := time.ParseInLocation(dayLayout, d, time.Local)
		if err != nil {
			return nil, fmt.Errorf("--date: ожидается YYYY-MM-DD")
		}
		started = t.Add(9 * time.Hour)
	}
	if err := c.addWorklog(key, spent, started, comment); err != nil {
		return nil, err
	}
	return message(fmt.Sprintf("%s: списано %s за %s\n", key, spent, started.Format(dayLayout))), nil
}
