# jira-cli

`jira` — маленький CLI к Jira Data Center: свои задачи, поиск, комментарии, учёт времени и месячный отчёт, а также интерактивный экран `jira vi` — задачи списком и канбаном.
Вывод компактный (TSV и короткий текст), поэтому им удобно пользоваться и человеку, и ИИ-агенту (в комплекте — скилл для Claude Code).

- Один бинарник, только стандартная библиотека Go.
- Токен хранится в системном хранилище ОС, а не в файле.
- Работает с Jira Data Center / Server по Personal Access Token. Проверялся на Data Center 10.3; Jira Cloud не поддерживается.

## Установка

**Homebrew** (macOS, Linux):

```sh
brew install nizom333/tap/jira-cli
```

**Scoop** (Windows):

```sh
scoop bucket add nizom333 https://github.com/nizom333/homebrew-tap
scoop install jira-cli
```

**Готовый бинарник** под macOS, Linux и Windows — на странице [релизов](https://github.com/nizom333/jira-cli/releases): распакуйте архив и положите `jira` в папку из `PATH`.

**Go**: `go install github.com/nizom333/jira-cli@latest` — бинарник будет называться `jira-cli`; переименуйте его в `jira` или заведите алиас — справка и скилл называют команду `jira`.

Скилл для Claude Code эти способы сами не ставят: скопируйте `SKILL.md` (он есть в архиве релиза и в папке `skill/`) в `~/.claude/skills/jira/`.

### Из исходников

Нужен Go 1.22+.

```sh
git clone https://github.com/nizom333/jira-cli && cd jira-cli
make install          # тесты, сборка, копирует бинарник в ~/.local/bin/jira и скилл в ~/.claude/skills/jira
```

Папка `~/.local/bin` должна быть в `PATH`. Другое место установки: `make install BIN=/usr/local/bin/jira`.

Бинарники под macOS, Linux и Windows — `make dist` (папка `dist/`). В Windows положите `jira-windows-amd64.exe` в папку из `PATH` под именем `jira.exe`.

## Первая настройка

```sh
jira login
```

Команда спросит адрес Jira (если он ещё не задан) и Personal Access Token (в Jira: Профиль → Personal Access Tokens), проверит токен и сохранит оба. Адрес можно передать сразу: `jira login https://jira.example.com`.

```sh
jira me               # проверить: username<TAB>Имя
jira url              # какой адрес используется и откуда он взят
jira url АДРЕС        # сменить адрес
jira logout           # удалить токен из хранилища
jira type             # тип задачи для create без --type (изначально Task)
jira type Задача      # задать свой, если в вашей Jira типы называются иначе
```

### Где что хранится

| Что | Где |
| --- | --- |
| Адрес, тип задачи по умолчанию | `config.json` в папке настроек ОС: `~/Library/Application Support/jira-cli` (macOS), `~/.config/jira-cli` (Linux), `%AppData%\jira-cli` (Windows) |
| Токен | Keychain (macOS), `secret-tool` / libsecret (Linux), Диспетчер учётных данных (Windows) |

Переменные окружения `JIRA_URL` и `JIRA_TOKEN` важнее сохранённых значений — для CI и скриптов; с ними `jira login` не нужен.

## Команды

```
jira ls [--all] [--user U] [--project P] [-n 30]     мои задачи (по умолчанию незакрытые)
jira q 'JQL' [-n 30]                                 поиск
jira sprint [--project P]                            активные спринты и их фаза (идёт, просрочен)
jira sprint now|active|ID [--project P] [--user U] [--open] [-n 30]
                                                     задачи текущих, всех активных или спринта по номеру
jira show KEY [--comments 3] [--full]
jira vi [--project P] [--sprint now|active|ID] [--user U] [-n 100]
                                                     интерактивно: список и канбан (см. ниже)
jira create --project P --summary S [--type T] [--desc D|-] [--estimate 4h] [--assignee U|me|none] [--yes]
jira assign KEY U|me|none [--yes]
jira move KEY [статус]                               без статуса — список переходов
jira comment KEY текст|-
jira log KEY 1d 4h [комментарий] [--date YYYY-MM-DD]  списать время
jira estimate KEY 1d 4h                              Original Estimate
jira report [YYYY-MM] [--project P] [--user U] [--by day]
```

Полная справка — `jira help`. Коротко о важном:

- `create` и `assign` без `--yes` только показывают превью и ничего не меняют.
- `--json` есть у команд чтения: `me`, `ls`, `q`, `sprint`, `show`, `report`.
- `sprint now` — задачи спринтов, которые идут сегодня по календарным датам (в день конца спринт ещё идёт); активный спринт с прошедшей датой конца считается просроченным, его задачи — `jira sprint ID` или `jira sprint active` вместе со всеми активными. Нет текущего спринта — `sprint now` завершается ошибкой со списком активных спринтов и пропущенных досок (текущий мог быть на них); `sprint active` без спринтов печатает «# нет активных спринтов» без ошибки. Доска, на которой Jira ответила ошибкой, пропускается с предупреждением; в `--json` — поле `warnings` (всегда массив).
- Время вводится в формате Jira (`30m`, `2h`, `1d 4h`; 1d = 8h), выводится в часах и минутах.
- `report` — задачи, которые пользователь закрыл в месяце, и задачи, на которые он списывал время в этом месяце. Термины — в [GLOSSARY.md](GLOSSARY.md).

## Интерактивный режим: jira vi

```sh
jira vi                       # мои задачи: незакрытые и закрытые за последние 14 дней
jira vi --project PROJ        # сразу в проекте
jira vi --sprint now          # мои задачи текущего спринта (active, ID — как в jira sprint)
```

Задачи показываются списком или канбаном (`v` переключает). Колонки канбана — статусы загруженных задач по порядку категорий: к выполнению → в работе → готово. Это не колонки доски Jira: настройки досок `jira vi` не читает.

| Клавиша | Что делает |
| --- | --- |
| `j` `k` / `↑` `↓`, `h` `l` / `←` `→` | выбор задачи; `h` `l` — колонки канбана |
| `v`, `Tab` | список ↔ канбан |
| `Enter` | подробно: описание, комментарии (`Esc` — назад) |
| `d` | закрыть: переход в статус категории «готово»; подтвердить — `y` |
| `m` | сменить статус: список переходов |
| `H` `L` / `Shift+←` `Shift+→` | перенести карточку в соседнюю колонку канбана |
| `e` | оценка (Original Estimate): `1d 4h`, `2h30m` |
| `w` | списать время за сегодня: `1h 30m [комментарий]` |
| `c` | комментарий |
| `a` | исполнитель: username, `me` или `none` |
| `o` | открыть задачу в браузере |
| `p` | проект: все проекты Jira с поиском, сверху — где есть ваши незакрытые задачи; «все проекты» — сброс |
| `u` | мои ↔ вся команда (только в проекте или спринте) |
| `/` | поиск по загруженным задачам (`Esc` — сбросить) |
| `r` | обновить |
| `?` | все клавиши |
| `q`, `Ctrl+C` | выход |

Клавиши работают и в русской раскладке. Нужен терминал: без него (`jira vi | …`, скрипты, ИИ-агенты) команда завершается ошибкой — для них есть `ls`, `q`, `sprint`. `NO_COLOR` отключает цвета.

## Скилл для Claude Code

`make install` кладёт [skill/SKILL.md](skill/SKILL.md) в `~/.claude/skills/jira`: Claude узнаёт команды и правила (читать можно свободно, писать — только по явной просьбе, `create`/`assign` — через превью).

Особенности своей Jira — названия статусов, формат username, шаблон задачи — запишите в `skill/local.md`. Этот файл не попадает в git, а `make install` дописывает его в конец установленного скилла.

## Разработка

```sh
make test             # go test ./...
make build            # ./jira
```

Команды тестируются через `run()` против поддельной Jira на `httptest`, без сети — см. `harness_test.go`.
