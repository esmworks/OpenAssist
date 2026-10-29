package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/esmworks/OpenAssist/internal/engine"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// eklentiDTO — sunucuda KURULU bir eklenti ve onu kullanan botlar.
//
// İki ayrı gerçeği tek satırda gösteriyoruz çünkü kullanıcı ikisini birlikte
// soruyor: "bu eklenti duruyor mu" ve "kim kullanıyor". Kaldırma kararı
// ikincisine bakmadan verilemez.
type eklentiDTO struct {
	ID          string `json:"id"` // allowlist adı — bots.extensions'a bu yazılır
	Engine      string `json:"engine"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // builtin | file | npm
	Version     string `json:"version,omitempty"`
	// UsedBy — bu eklentiyi allowlist'inde taşıyan botların adları (sıralı).
	// Sayı değil AD döner: "3 bot" kullanıcıya hangi botu bozacağını söylemez.
	UsedBy []string `json:"used_by"`
}

func (a *api) listExtensions(w http.ResponseWriter, r *http.Request) {
	bots, err := a.st.Q.ListBots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := []eklentiDTO{}
	for _, id := range motorAdlari(a.engines) {
		ex, ok := a.engines[id].(engine.Extender)
		if !ok {
			continue
		}
		list, err := ex.Extensions(r.Context())
		if err != nil {
			// Katalog okunamıyorsa SUSMAK yanlış olur: ekran "hiç eklenti
			// yok" gösterir, kullanıcı da onay kapısının kaybolduğunu
			// sanır. Sebebi olduğu gibi söylüyoruz.
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, eklentiDTOlari(id, list, bots)...)
	}
	writeJSON(w, http.StatusOK, out)
}

type eklentiKurReq struct {
	// Source — motorun kendi kaynak sözdizimi (pi: "npm:paket@sürüm").
	Source string `json:"source"`
	// Engine — hangi motora kurulacağı; boşsa tek eklentili motor seçilir.
	Engine string `json:"engine"`
}

// POST /api/extensions — paket kurar.
//
// DİKKAT: bu uç SUNUCUDA ÜÇÜNCÜ PARTİ KOD ÇALIŞTIRIR (npm kurulum betikleri).
// Kapı bearer belirtecidir; kurulan paket yine de hiçbir bota kendiliğinden
// bulaşmaz, botun allowlist'ine açıkça eklenmesi gerekir.
func (a *api) installExtension(w http.ResponseWriter, r *http.Request) {
	var req eklentiKurReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeErr(w, http.StatusBadRequest, "source zorunlu (örn. npm:pi-subagents@0.50.0)")
		return
	}
	mgr, id, ok := a.eklentiYoneticisi(req.Engine)
	if !ok {
		writeErr(w, http.StatusBadRequest, "eklenti kurulabilen motor yok: "+req.Engine)
		return
	}
	slog.Warn("eklenti kurulum isteği", "motor", id, "kaynak", req.Source, "uzak", r.RemoteAddr)
	if err := mgr.InstallExtension(r.Context(), req.Source); err != nil {
		// Motorun kendi cümlesi (404, ağ hatası, geçersiz kaynak) kullanıcıya
		// aynen gider: "kurulamadı" tek başına hiçbir şey söylemez.
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	a.listExtensions(w, r)
}

// DELETE /api/extensions/{id} — kurulu paketi kaldırır. Gömülü eklenti
// kaldırılamaz (ikilinin parçası) ve bir bot kullanıyorsa önce uyarılır:
// sessizce kaldırmak o botun HER koşusunu düşürürdü.
func (a *api) removeExtension(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mgr, engineID, ok := a.eklentiYoneticisi(r.URL.Query().Get("engine"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "eklenti yönetebilen motor yok")
		return
	}

	bots, err := a.st.Q.ListBots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if kullananlar := eklentiyiKullananlar(engineID, id, bots); len(kullananlar) > 0 && r.URL.Query().Get("force") != "1" {
		writeErr(w, http.StatusConflict,
			"bu eklentiyi kullanan botlar var: "+strings.Join(kullananlar, ", ")+
				" — önce onların eklenti listesinden çıkar")
		return
	}
	if err := mgr.RemoveExtension(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	a.listExtensions(w, r)
}

// eklentiYoneticisi — istenen motoru (boşsa eklenti yönetebilen tek motoru)
// döner. Ad tahmin EDİLMEZ: iki motor da yönetebiliyorsa istemci hangisini
// kastettiğini söylemek zorundadır, yoksa paket yanlış motora kurulurdu.
func (a *api) eklentiYoneticisi(engineID string) (engine.ExtensionManager, string, bool) {
	if engineID != "" {
		mgr, ok := a.engines[engineID].(engine.ExtensionManager)
		return mgr, engineID, ok
	}
	var bulunan engine.ExtensionManager
	var bulunanID string
	for _, id := range motorAdlari(a.engines) {
		if mgr, ok := a.engines[id].(engine.ExtensionManager); ok {
			if bulunan != nil {
				return nil, "", false
			}
			bulunan, bulunanID = mgr, id
		}
	}
	return bulunan, bulunanID, bulunan != nil
}

func eklentiyiKullananlar(engineID, extID string, bots []db.Bot) []string {
	var out []string
	for _, b := range bots {
		if b.Engine != engineID {
			continue
		}
		for _, e := range b.Extensions {
			if e == extID {
				out = append(out, b.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// eklentiDTOlari — motor kataloğu + bot listesi → ekran satırları. Saf
// fonksiyon: eşleme kuralı (hangi bot hangi eklentiyi kullanıyor) testte
// veritabanı olmadan sınanabilsin.
func eklentiDTOlari(engineID string, list []engine.ExtensionInfo, bots []db.Bot) []eklentiDTO {
	kullanan := map[string][]string{}
	for _, b := range bots {
		// Başka motora bağlı botun allowlist'i bu kataloğu ilgilendirmez:
		// aynı ad iki motorda başka şey demek olabilir.
		if b.Engine != engineID {
			continue
		}
		for _, e := range b.Extensions {
			kullanan[e] = append(kullanan[e], b.Name)
		}
	}

	out := make([]eklentiDTO, 0, len(list))
	for _, e := range list {
		// Kullanılmayan eklentide BOŞ DİZİ: nil bırakılsa JSON'da null olur
		// ve istemci listeyi çözerken kartı düşürürdü.
		adlar := []string{}
		if k, ok := kullanan[e.ID]; ok {
			adlar = k
		}
		sort.Strings(adlar)
		out = append(out, eklentiDTO{
			ID: e.ID, Engine: engineID, Name: e.Name,
			Description: e.Description, Kind: e.Kind, Version: e.Version,
			UsedBy: adlar,
		})
	}
	return out
}

// motorAdlari — kayıtlı motorların sıralı kimlikleri. Harita gezintisi rastgele
// sıralıdır; liste her istekte yer değiştirseydi ekran kendiliğinden zıplardı.
func motorAdlari(engines map[string]engine.Executor) []string {
	ids := make([]string, 0, len(engines))
	for id := range engines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// bilinmeyenEklenti — allowlist DB'ye yazılmadan önceki kapı: kayıtlı olmayan
// bir ad varsa onu döner.
//
// Neden yazma anında: eksik eklenti koşuyu DÜŞÜRÜR (adaptör bilinçli olarak
// fail-closed — onay kapısı oa-gateway'in içinde). Kapı olmasaydı kullanıcı
// ayarda "kaydedildi" görür, hatayı ilk mesajda yaşardı.
//
// Motor tanımıyorsa (test/iskelet kurulumu), eklenti kavramı yoksa ya da
// katalog okunamıyorsa doğrulama YAPILMAZ: bilmediğimiz için reddetmek,
// çalışan bir kurulumu ayar ekranından kilitlerdi.
func (a *api) bilinmeyenEklenti(ctx context.Context, engineID string, istenen []string) (string, bool) {
	ex, ok := a.engines[engineID].(engine.Extender)
	if !ok {
		return "", false
	}
	list, err := ex.Extensions(ctx)
	if err != nil {
		return "", false
	}
	kurulu := make(map[string]bool, len(list))
	for _, e := range list {
		kurulu[e.ID] = true
	}
	for _, ad := range istenen {
		if !kurulu[ad] {
			return ad, true
		}
	}
	return "", false
}
