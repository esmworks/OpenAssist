MODULE  := github.com/esmworks/OpenAssist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) -X $(MODULE)/internal/version.Commit=$(COMMIT)

.PHONY: build run test test-integration lint generate sqlc-check migrate tidy

# Tek ikili: motor pi'nin kendi CLI'ıdır (PATH'ten), oa-gateway eklentisi
# core'a gömülüdür (internal/engine/pi/embed.go) — yanına kurulacak yardımcı
# süreç yok.
build:
	go build -ldflags '$(LDFLAGS)' -o bin/core ./cmd/core

run: build
	./bin/core serve

test:
	go test ./...

# Gerçek Postgres ister: DATABASE_URL=postgres://… make test-integration
# -p 1: paketler TEK TEK koşar. Hepsi aynı veritabanını ve aynı koşu
# kuyruğunu paylaşır (FOR UPDATE SKIP LOCKED); paralel koşarsa bir paketin
# worker'ı diğerinin koşusunu kapar ve testler birbirini bozar.
test-integration:
	go test -tags integration -p 1 -timeout 300s ./...

lint:
	golangci-lint run

generate:
	sqlc generate

# Üretilmiş kod bayat/elle değiştirilmiş mi? (CI'da da koşar)
sqlc-check: generate
	git diff --exit-code -- internal/store/db

migrate:
	go run ./cmd/core migrate

tidy:
	go mod tidy
