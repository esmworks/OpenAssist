# Çok aşamalı imaj (Faz 9, PRD §4/K15):
#   1. Go build — core (statik)
#   2. Runtime — node:22-slim: pi Node gerektirir (K15 notu:
#      distroless/static'ten Node'lu imaja evrildi). Root değil "node"
#      kullanıcısıyla koşar.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/esmworks/OpenAssist/internal/version.Version=${VERSION} -X github.com/esmworks/OpenAssist/internal/version.Commit=${COMMIT}" \
    -o /core ./cmd/core

FROM node:22-slim
# ca-certificates ŞART: node:22-slim CA paketi taşımaz (Node kendi gömülü
# köklerini kullanır ama Go core'un dış HTTPS'i — Telegram Bot API, Google
# OAuth — sistem köklerine bakar; paketsiz x509 doğrulaması düşer).
# tzdata imajda zaten var (rutin/politika dilimleri için doğrulandı).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# Motor (tek): pi. Sürüm sabitlenir — motor davranışı deploy'lar arasında
# sürpriz değişmesin; yükseltme bilinçli commit ile. RPC olay şeması sürüme
# bağlıdır, adaptör ölçülmüş sürüme göre yazıldı.
RUN npm install -g @earendil-works/pi-coding-agent@0.84.2 \
    && npm cache clean --force
COPY --from=build /core /usr/local/bin/core
# oa-gateway eklentisi binary'ye GÖMÜLÜDÜR (internal/engine/pi/embed.go):
# açılışta veri dizinine yazılır, ayrı kopyalama adımı yoktur.
# Veri dizini: koşu çalışma alanları + olay tee dosyaları (DATA_DIR).
RUN mkdir -p /data && chown node:node /data
# USER node YOK — bind mount imajdaki chown'u örtüyor. Kap root başlar,
# entrypoint /data sahipliğini düzeltip setpriv ile "node"a düşer; koşan
# süreç yine ayrıcalıksızdır (bkz. entrypoint.sh).
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENV DATA_DIR=/data
# 8082 (MCP) BİLİNÇLİ yok: konteyner içi loopback, dışarı açılmaz.
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["serve"]
