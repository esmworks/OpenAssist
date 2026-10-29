package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/providers"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Sağlayıcı yönetimi (Modeller ekranı). Tarifler eskiden ortam
// değişkenindeydi; artık DB'de yaşıyor ve buradan düzenleniyor.
//
// ANAHTAR TEK YÖNLÜ: istemciye asla dönmez, yalnız "tanımlı mı" bilgisi
// gider. Bir kez yazıldıktan sonra okunabilseydi, API belirtecini ele
// geçiren biri tüm sağlayıcı anahtarlarını da almış olurdu.

// pi models.json anahtarı ve `--model <id>/<model>` öneki olduğu için kimlik
// dar bir alfabeye sıkışır (göç 00016'daki CHECK ile aynı).
var saglayiciKimlikDeseni = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// pi'nin konuştuğu tel biçimleri (docs/models.md).
var gecerliAPIler = map[string]bool{
	"openai-completions":   true,
	"openai-responses":     true,
	"anthropic-messages":   true,
	"google-generative-ai": true,
}

type providerModelDTO struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	ContextWindow int      `json:"context_window"`
	MaxTokens     int      `json:"max_tokens"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input"`
}

type providerDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	API     string `json:"api"`
	Enabled bool   `json:"enabled"`
	// HasKey — anahtar TANIMLI mı. Değerin kendisi hiçbir yanıtta yer almaz.
	HasKey   bool               `json:"has_key"`
	Compat   map[string]any     `json:"compat,omitempty"`
	Position int                `json:"position"`
	Models   []providerModelDTO `json:"models"`
}

type providerReq struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	API     string `json:"api"`
	Enabled *bool  `json:"enabled"`
	// APIKey — YALNIZ yazma. nil = dokunma, "" = sil, dolu = değiştir.
	// Pointer olması şart: her düzenlemede istemcinin anahtarı geri
	// göndermesi gerekseydi, hiç göndermediğimiz bir değeri istemiş olurduk.
	APIKey   *string            `json:"api_key"`
	Compat   map[string]any     `json:"compat"`
	Position int                `json:"position"`
	Models   []providerModelDTO `json:"models"`
}

func (r *providerReq) Validate() error {
	r.ID = strings.TrimSpace(strings.ToLower(r.ID))
	r.BaseURL = strings.TrimSpace(r.BaseURL)
	r.API = strings.TrimSpace(r.API)

	if !saglayiciKimlikDeseni.MatchString(r.ID) {
		return errors.New("id küçük harf, rakam ve . _ - içerebilir; harf ya da rakamla başlamalı")
	}
	if r.BaseURL == "" {
		return errors.New("base_url boş olamaz")
	}
	if !strings.HasPrefix(r.BaseURL, "http://") && !strings.HasPrefix(r.BaseURL, "https://") {
		return errors.New("base_url http:// ya da https:// ile başlamalı")
	}
	if r.API == "" {
		r.API = "openai-completions"
	}
	if !gecerliAPIler[r.API] {
		return errors.New("api geçersiz: openai-completions | openai-responses | anthropic-messages | google-generative-ai")
	}

	gorulen := map[string]bool{}
	for i := range r.Models {
		m := &r.Models[i]
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" {
			return errors.New("model kimliği boş olamaz")
		}
		// Eğik çizgi kimliği ikiye bölerdi: önek sağlayıcının kendisidir.
		if strings.Contains(m.ID, "/") {
			return errors.New("model kimliği eğik çizgi içeremez: " + m.ID)
		}
		if gorulen[m.ID] {
			return errors.New("model iki kez verildi: " + m.ID)
		}
		gorulen[m.ID] = true
		if m.ContextWindow < 0 || m.MaxTokens < 0 {
			return errors.New("bağlam penceresi ve çıktı sınırı negatif olamaz")
		}
		if m.Input == nil {
			m.Input = []string{}
		}
	}
	return nil
}

func toProviderDTO(p db.Provider, models []db.ProviderModel) providerDTO {
	out := providerDTO{
		ID: p.ID, Name: p.Name, BaseURL: p.BaseUrl, API: p.Api,
		Enabled: p.Enabled, HasKey: p.SealedApiKey != "",
		Position: int(p.Position), Models: []providerModelDTO{},
	}
	if len(p.Compat) > 0 {
		var c map[string]any
		if err := json.Unmarshal(p.Compat, &c); err == nil && len(c) > 0 {
			out.Compat = c
		}
	}
	for _, m := range models {
		if m.ProviderID != p.ID {
			continue
		}
		girdi := m.Input
		if girdi == nil {
			girdi = []string{}
		}
		out.Models = append(out.Models, providerModelDTO{
			ID: m.ModelID, Label: m.Label, ContextWindow: int(m.ContextWindow),
			MaxTokens: int(m.MaxTokens), Reasoning: m.Reasoning, Input: girdi,
		})
	}
	return out
}

func (a *api) listProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListProviders(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := a.st.Q.ListProviderModels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]providerDTO, 0, len(rows))
	for _, p := range rows {
		out = append(out, toProviderDTO(p, models))
	}
	writeJSON(w, http.StatusOK, out)
}

// saveProvider — oluşturur ya da tümüyle günceller (PUT semantiği).
//
// Model listesi TAM DEĞİŞTİRİLİR: ekrandaki liste neyse DB'de o olur. Kısmi
// birleştirme, kullanıcının sildiği bir modelin listede kalmasına yol açardı.
func (a *api) saveProvider(w http.ResponseWriter, r *http.Request) {
	var req providerReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if yol := r.PathValue("id"); yol != "" {
		req.ID = yol
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.providers == nil {
		writeErr(w, http.StatusServiceUnavailable, "sağlayıcı servisi kapalı")
		return
	}
	// Anahtar yazılacaksa mühür ŞART: düz metin saklamaktansa reddet.
	if req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "" && !a.providers.MuhurVar() {
		writeErr(w, http.StatusBadRequest, providers.ErrMuhurYok.Error())
		return
	}

	// compat KORUNUR: alan hiç gönderilmediyse (Modeller ekranı göndermiyor —
	// uyumluluk bayrakları uzman ayarıdır) var olan değer yerinde kalır.
	// Yoksa ekrandan yapılan her düzenleme, API'den kurulmuş bir bayrağı
	// sessizce siler ve sağlayıcı bir sonraki turda bozuk konuşurdu.
	compat := []byte("{}")
	if req.Compat != nil {
		if b, err := json.Marshal(req.Compat); err == nil {
			compat = b
		}
	} else if eski, err := a.st.Q.GetProvider(r.Context(), req.ID); err == nil && len(eski.Compat) > 0 {
		compat = eski.Compat
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if _, err := a.st.Q.UpsertProvider(r.Context(), db.UpsertProviderParams{
		ID: req.ID, Name: req.Name, BaseUrl: req.BaseURL, Api: req.API,
		Enabled: enabled, Compat: compat, Position: int32(req.Position),
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := a.st.Q.DeleteProviderModels(r.Context(), req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i, m := range req.Models {
		if err := a.st.Q.UpsertProviderModel(r.Context(), db.UpsertProviderModelParams{
			ProviderID: req.ID, ModelID: m.ID, Label: m.Label,
			ContextWindow: int32(m.ContextWindow), MaxTokens: int32(m.MaxTokens),
			Reasoning: m.Reasoning, Input: m.Input, Position: int32(i),
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if req.APIKey != nil {
		if err := a.providers.AnahtarYaz(r.Context(), req.ID, *req.APIKey); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	kayit, err := a.st.Q.GetProvider(r.Context(), req.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	models, err := a.st.Q.ListModelsForProvider(r.Context(), req.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Yoklama önbelleği bayatladı: model listesi değişti, Modeller ekranı
	// eski listeyi göstermesin.
	a.probes.temizle()
	writeJSON(w, http.StatusOK, toProviderDTO(kayit, models))
}

func (a *api) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.st.Q.GetProvider(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "sağlayıcı bulunamadı")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Modeller ON DELETE CASCADE ile gider.
	if err := a.st.Q.DeleteProvider(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.probes.temizle()
	w.WriteHeader(http.StatusNoContent)
}
