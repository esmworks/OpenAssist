package memory

import (
	"bufio"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Markdown dışa/içe aktarma (K20).
//
// Bu bir YEDEK DEĞİL — yedek `deploy/backup.sh` içindeki `pg_dump`. Buradaki
// biçim insanın gözüyle denetlemesi ve elle düzeltmesi içindir: dogfooding
// sırasında "bot benim hakkımda ne biliyor" sorusunun en hızlı cevabı.
//
// Bu yüzden içe aktarma id/revizyon geri YÜKLEMEZ: her satır yeni bir öneri
// gibi normal yazma yolundan (SaveMemory → dedup + sır kapısı + revizyon)
// geçer. Aynı içerik zaten varsa yeni satır doğmaz. Böylece elle düzenlenmiş
// bir dosyayı geri vermek güvenlidir.

const (
	// aktarimBaslik — dosyanın ilk satırı; içe aktarmada biçim doğrulaması.
	aktarimBaslik = "# OpenAssist hafıza"
	kapsamUser    = "## user"
	botOneki      = "## bot: "
)

// DisaAktar — kayıtları insan-okur markdown'a çevirir. Bot adları çözülebilsin
// diye bot listesi de alınır; adı bilinmeyen bot id'siyle yazılır.
func DisaAktar(mems []db.Memory, bots []db.Bot, simdi time.Time) string {
	adlar := make(map[uuid.UUID]string, len(bots))
	for _, b := range bots {
		adlar[b.ID] = b.Name
	}

	var user []db.Memory
	botlu := map[string][]db.Memory{}
	for _, m := range mems {
		if m.Scope == "user" || !m.BotID.Valid {
			user = append(user, m)
			continue
		}
		ad := adlar[m.BotID.UUID]
		if ad == "" {
			ad = m.BotID.UUID.String()
		}
		botlu[ad] = append(botlu[ad], m)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n# Dışa aktarma: %s\n#\n", aktarimBaslik, simdi.UTC().Format(time.RFC3339))
	b.WriteString("# Bu dosya elle düzenlenebilir ve geri yüklenebilir. Satır biçimi:\n")
	b.WriteString("#   - [fact|preference|instruction] içerik\n")
	b.WriteString("# Köşeli parantez içine ek alan yazılabilir (varsayılan değilse\n")
	b.WriteString("# dışa aktarma da yazar):\n")
	b.WriteString("#   - [fact expires=2026-09-01 conf=0.80] içerik\n")
	b.WriteString("# İçe aktarma id/revizyon geri yüklemez — her satır yeni bir öneri\n")
	b.WriteString("# gibi işlenir (dedup ve sır taraması dahil). Yedek için pg_dump.\n")

	if len(user) > 0 {
		fmt.Fprintf(&b, "\n%s\n\n", kapsamUser)
		yazSatirlar(&b, user)
	}
	adSirali := make([]string, 0, len(botlu))
	for ad := range botlu {
		adSirali = append(adSirali, ad)
	}
	sort.Strings(adSirali)
	for _, ad := range adSirali {
		fmt.Fprintf(&b, "\n%s%s\n\n", botOneki, ad)
		yazSatirlar(&b, botlu[ad])
	}
	return b.String()
}

func yazSatirlar(b *strings.Builder, mems []db.Memory) {
	for _, m := range mems {
		// İçerik tek satıra indirgenir: çok satırlı bir olgu ayrıştırmayı
		// belirsizleştirir ve zaten "tek cümlelik olgu" sözleşmesini bozar.
		icerik := strings.Join(strings.Fields(m.Content), " ")

		// Varsayılan olmayan alanlar da taşınır. Taşınmasaydı dışa aktarılıp
		// geri yüklenen GEÇİCİ bir olgu ("kullanıcı bu hafta İzmir'de")
		// kalıcılaşırdı: içerik aynı olduğu için upsert var olan kaydın
		// expires_at'ini de NULL ile ezer ve olgu haftalar sonra hâlâ promptta
		// durur.
		alanlar := m.Kind
		if m.ExpiresAt != nil {
			alanlar += " expires=" + m.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if m.Confidence > 0 && m.Confidence < 1 {
			alanlar += fmt.Sprintf(" conf=%.2f", m.Confidence)
		}
		fmt.Fprintf(b, "- [%s] %s\n", alanlar, icerik)
	}
}

// AktarimSatiri — içe aktarmada çözülen tek kayıt.
type AktarimSatiri struct {
	Scope      string // user | bot
	BotAdi     string // scope=bot ise
	Kind       string
	Content    string
	Confidence float32    // 0 = varsayılan (1)
	ExpiresAt  *time.Time // nil = süresiz
}

// basligiCoz — köşeli parantez içini çözer: ilk sözcük tür, kalanı ad=değer
// çiftleri. Bilinmeyen anahtar hata verir (dosyanın geri kalanı gibi: sessizce
// atlamak, kullanıcının yazdığı alanın kaybolduğunu gizlerdi).
func basligiCoz(govde string, satirNo int) (AktarimSatiri, error) {
	parcalar := strings.Fields(govde)
	if len(parcalar) == 0 {
		return AktarimSatiri{}, fmt.Errorf("%d. satır: tür boş", satirNo)
	}
	var s AktarimSatiri
	switch parcalar[0] {
	case "fact", "preference", "instruction":
		s.Kind = parcalar[0]
	default:
		return AktarimSatiri{}, fmt.Errorf("%d. satır: bilinmeyen tür %q (fact|preference|instruction)", satirNo, parcalar[0])
	}
	for _, p := range parcalar[1:] {
		ad, deger, ok := strings.Cut(p, "=")
		if !ok {
			return AktarimSatiri{}, fmt.Errorf("%d. satır: %q ad=değer biçiminde değil", satirNo, p)
		}
		switch ad {
		case "expires":
			t, err := zamanCoz(deger)
			if err != nil {
				return AktarimSatiri{}, fmt.Errorf("%d. satır: expires okunamadı (%q) — 2026-09-01 ya da RFC3339 bekleniyor", satirNo, deger)
			}
			s.ExpiresAt = &t
		case "conf":
			v, err := strconv.ParseFloat(deger, 32)
			if err != nil || v <= 0 || v > 1 {
				return AktarimSatiri{}, fmt.Errorf("%d. satır: conf 0-1 aralığında olmalı (%q)", satirNo, deger)
			}
			s.Confidence = float32(v)
		default:
			return AktarimSatiri{}, fmt.Errorf("%d. satır: bilinmeyen alan %q (expires|conf)", satirNo, ad)
		}
	}
	return s, nil
}

// zamanCoz — tam zaman damgası ya da yalnız tarih. Tarih hâli insanın elle
// yazacağı biçim; dışa aktarma tam damgayı yazar ki gün içi hassasiyet kaybolmasın.
func zamanCoz(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

// IceAktarCoz — markdown'ı satırlara çevirir. Ayrıştırma bilinçli olarak
// hoşgörüsüz: tanınmayan başlık ya da bozuk satır sessizce atlanmaz, hata
// döner. Kullanıcının elle düzenlediği dosyanın yarısının sessizce
// yüklenmesi, hiç yüklenmemesinden kötüdür.
func IceAktarCoz(metin string) ([]AktarimSatiri, error) {
	sc := bufio.NewScanner(strings.NewReader(metin))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var out []AktarimSatiri
	scope, botAdi := "", ""
	satirNo := 0
	basliklıMı := false

	for sc.Scan() {
		satirNo++
		ham := strings.TrimSpace(sc.Text())
		switch {
		case ham == "":
			continue
		case strings.HasPrefix(ham, aktarimBaslik):
			basliklıMı = true
		case ham == kapsamUser:
			scope, botAdi = "user", ""
		case strings.HasPrefix(ham, botOneki):
			ad := strings.TrimSpace(strings.TrimPrefix(ham, botOneki))
			if ad == "" {
				return nil, fmt.Errorf("%d. satır: bot adı boş", satirNo)
			}
			scope, botAdi = "bot", ad
		case strings.HasPrefix(ham, "#"):
			continue // yorum
		case strings.HasPrefix(ham, "- ["):
			if scope == "" {
				return nil, fmt.Errorf("%d. satır: kapsam başlığı yok (önce '## user' ya da '## bot: <ad>' gelmeli)", satirNo)
			}
			kapanis := strings.Index(ham, "]")
			if kapanis < 0 {
				return nil, fmt.Errorf("%d. satır: kapanmamış tür köşeli parantezi", satirNo)
			}
			satir, err := basligiCoz(ham[3:kapanis], satirNo)
			if err != nil {
				return nil, err
			}
			satir.Scope, satir.BotAdi = scope, botAdi
			satir.Content = strings.TrimSpace(ham[kapanis+1:])
			if satir.Content == "" {
				return nil, fmt.Errorf("%d. satır: içerik boş", satirNo)
			}
			out = append(out, satir)
		default:
			return nil, fmt.Errorf("%d. satır anlaşılmadı: %.60q", satirNo, ham)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !basliklıMı {
		return nil, fmt.Errorf("dosya %q ile başlamalı — yanlış dosya verilmiş olabilir", aktarimBaslik)
	}
	return out, nil
}
