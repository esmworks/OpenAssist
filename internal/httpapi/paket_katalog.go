package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Pi paket galerisi — pi.dev/packages'ın beslendiği yer npm'in kendisidir:
// galeri `pi-package` anahtar kelimesiyle yayınlanmış paketleri gösteriyor
// (pi docs/packages.md, "Gallery Metadata"). Bu yüzden pi.dev'i kazımak yerine
// npm kayıt defterinin arama ucunu kullanıyoruz: sözleşmesi belgeli, sayfa
// tasarımı değişince kırılmaz ve indirme sayısı gibi alanları da veriyor.
//
// Katalog SEÇİM KOLAYLIĞIDIR, onay değil: listede görünmek paketin
// güvenli olduğu anlamına gelmez ve ekran bunu açıkça yazar.
const (
	npmAramaURL         = "https://registry.npmjs.org/-/v1/search"
	npmAramaEtiketi     = "keywords:pi-package"
	paketKatalogTTL     = 30 * time.Minute
	paketKatalogTimeout = 20 * time.Second
	paketKatalogMaxBayt = 8 << 20
	// paketKatalogBoyut — tek sayfada dönen kayıt sayısı. npm'in tavanı 250;
	// ekranda bir sayfa gezinmek yeter, gerisi arama kutusunun işi.
	paketKatalogBoyut = 50
)

// cakisanlar — OpenAssist'te KARŞILIĞI OLAN paketler. Bunlar "kötü paket"
// değil; bizim mimarimizle aynı işi yapıyorlar ve iki kez takılırsa araç adları
// çakışır ya da iş iki kez yapılır. Kullanıcıya sebebiyle söylüyoruz —
// listeden gizlemek, kendi seçimini engellemek olurdu.
//
// Not: bu bir GÜVENLİK onayı listesi DEĞİL. Hiçbir paketi denetlemiş
// değiliz; "önerilen" diye bir liste tutmuyoruz çünkü arkasında duramayız.
var cakisanlar = map[string]string{
	"pi-mcp-adapter": "OpenAssist MCP yüzeylerini zaten oa-gateway ile takıyor (mcp__<yüzey>__<araç> adlarıyla)",
	"pi-web-access":  "web okuma OpenAssist'in kendi aracında — onay kapısından geçiyor",
	"pi-memory":      "hafıza OpenAssist'te koşu sonrası çıkarımla tutuluyor (K20)",
	"pi-subagents":   "işi başka bota devretme ask_bot ile yapılıyor (ADIM 6)",
}

type paketKatalogDTO struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Publisher   string `json:"publisher,omitempty"`
	// Source — kurulum gövdesine AYNEN konur; istemci kaynak dizesi kurmaz.
	Source string `json:"source"`
	// MonthlyDownloads — popülerlik tek ölçüt değil ama tek ölçülebilir olan.
	MonthlyDownloads int `json:"monthly_downloads"`
	// Installed — bu paket sunucuda kurulu mu (kimlik sürümsüz addır).
	Installed bool `json:"installed"`
	// ConflictNote — OpenAssist'te karşılığı varsa sebebi; boşsa çakışma yok.
	ConflictNote string `json:"conflict_note,omitempty"`
	NpmURL       string `json:"npm_url"`
}

type npmAramaYanit struct {
	Total   int `json:"total"`
	Objects []struct {
		Package struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
			Publisher   struct {
				Username string `json:"username"`
			} `json:"publisher"`
			Links struct {
				NPM string `json:"npm"`
			} `json:"links"`
		} `json:"package"`
		Downloads struct {
			Monthly int `json:"monthly"`
		} `json:"downloads"`
	} `json:"objects"`
}

type paketKatalogOnbellek struct {
	mu       sync.Mutex
	kayit    map[string]paketKatalogGirdi
	tekSefer sync.Mutex
}

type paketKatalogGirdi struct {
	veri []paketKatalogDTO
	at   time.Time
}

// GET /api/extensions/catalog?q=…
//
// Arama SUNUCUDAN yapılır, Mac'ten değil: uygulama yalnız OpenAssist'e bağlanır
// (Modeller kataloğunda verilen kararın aynısı) ve önbellek tek yerde durur.
func (a *api) extensionCatalog(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	veri, err := a.paketKatalog.al(r.Context(), q, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeErr(w, http.StatusBadGateway, "paket kataloğu alınamadı: "+err.Error())
		return
	}

	// Kurulu olanlar işaretlenir: aynı paketi ikinci kez kurmaya çalışmak
	// kullanıcının zamanını yer, üstelik "kurdum mu" sorusu ekranda kalır.
	kurulu := map[string]bool{}
	for _, id := range motorAdlari(a.engines) {
		ex, ok := a.engines[id].(engine.Extender)
		if !ok {
			continue
		}
		list, err := ex.Extensions(r.Context())
		if err != nil {
			continue
		}
		for _, e := range list {
			kurulu[e.ID] = true
		}
	}
	out := make([]paketKatalogDTO, 0, len(veri))
	for _, p := range veri {
		p.Installed = kurulu["npm:"+p.Name]
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (c *paketKatalogOnbellek) al(ctx context.Context, q string, zorla bool) ([]paketKatalogDTO, error) {
	c.mu.Lock()
	girdi, varsa := c.kayit[q]
	c.mu.Unlock()
	if !zorla && varsa && time.Since(girdi.at) < paketKatalogTTL {
		return girdi.veri, nil
	}

	c.tekSefer.Lock()
	defer c.tekSefer.Unlock()
	c.mu.Lock()
	girdi, varsa = c.kayit[q]
	c.mu.Unlock()
	if !zorla && varsa && time.Since(girdi.at) < paketKatalogTTL {
		return girdi.veri, nil
	}

	yeni, err := npmPaketAra(ctx, q)
	if err != nil {
		// Bayat veri hiç veriden iyidir: npm düştüğünde ekran çalışsın.
		if varsa {
			return girdi.veri, nil
		}
		return nil, err
	}
	c.mu.Lock()
	if c.kayit == nil {
		c.kayit = map[string]paketKatalogGirdi{}
	}
	c.kayit[q] = paketKatalogGirdi{veri: yeni, at: time.Now()}
	c.mu.Unlock()
	return yeni, nil
}

func npmPaketAra(ctx context.Context, q string) ([]paketKatalogDTO, error) {
	ctx, cancel := context.WithTimeout(ctx, paketKatalogTimeout)
	defer cancel()

	// Etiket SORGUYA EKLENİR, sorgunun yerine geçmez: kullanıcı "pdf" yazınca
	// npm'in tamamında değil, pi paketleri içinde arıyor olmalı.
	metin := npmAramaEtiketi
	if q != "" {
		metin += " " + q
	}
	adres := npmAramaURL + "?" + url.Values{
		"text": {metin},
		"size": {fmt.Sprint(paketKatalogBoyut)},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adres, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("npm HTTP " + res.Status)
	}

	var ham npmAramaYanit
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, paketKatalogMaxBayt)).Decode(&ham); err != nil {
		return nil, err
	}
	return npmSonuclariCevir(ham), nil
}

// npmSonuclariCevir — npm yanıtını ekran satırına indirger. Saf fonksiyon:
// kaynak dizesinin kuruluşu (sürüm sabitleme) ve çakışma notu testte ağa
// çıkmadan sınanabilsin.
func npmSonuclariCevir(ham npmAramaYanit) []paketKatalogDTO {
	out := make([]paketKatalogDTO, 0, len(ham.Objects))
	for _, o := range ham.Objects {
		p := o.Package
		if p.Name == "" || p.Version == "" {
			continue
		}
		out = append(out, paketKatalogDTO{
			Name: p.Name, Version: p.Version, Description: p.Description,
			Publisher: p.Publisher.Username,
			// SÜRÜM SABİTLENİR. "npm:paket" yazsaydık her kurulum o günkü
			// son sürümü çekerdi ve iki kurulum aynı şeyi vermezdi; pi de
			// sürümlü kaynakları güncellemelerde atlıyor.
			Source:           "npm:" + p.Name + "@" + p.Version,
			MonthlyDownloads: o.Downloads.Monthly,
			ConflictNote:     cakisanlar[p.Name],
			NpmURL:           p.Links.NPM,
		})
	}
	return out
}
