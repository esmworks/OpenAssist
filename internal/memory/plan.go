package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Plan — motorun döndürdüğü konsolidasyon planı. Kontrat dar tutuldu: model
// YENİ kayıt yaratamaz, yalnız verdiğimiz kayıtları birleştirebilir, eleyebilir
// ve bir özet yazabilir. "Yeni hafıza öner" yolu bilinçli olarak yok — hafıza
// yazma yetkisi ajanın koşu içindeki `memory_save` önerisinde kalır (K20).
type Plan struct {
	Birlestir []Birlestirme `json:"birlestir"`
	Ele       []Eleme       `json:"ele"`
	Ozet      string        `json:"ozet"`
}

type Birlestirme struct {
	// Tut — hangi kaydın kalacağı (içeriği Icerik ile değişir).
	Tut string `json:"tut"`
	// Kat — bu kayda katılıp emekliye ayrılacak kayıtlar.
	Kat []string `json:"kat"`
	// Icerik — birleşik olgunun yeni metni.
	Icerik  string `json:"icerik"`
	Gerekce string `json:"gerekce"`
}

type Eleme struct {
	ID      string `json:"id"`
	Gerekce string `json:"gerekce"`
}

// planCoz — motorun serbest metninden JSON'u ayıklar. Motor markdown çiti
// (```json) ya da kısa bir giriş cümlesi ekleyebilir; ilk `{` ile son `}`
// arasını alıyoruz. Kontratı katı tutuyoruz (DisallowUnknownFields): plan
// şemasında olmayan bir alan gelirse model başka bir şey anlamış demektir,
// sessizce yarısını uygulamaktansa turu düşürmek doğru.
func planCoz(metin string) (Plan, error) {
	bas := strings.Index(metin, "{")
	son := strings.LastIndex(metin, "}")
	if bas < 0 || son < bas {
		return Plan{}, fmt.Errorf("motor JSON plan döndürmedi: %.200q", metin)
	}
	dec := json.NewDecoder(strings.NewReader(metin[bas : son+1]))
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("plan çözülemedi: %w", err)
	}
	return p, nil
}

// CozulmusPlan — doğrulamadan geçmiş plan; id'ler artık metin değil uuid.UUID.
//
// Ayrı bir tip olmasının nedeni tekniğin ötesinde: uygulama adımı yalnız bu
// tipi kabul ederse, doğrulamadan geçmemiş bir plan derleyiciden geçemez.
// Önceki halde uygulama adımı ham metni `uuid.MustParse` ile çözüyordu —
// bugün güvenliydi (doğrulama hepsini ayrıştırıyor), ama şema genişletildiğinde
// ya da bir dal atlandığında panic arka plan goroutine'inde core sürecinin
// tamamını (orkestratör, rutin motoru, Telegram) götürürdü.
type CozulmusPlan struct {
	Birlestir []CozulmusBirlestirme
	Ele       []CozulmusEleme
	Ozet      string
}

type CozulmusBirlestirme struct {
	Tut     uuid.UUID
	Kat     []uuid.UUID
	Icerik  string
	Gerekce string
}

type CozulmusEleme struct {
	ID      uuid.UUID
	Gerekce string
}

// planDogrula — planı UYGULAMADAN ÖNCE kilitler ve id'leri çözer.
//
// Burada bir model kullanıcının hafızasını yeniden yazıyor; kontrolü koda
// bırakmak zorundayız:
//
//  1. Her id BİZİM verdiğimiz listede olmalı — model başka bir botun ya da
//     uydurduğu bir kaydın üzerine yazamaz.
//  2. Bir kayıt hem tutulup hem katılamaz, iki kez elenemez — çelişkili plan
//     yarısı uygulanmış bir hafıza bırakır.
//  3. Tek turda kayıtların yarısından fazlası eleniyorsa plan TÜMDEN reddedilir:
//     bozuk/yanlış anlamış bir tur hafızayı boşaltmasın. Eşik aşılırsa
//     kullanıcı bir sonraki turda daha küçük bir planla ilerler.
func planDogrula(p Plan, mems []db.Memory) (CozulmusPlan, error) {
	gecerli := make(map[string]bool, len(mems))
	for _, m := range mems {
		gecerli[m.ID.String()] = true
	}

	gorulen := make(map[string]string) // id → nerede kullanıldı
	kullan := func(id, yer string) (uuid.UUID, error) {
		coz, err := uuid.Parse(id)
		if err != nil {
			return uuid.Nil, fmt.Errorf("geçersiz id %q (%s)", id, yer)
		}
		if !gecerli[id] {
			return uuid.Nil, fmt.Errorf("plan listede olmayan kayda dokunuyor: %s (%s)", id, yer)
		}
		if onceki, ok := gorulen[id]; ok {
			return uuid.Nil, fmt.Errorf("kayıt iki kez kullanıldı: %s (%s ve %s)", id, onceki, yer)
		}
		gorulen[id] = yer
		return coz, nil
	}

	out := CozulmusPlan{Ozet: p.Ozet}
	dusen := 0
	for _, m := range p.Birlestir {
		tut, err := kullan(m.Tut, "birlestir.tut")
		if err != nil {
			return CozulmusPlan{}, err
		}
		if strings.TrimSpace(m.Icerik) == "" {
			return CozulmusPlan{}, fmt.Errorf("birleştirmenin içeriği boş: %s", m.Tut)
		}
		if len(m.Kat) == 0 {
			return CozulmusPlan{}, fmt.Errorf("birleştirme kimseyi katmıyor: %s", m.Tut)
		}
		cb := CozulmusBirlestirme{Tut: tut, Icerik: m.Icerik, Gerekce: m.Gerekce}
		for _, k := range m.Kat {
			kat, err := kullan(k, "birlestir.kat")
			if err != nil {
				return CozulmusPlan{}, err
			}
			cb.Kat = append(cb.Kat, kat)
			dusen++
		}
		out.Birlestir = append(out.Birlestir, cb)
	}
	for _, d := range p.Ele {
		id, err := kullan(d.ID, "ele")
		if err != nil {
			return CozulmusPlan{}, err
		}
		out.Ele = append(out.Ele, CozulmusEleme{ID: id, Gerekce: d.Gerekce})
		dusen++
	}

	if len(mems) > 0 && dusen*2 > len(mems) {
		return CozulmusPlan{}, fmt.Errorf("plan %d/%d kaydı düşürüyor — yarıdan fazlası, tümden reddedildi",
			dusen, len(mems))
	}
	if strings.TrimSpace(p.Ozet) == "" {
		return CozulmusPlan{}, errors.New("plan özet üretmedi")
	}
	return out, nil
}
