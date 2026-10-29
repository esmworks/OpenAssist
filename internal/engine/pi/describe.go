package pi

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/clipath"
)

var (
	_ engine.Describer = (*Adapter)(nil)
	_ engine.Prober    = (*Adapter)(nil)
)

func (a *Adapter) DisplayName() string { return "Pi" }

// Models — yapılandırılmış sağlayıcı tarifinden gelen liste. Elle yazılmış
// sabit ad YOKTUR: liste ya bizim tarifimizden ya da yoklamanın ikiliden
// ölçtüğü Status.Models'ten gelir (K18 — bayat liste her seçimde koşu düşürür).
func (a *Adapter) Models() []engine.ModelInfo {
	out := []engine.ModelInfo{{ID: "", Label: "Varsayılan — motorun kendi seçimi"}}
	// Burada hata KOŞU DÜŞÜRMEZ: bu yalnız açılır listeyi dolduran bir
	// okuma. Sebep loglanır, liste kısa kalır.
	ps, err := a.saglayicilar(context.Background())
	if err != nil {
		slog.Warn("pi model listesi için sağlayıcılar okunamadı", "err", err)
		return out
	}
	return append(out, modelInfolar(ps)...)
}

const probeTimeout = 20 * time.Second

// Probe — pi kurulu mu, hangi sürüm, hangi modeller gerçekten seçilebilir.
//
// Model listesi `--list-models`ten okunur çünkü liste KİMLİĞE BAĞLIDIR:
// anahtarsız kurulumda sağlayıcı hiç görünmez. Yalnız çalışma anında
// bilinebilen bir şeyi sabit listede tutmak, opencode'da her seçimin koşuyu
// düşürmesine yol açmıştı.
func (a *Adapter) Probe(ctx context.Context) engine.Status {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var st engine.Status
	bin := a.cli()

	// TEK okuma: aşağıdaki iki komut da bu listeden kurulan ortamı alır,
	// models.json da aynı listeden yazıldı.
	ps, err := a.hazirla(ctx)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	env := a.cleanEnv(bin, ps)

	sur := exec.CommandContext(ctx, bin, "--version")
	sur.Env = env
	out, err := sur.Output()
	if err != nil {
		st.Detail = "pi " + clipath.BulunamadiDetay(bin, err)
		return st
	}
	st.Available = true
	st.Version = strings.TrimSpace(string(out))

	// stderr'i YUTMUYORUZ. pi models.json'ı okuyamazsa sebebi tam buraya
	// yazıp çıkışa 0 ile döner ("Warning: errors loading models.json: …")
	// — Output() yalnız stdout aldığı için o cümle kayboluyordu ve ekranda
	// sebepsiz bir "model yok" kalıyordu. Yoklamanın işi zaten SEBEBİ
	// söylemek; motorun kendi teşhisinden iyisi yok.
	liste := exec.CommandContext(ctx, bin, "--list-models")
	liste.Env = env
	var lout, lerr bytes.Buffer
	liste.Stdout, liste.Stderr = &lout, &lerr
	uyari := func() string { return sirlariGizle(kuyruk(strings.TrimSpace(lerr.String()), 500), ps) }
	if err := liste.Run(); err != nil {
		st.Detail = "pi kurulu ama model listesi alınamadı: " + err.Error()
		if u := uyari(); u != "" {
			st.Detail += " — " + u
		}
		return st
	}

	st.Models = modelleriAyristir(lout.String())
	if len(st.Models) == 0 {
		st.Detail = "pi kurulu ama seçilebilir model yok — " + modelYok
		if u := uyari(); u != "" {
			// pi'nin kendi sebebi varsa GENEL cümlenin yerini alsın:
			// "models.json okunamadı" ile "sağlayıcı ekle" bambaşka işler.
			st.Detail = "pi modelleri okuyamadı — " + u
		}
		slog.Warn("pi seçilebilir model bulamadı",
			"models_json", filepath.Join(a.ConfigDir, "models.json"),
			"tarif", len(ps), "model", modelSayisi(ps), "pi_stderr", uyari())
		return st
	}

	st.LoggedIn = true
	st.Account = saglayiciAdlari(ps)
	st.Detail = "kurulu — " + strconv.Itoa(len(st.Models)) + " model seçilebilir"
	return st
}

// saglayiciAdlari — yoklamada gösterilecek kimlik satırı.
func saglayiciAdlari(ps []ProviderConfig) string {
	adlar := make([]string, 0, len(ps))
	for _, p := range ps {
		ad := p.Name
		if ad == "" {
			ad = p.ID
		}
		adlar = append(adlar, ad)
	}
	return strings.Join(adlar, ", ")
}

// modelleriAyristir — `pi --list-models` tablosunu okur. Biçim:
//
//	provider         model                   context  max-out  thinking  images
//	qwen-token-plan  deepseek-v4-flash       1M       384K     yes       no
//
// Yalnız ilk iki sütun bizi ilgilendirir; kimlik pi'nin beklediği
// "provider/model" biçiminde kurulur (--model bu deseni kabul eder).
//
// BAŞLIK ZORUNLU: hiç sağlayıcı yapılandırılmamışken pi tablo yerine düz bir
// cümle basıyor ve "her satır bir modeldir" varsayımı o cümleyi model sanıp
// ekrana "No/models" diye yazıyordu. Başlığı görene kadar hiçbir satır
// okunmaz — tanımadığımız bir çıktıyı modelmiş gibi göstermektense hiç
// model göstermemek doğru.
func modelleriAyristir(out string) []engine.ModelInfo {
	var models []engine.ModelInfo
	basladi := false
	for _, satir := range satirlar(out) {
		alan := strings.Fields(satir)
		if !basladi {
			if len(alan) >= 2 && alan[0] == "provider" && alan[1] == "model" {
				basladi = true
			}
			continue
		}
		if len(alan) < 2 {
			continue
		}
		id := alan[0] + "/" + alan[1]
		models = append(models, engine.ModelInfo{ID: id, Label: id})
	}
	return models
}
