-- +goose Up
-- Sağlayıcılar ortam değişkeninden VERİTABANINA taşınır.
--
-- Neden: PI_PROVIDER_* ile bir sağlayıcı eklemek ya da bir modelin bağlam
-- penceresini düzeltmek, Dokploy'da env düzenleyip kabı yeniden başlatmayı
-- gerektiriyordu. Sağlayıcı ve model listesi kullanıcının SIK dokunduğu bir
-- şey (yeni model çıkıyor, anahtar dönüyor, adres değişiyor) — ayarların
-- yaşadığı yer uygulamanın içi olmalı.
--
-- Ortam değişkenleri KALKMADI: ilk açılışta tarif DB boşsa oraya tohumlanır
-- (mevcut kurulumlar kesintisiz geçer), sonra tek kaynak DB'dir.

CREATE TABLE providers (
    -- id pi'ye AYNEN geçer: models.json anahtarı ve `--model <id>/<model>`
    -- önekidir. Bu yüzden dar bir alfabe: boşluk ya da eğik çizgi model
    -- kimliğini ikiye bölerdi.
    id         text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9._-]*$'),
    name       text NOT NULL DEFAULT '',
    base_url   text NOT NULL,
    -- pi'nin konuştuğu tel biçimi (docs/models.md). Listeyi CHECK ile
    -- sabitliyoruz: yanlış değer koşu anında "sağlayıcı çözülemedi" ile
    -- düşer, oysa burada anında hata verir.
    api        text NOT NULL DEFAULT 'openai-completions'
                    CHECK (api IN ('openai-completions', 'openai-responses',
                                   'anthropic-messages', 'google-generative-ai')),
    -- AES-256-GCM mühürlü anahtar (base64: nonce||ciphertext) — connectors
    -- ile aynı yol. Boş = anahtarsız yerel sunucu.
    sealed_api_key text NOT NULL DEFAULT '',
    -- Kapalı sağlayıcı models.json'a hiç yazılmaz: anahtarı silmeden
    -- geçici olarak devre dışı bırakmanın yolu.
    enabled    boolean NOT NULL DEFAULT true,
    -- Sağlayıcıya özel uyumluluk bayrakları (pi docs/models.md), ör.
    -- {"supportsEagerToolInputStreaming": false}.
    compat     jsonb NOT NULL DEFAULT '{}'::jsonb,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE provider_models (
    provider_id    text NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    -- Sağlayıcıya giden ham model adı (eğik çizgi YOK: önek provider_id).
    model_id       text NOT NULL CHECK (model_id <> '' AND model_id !~ '/'),
    label          text NOT NULL DEFAULT '',
    -- 0 = belirtilmedi; pi kendi varsayılanını kullanır.
    context_window integer NOT NULL DEFAULT 0 CHECK (context_window >= 0),
    max_tokens     integer NOT NULL DEFAULT 0 CHECK (max_tokens >= 0),
    reasoning      boolean NOT NULL DEFAULT false,
    -- Girdi modaliteleri (models.dev "modalities.input"): text/image/pdf…
    input          text[] NOT NULL DEFAULT '{}',
    position       integer NOT NULL DEFAULT 0,
    PRIMARY KEY (provider_id, model_id)
);

CREATE INDEX provider_models_sira ON provider_models (provider_id, position);

-- +goose Down
DROP TABLE provider_models;
DROP TABLE providers;
