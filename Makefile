BIN   ?= $(HOME)/.local/bin/jira
SKILL ?= $(HOME)/.claude/skills/jira
LDFLAGS = -s -w

.PHONY: build test install dist clean

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o jira .

test:
	go test ./...

install: test build
	mkdir -p $(dir $(BIN))
	install -m 755 jira $(BIN)
	mkdir -p $(SKILL)
	cp -f skill/SKILL.md $(SKILL)/SKILL.md
	@# skill/local.md — своё дополнение к скиллу (адрес, статусы, шаблон задач); в git не попадает
	@if [ -f skill/local.md ]; then printf '\n' >> $(SKILL)/SKILL.md; cat skill/local.md >> $(SKILL)/SKILL.md; echo "+ skill/local.md"; fi

# бинарники под все платформы
dist: test
	mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/jira-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/jira-darwin-amd64 .
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/jira-linux-amd64 .
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/jira-windows-amd64.exe .

clean:
	rm -rf jira dist
