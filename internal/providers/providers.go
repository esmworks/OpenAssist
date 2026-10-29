// Package providers — model sağlayıcılarının kaydı (göç 00016).
//
// Sağlayıcı tarifleri ORTAM DEĞİŞKENİNDE değil veritabanında yaşar: yeni bir
// model eklemek ya da dönen bir anahtarı değiştirmek, Dokploy'da env düzenleyip
// kabı yeniden başlatmayı gerektirmesin. Ayarların yaşadığı yer uygulamanın
// içidir; koşu yolu her turda buradan okur.
//
// Anahtarlar durağan halde MÜHÜRLÜ durur (AES-256-GCM, MASTER_KEY) —
// konektör token'larıyla aynı yol. Dışarı hiç verilmez: API yalnız "anahtar
// var mı" bilgisini döner, değeri asla.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/esmworks/OpenAssist/internal/crypto"
	"github.com/esmworks/OpenAssist/internal/engine/pi"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

type Service struct {
	st     *store.Store
	sealer *crypto.Sealer
}

// New — sealer nil olabilir: MASTER_KEY tanımsız bir kurulumda sağlayıcı
// EKLENEMEZ (anahtar mühürlenemez) ama var olanların anahtarsız alanları yine
// okunur. Sessizce düz metin yazmaktansa özelliği kapatmak doğru.
func New(st *store.Store, sealer *crypto.Sealer) *Service {
	return &Service{st: st, sealer: sealer}
}

// ErrMuhurYok — MASTER_KEY yokken anahtar yazma denemesi.
var ErrMuhurYok = errors.New("MASTER_KEY tanımlı değil: sağlayıcı anahtarı mühürlenemiyor (openssl rand -hex 32)")

func (s *Service) MuhurVar() bool { return s.sealer != nil }

// Tarifler — motorun ihtiyaç duyduğu biçimde etkin sağlayıcılar.
// pi.Adapter.Providers buna bağlanır ve HER turda çağırır.
func (s *Service) Tarifler(ctx context.Context) ([]pi.ProviderConfig, error) {
	rows, err := s.st.Q.ListEnabledProviders(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	modeller, err := s.st.Q.ListProviderModels(ctx)
	if err != nil {
		return nil, err
	}
	basina := map[string][]db.ProviderModel{}
	for _, m := range modeller {
		basina[m.ProviderID] = append(basina[m.ProviderID], m)
	}

	out := make([]pi.ProviderConfig, 0, len(rows))
	for _, r := range rows {
		p := pi.ProviderConfig{
			ID: r.ID, Name: r.Name, BaseURL: r.BaseUrl, API: r.Api,
			APIKey: s.anahtarAc(r),
			Compat: compatCoz(r.Compat),
		}
		for _, m := range basina[r.ID] {
			p.Models = append(p.Models, pi.ModelSpec{
				ID: m.ModelID, Name: m.Label, Reasoning: m.Reasoning,
				Input: m.Input, ContextWindow: int(m.ContextWindow),
				MaxTokens: int(m.MaxTokens),
			})
		}
		out = append(out, p)
	}
	return out, nil
}

// anahtarAc — mührü açar. Açılamayan anahtar koşuyu düşürmez: sağlayıcı
// anahtarsız denenir ve sebep loglanır. (MASTER_KEY değişmişse tüm mühürler
// açılamaz; o durumda kullanıcının yapması gereken anahtarları yeniden
// girmektir, koşuların hepsinin "config hatası" ile düşmesi değil.)
func (s *Service) anahtarAc(r db.Provider) string {
	if r.SealedApiKey == "" || s.sealer == nil {
		return ""
	}
	ham, err := s.sealer.Open(r.SealedApiKey)
	if err != nil {
		slog.Warn("sağlayıcı anahtarının mührü açılamadı",
			"saglayici", r.ID, "err", err)
		return ""
	}
	return string(ham)
}

// AnahtarYaz — anahtarı mühürleyip saklar. Boş değer anahtarı SİLER.
func (s *Service) AnahtarYaz(ctx context.Context, id, anahtar string) error {
	anahtar = strings.TrimSpace(anahtar)
	if anahtar == "" {
		return s.st.Q.SetProviderKey(ctx, db.SetProviderKeyParams{ID: id, SealedApiKey: ""})
	}
	if s.sealer == nil {
		return ErrMuhurYok
	}
	muhurlu, err := s.sealer.Seal([]byte(anahtar))
	if err != nil {
		return err
	}
	return s.st.Q.SetProviderKey(ctx, db.SetProviderKeyParams{ID: id, SealedApiKey: muhurlu})
}

func compatCoz(ham []byte) map[string]any {
	if len(ham) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(ham, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Tohumla — DB boşsa ortamdaki tarifi bir kereliğine içeri alır.
//
// Neden var: bu özellik gelmeden önceki kurulumlar sağlayıcıyı PI_PROVIDER_*
// ile tanımlıyordu. Göç sonrası DB boş olacağı için o kurulumlar modelsiz
// kalırdı. Tohumlama YALNIZ tablo tamamen boşken çalışır — kullanıcı bir kez
// düzenledikten sonra ortam değişkeni bir daha söz sahibi değildir, yoksa her
// yeniden başlatma kullanıcının değişikliğini geri alırdı.
func (s *Service) Tohumla(ctx context.Context, t Tohum) error {
	if t.ID == "" || t.BaseURL == "" {
		return nil
	}
	sayi, err := s.st.Q.CountProviders(ctx)
	if err != nil {
		return err
	}
	if sayi > 0 {
		return nil
	}

	// Anahtar var ama mühür yok: HİÇ tohumlama ve açılışı da DÜŞÜRME.
	//
	// Yarım tohumlamak (kayıt açıp anahtarı atlamak) tabloyu doldurur ve
	// tohum bir daha asla koşmaz — MASTER_KEY sonradan tanımlandığında bile.
	// Hata döndürmek ise çekirdeği hiç açtırmazdı: kullanıcı ne olduğunu
	// görebileceği ekrana da ulaşamazdı. Doğrusu gürültülü uyarıp geçmek.
	if t.APIKey != "" && s.sealer == nil {
		slog.Warn("sağlayıcı tohumlanmadı: MASTER_KEY tanımlı değil, anahtar mühürlenemiyor",
			"saglayici", t.ID, "yapılacak", "MASTER_KEY tanımla (openssl rand -hex 32) ve yeniden başlat")
		return nil
	}

	if _, err := s.st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: t.ID, Name: t.Name, BaseUrl: t.BaseURL, Api: t.API,
		Enabled: true, Compat: []byte("{}"),
	}); err != nil {
		return fmt.Errorf("sağlayıcı tohumlanamadı: %w", err)
	}
	for i, m := range t.Models {
		if err := s.st.Q.UpsertProviderModel(ctx, db.UpsertProviderModelParams{
			ProviderID: t.ID, ModelID: m.ID, Label: m.Label,
			ContextWindow: int32(m.ContextWindow), MaxTokens: int32(m.MaxTokens),
			Input: []string{}, Position: int32(i),
		}); err != nil {
			return fmt.Errorf("model tohumlanamadı (%s): %w", m.ID, err)
		}
	}
	if t.APIKey != "" {
		if err := s.AnahtarYaz(ctx, t.ID, t.APIKey); err != nil {
			return err
		}
	}
	slog.Info("sağlayıcı ortamdan tohumlandı — bundan sonra Modeller ekranından yönetilir",
		"saglayici", t.ID, "model", len(t.Models))
	return nil
}

// Tohum — ortamdan gelen ilk tarif.
type Tohum struct {
	ID, Name, BaseURL, API, APIKey string
	Models                         []TohumModel
}

type TohumModel struct {
	ID, Label                string
	ContextWindow, MaxTokens int
}
