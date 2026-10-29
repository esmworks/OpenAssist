package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

// Hafıza yazma yolu (K20).
//
// Kural: hafıza satırı ile revizyon satırı BİRLİKTE yazılır. Bu yüzden
// `Q.UpsertMemory` / `Q.ReviseMemoryContent` doğrudan çağrılmaz — giriş
// noktası buradaki `MemoryWriter` ve onu saran kolaylık fonksiyonlarıdır.
// Geçmişi yazma sorumluluğu çağırana bırakılırsa er geç bir yol onu atlar ve
// o kaydın geçmişi sessizce eksik kalır; konsolidasyon turu da tam o kaydı
// geri alınamaz şekilde ezer.
//
// İkinci kural: ÇOK KAYITLI yazımlar tek transaction'da olur. Konsolidasyon
// planı ve markdown içe aktarma birer bütündür — yarısı uygulanmış bir hafıza,
// hiç uygulanmamıştan kötüdür (kullanıcı neyin girdiğini bilemez). `InMemoryTx`
// bunun için var; tek satırlık yazımlar da aynı yoldan geçer (tek elemanlı
// transaction bedava).
//
// content_hash da burada hesaplanır: dedup anahtarı içerikle birlikte
// değişmek zorunda, iki yerde hesaplanırsa bir gün ayrışır.

// Yazar kimlikleri — kaydı kim değiştirdi (memory_revisions.author).
const (
	// YazarAgent — ajanın koşu içindeki `memory_save` önerisi.
	YazarAgent = "agent"
	// YazarKonsolidasyon — gece konsolidasyon turu.
	YazarKonsolidasyon = "consolidation"
	// YazarKullanici — geri alma ya da elle düzeltme.
	YazarKullanici = "user"
)

// MemoryDraft — hafıza önerisi. content_hash yok: onu store hesaplar.
type MemoryDraft struct {
	// Scope — "user" (tüm botlar görür) | "bot" (yalnız BotID).
	Scope       string
	BotID       uuid.NullUUID
	Kind        string
	Content     string
	SourceRunID uuid.NullUUID
	Confidence  float32
	ExpiresAt   *time.Time
}

func icerikHash(icerik string) string {
	toplam := sha256.Sum256([]byte(icerik))
	return hex.EncodeToString(toplam[:])
}

// InTx — fn'i tek transaction'da koşturur; hata dönerse geri sarar.
// Codebase'deki tek transaction sarmalayıcısı: çok satırlı yazımlar buradan
// geçer, tek satırlıklar doğrudan Q'yu kullanmaya devam eder.
func (s *Store) InTx(ctx context.Context, fn func(q *sqlcdb.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: transaction açılamadı: %w", err)
	}
	// Başarılı yolda Commit çalışır ve bu Rollback "already committed"
	// döner — pgx bunu zararsız sayar, bilinçli olarak yutuyoruz.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(s.Q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: transaction yazılamadı: %w", err)
	}
	return nil
}

// MemoryWriter — transaction'a bağlı hafıza yazarı. Doğrudan yaratılmaz;
// `InMemoryTx` verir. Bir yazarın tüm çağrıları aynı transaction'dadır: ilki
// yazıp ikincisi patlarsa hiçbiri kalıcı olmaz.
type MemoryWriter struct{ q *sqlcdb.Queries }

// InMemoryTx — hafıza yazımlarını tek transaction'da toplar.
func (s *Store) InMemoryTx(ctx context.Context, fn func(w *MemoryWriter) error) error {
	return s.InTx(ctx, func(q *sqlcdb.Queries) error { return fn(&MemoryWriter{q: q}) })
}

// Save — ajan önerisini kalıcılaştırır (dedup + 1. revizyon).
//
// Aynı içerik daha önce kaydedilmişse yeni satır doğmaz; güven/süre tazelenir
// ve kayıt "katılmış" durumdaysa diriltilir. O durumda revizyon sayacı
// artmadığı için geçmişe de yeni satır yazılmaz (RecordMemoryRevision'daki
// DO NOTHING).
func (w *MemoryWriter) Save(ctx context.Context, d MemoryDraft) (sqlcdb.Memory, error) {
	d.Content = strings.TrimSpace(d.Content)
	if d.Content == "" {
		return sqlcdb.Memory{}, errors.New("hafıza içeriği boş olamaz")
	}
	if err := SirTara(d.Content); err != nil {
		return sqlcdb.Memory{}, err
	}
	if d.Confidence <= 0 || d.Confidence > 1 {
		d.Confidence = 1
	}
	hash := icerikHash(d.Content)

	var (
		mem sqlcdb.Memory
		err error
	)
	switch d.Scope {
	case "user":
		mem, err = w.q.UpsertUserMemory(ctx, sqlcdb.UpsertUserMemoryParams{
			Kind: d.Kind, Content: d.Content, ContentHash: hash,
			SourceRunID: d.SourceRunID, Confidence: d.Confidence, ExpiresAt: d.ExpiresAt,
		})
	case "bot":
		if !d.BotID.Valid {
			return sqlcdb.Memory{}, errors.New("bot scope'unda bot_id zorunlu")
		}
		mem, err = w.q.UpsertMemory(ctx, sqlcdb.UpsertMemoryParams{
			Scope: "bot", BotID: d.BotID, Kind: d.Kind, Content: d.Content, ContentHash: hash,
			SourceRunID: d.SourceRunID, Confidence: d.Confidence, ExpiresAt: d.ExpiresAt,
		})
	default:
		return sqlcdb.Memory{}, fmt.Errorf("scope user|bot olmalı, %q geldi", d.Scope)
	}
	if err != nil {
		return sqlcdb.Memory{}, err
	}
	if err := w.q.RecordMemoryRevision(ctx, sqlcdb.RecordMemoryRevisionParams{
		MemoryID: mem.ID, Revision: mem.Revision, Content: mem.Content,
		Author: YazarAgent, SourceRunID: d.SourceRunID,
	}); err != nil {
		return sqlcdb.Memory{}, err
	}
	return mem, nil
}

// Revise — var olan kaydın içeriğini değiştirir ve geçmişe yazar.
// Konsolidasyon turu ve geri alma bu yoldan geçer.
//
// Çakışma (aynı kapsamda aynı içerik zaten var) anlaşılır hataya çevrilir:
// çağıran ya birleştirmeyi ya geri almayı yeniden düşünmeli, ham SQLSTATE
// görmemeli.
func (w *MemoryWriter) Revise(ctx context.Context, id uuid.UUID, icerik, yazar, not string, runID uuid.NullUUID) (sqlcdb.Memory, error) {
	icerik = strings.TrimSpace(icerik)
	if icerik == "" {
		return sqlcdb.Memory{}, errors.New("hafıza içeriği boş olamaz")
	}
	// Konsolidasyon turu da buradan geçer: birleştirilmiş metne sır sızarsa
	// ajan önerisinden farkı yok.
	if err := SirTara(icerik); err != nil {
		return sqlcdb.Memory{}, err
	}
	switch yazar {
	case YazarAgent, YazarKonsolidasyon, YazarKullanici:
	default:
		return sqlcdb.Memory{}, fmt.Errorf("bilinmeyen yazar: %q", yazar)
	}

	mem, err := w.q.ReviseMemoryContent(ctx, sqlcdb.ReviseMemoryContentParams{
		ID: id, Content: icerik, ContentHash: icerikHash(icerik),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcdb.Memory{}, fmt.Errorf("hafıza kaydı bulunamadı: %s", id)
		}
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "memories_dedup") {
			return sqlcdb.Memory{}, errors.New("bu içerik aynı kapsamda zaten var — birleştirme hedefi başka bir kayıt olmalı")
		}
		return sqlcdb.Memory{}, err
	}
	if err := w.q.RecordMemoryRevision(ctx, sqlcdb.RecordMemoryRevisionParams{
		MemoryID: mem.ID, Revision: mem.Revision, Content: mem.Content,
		Author: yazar, Note: not, SourceRunID: runID,
	}); err != nil {
		return sqlcdb.Memory{}, err
	}
	return mem, nil
}

// UpsertSummary — bot başına özet belgeyi yazar. Konsolidasyon planıyla AYNI
// transaction'da çalışsın diye burada: kayıtlar birleşip özet yazılamazsa
// prompt hafızanın artık var olmayan haline atıfta bulunurdu.
func (w *MemoryWriter) UpsertSummary(ctx context.Context, botID uuid.UUID, icerik string, kaynakSayisi int32) (sqlcdb.MemorySummary, error) {
	return w.q.UpsertMemorySummary(ctx, sqlcdb.UpsertMemorySummaryParams{
		BotID: botID, Content: icerik, SourceCount: kaynakSayisi,
	})
}

// Supersede — kaydı "emekliye ayrıldı" diye işaretler. Silme değil: satır ve
// geçmişi durur, yalnız prompta ve listelere girmez. katilan geçerliyse
// "şu kayda katıldı" izi kalır.
func (w *MemoryWriter) Supersede(ctx context.Context, id uuid.UUID, katilan uuid.NullUUID) error {
	return w.q.SupersedeMemory(ctx, sqlcdb.SupersedeMemoryParams{ID: id, SupersededBy: katilan})
}

// SaveMemory — tek öneriyi kalıcılaştırır (kolaylık sarmalayıcısı).
func (s *Store) SaveMemory(ctx context.Context, d MemoryDraft) (sqlcdb.Memory, error) {
	var mem sqlcdb.Memory
	err := s.InMemoryTx(ctx, func(w *MemoryWriter) error {
		var err error
		mem, err = w.Save(ctx, d)
		return err
	})
	if err != nil {
		return sqlcdb.Memory{}, err
	}
	return mem, nil
}

// SaveMemories — bir listeyi YA HEP YA HİÇ yazar. Markdown içe aktarmanın
// giriş noktası: 40 satırlık bir dosyanın 30. satırı sır kapısına takılırsa
// ilk 29'u da yazılmamış olur, kullanıcı dosyayı düzeltip yeniden gönderdiğinde
// neyin girdiğini merak etmez.
func (s *Store) SaveMemories(ctx context.Context, drafts []MemoryDraft) ([]sqlcdb.Memory, error) {
	out := make([]sqlcdb.Memory, 0, len(drafts))
	err := s.InMemoryTx(ctx, func(w *MemoryWriter) error {
		out = out[:0]
		for _, d := range drafts {
			mem, err := w.Save(ctx, d)
			if err != nil {
				return fmt.Errorf("%q → %w", d.Content, err)
			}
			out = append(out, mem)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReviseMemory — tek kaydın içeriğini değiştirir (kolaylık sarmalayıcısı).
func (s *Store) ReviseMemory(ctx context.Context, id uuid.UUID, icerik, yazar, not string, runID uuid.NullUUID) (sqlcdb.Memory, error) {
	var mem sqlcdb.Memory
	err := s.InMemoryTx(ctx, func(w *MemoryWriter) error {
		var err error
		mem, err = w.Revise(ctx, id, icerik, yazar, not, runID)
		return err
	})
	if err != nil {
		return sqlcdb.Memory{}, err
	}
	return mem, nil
}

// RevertMemory — kaydı eski bir revizyonuna döndürür. Sayaç geri sarılmaz:
// geri alma da yeni bir revizyon yazar, böylece "ne zaman neye döndük" izi
// kalır (append-only geçmiş).
func (s *Store) RevertMemory(ctx context.Context, id uuid.UUID, revizyon int32) (sqlcdb.Memory, error) {
	eski, err := s.Q.GetMemoryRevision(ctx, sqlcdb.GetMemoryRevisionParams{MemoryID: id, Revision: revizyon})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcdb.Memory{}, fmt.Errorf("%d numaralı revizyon yok", revizyon)
		}
		return sqlcdb.Memory{}, err
	}
	not := fmt.Sprintf("%d numaralı revizyona geri alındı", revizyon)
	return s.ReviseMemory(ctx, id, eski.Content, YazarKullanici, not, uuid.NullUUID{})
}

// RestoreMemory — emekliye ayrılmış (superseded) kaydı geri getirir.
//
// K20'nin "birleştirme geri alınabilir" vaadinin ikinci yarısı: `RevertMemory`
// hedefin İÇERİĞİNİ eski haline döndürür, bu ise katılan kaydı yeniden
// yaşayanlar arasına alır. İkisi olmadan kötü bir konsolidasyon turundan tam
// dönüş mümkün değil.
func (s *Store) RestoreMemory(ctx context.Context, id uuid.UUID) (sqlcdb.Memory, error) {
	mem, err := s.Q.GetMemory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcdb.Memory{}, fmt.Errorf("hafıza kaydı bulunamadı: %s", id)
		}
		return sqlcdb.Memory{}, err
	}
	if mem.SupersededAt == nil {
		return mem, nil // zaten yaşıyor — geri getirme boş iş, hata değil
	}
	if err := s.Q.RestoreMemory(ctx, id); err != nil {
		return sqlcdb.Memory{}, err
	}
	return s.Q.GetMemory(ctx, id)
}

// hafizaKilitTuzu — advisory kilit anahtarını hafıza konsolidasyonuna
// özelleştirir. Başka bir iş de bot id'sinden anahtar türetirse tuz olmadan
// aynı sayıya düşer ve iki alakasız iş birbirini kilitler.
const hafizaKilitTuzu uint64 = 0x484146495A41 // "HAFIZA"

// HafizaKilidi — bot başına konsolidasyon kilidinin anahtarı.
func HafizaKilidi(botID uuid.UUID) int64 {
	return int64(hafizaKilitTuzu ^ binary.BigEndian.Uint64(botID[:8]))
}

// TryAdvisoryLock — Postgres OTURUM kilidi dener. Kilit başkasındaysa
// ok=false döner (beklemez).
//
// Neden var: gece konsolidasyonu gibi uzun süren bakım işleri tek kopya
// varsayar (compose replicas:1), ama deploy sırasında eski ve yeni kap kısa
// süre birlikte koşar. Rutin motoru aynı riski `FOR UPDATE SKIP LOCKED` ile
// karşılıyor; kuyruk satırı olmayan işler için karşılığı budur.
//
// Kilit BAĞLANTIYA bağlıdır: bağlantı iş bitene kadar havuzdan ayrı tutulur,
// dönen fonksiyon kilidi bırakıp bağlantıyı geri verir. Süreç çökerse
// bağlantı kapanır ve kilit Postgres tarafından otomatik düşer.
func (s *Store) TryAdvisoryLock(ctx context.Context, key int64) (release func(), ok bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("store: kilit bağlantısı alınamadı: %w", err)
	}
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("store: advisory kilit denenemedi: %w", err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		// Bırakma bağlamı çağıranınkinden bağımsız: iptal edilmiş bir ctx ile
		// unlock atlanırsa kilit bağlantı kapanana kadar asılı kalırdı.
		bırakCtx, iptal := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer iptal()
		_, _ = conn.Exec(bırakCtx, "SELECT pg_advisory_unlock($1)", key)
		conn.Release()
	}, true, nil
}
