package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

// SaveChat — kullanıcı mesajını ve koşusunu TEK transaction'da yazar (ADIM 3).
// Dönüşteki created=false, bu gönderim yeni koşu ÜRETMEDİ demektir: çağıran
// kuyruğu uyandırmamalıdır.
//
// key doluysa aynı (bot, origin, key) üçlüsüyle tekrar gelindiğinde yeni satır
// yazılmaz; ilk gönderimin koşusu döner. key boşsa eski davranış korunur.
//
// Yarış güvenliği: kısa yoldaki "önce bak" kontrolü tek başına yetmezdi —
// eşzamanlı iki gönderim ikisi de "yok" görüp ikisi de yazmaya kalkardı. Asıl
// kapı ON CONFLICT'dir: çakışan taraf sıfır satır alır, transaction'ı (ve
// içindeki koşu satırını) geri sarar, kazananın koşusunu döndürür.
//
// ADIM 5: prompt ile text AYRILDI — prompt motora gider (ek bloğu dahil
// olabilir), text mesaj tablosunda görünen metindir. ekIDs doluysa ekler
// mesaja BAĞLANIR: eksik ya da başka mesajın olan eklerde transaction geri
// sarılır (ya hep ya hiç).
func (s *Store) SaveChat(ctx context.Context, bot sqlcdb.Bot, prompt, text, origin, key string, ekIDs []uuid.UUID) (sqlcdb.Run, bool, error) {
	if key != "" {
		if run, ok, err := s.chatRunByKey(ctx, bot, origin, key); err != nil || ok {
			return run, ok, err
		}
	}

	errKeyTaken := errors.New("store: idempotency anahtarı eşzamanlı gönderimde kapıldı")
	var run sqlcdb.Run
	err := s.InTx(ctx, func(q *sqlcdb.Queries) error {
		conv, err := botConversation(ctx, q, bot)
		if err != nil {
			return err
		}
		// (Bot, motor, konuşma) başına imleç (ADIM 7+10): satır yoksa boş başlar.
		imlec, err := botImleci(ctx, q, bot.ID, bot.Engine, conv.ID)
		if err != nil {
			return err
		}
		r, err := q.CreateRun(ctx, sqlcdb.CreateRunParams{
			BotID:          bot.ID,
			Prompt:         prompt,
			Engine:         bot.Engine,
			Model:          bot.Model,
			ResumeCursor:   imlec,
			Origin:         origin,
			ConversationID: uuid.NullUUID{UUID: conv.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		msg, err := q.CreateChatMessage(ctx, sqlcdb.CreateChatMessageParams{
			BotID:          bot.ID,
			ConversationID: conv.ID,
			RunID:          uuid.NullUUID{UUID: r.ID, Valid: true},
			Text:           text,
			Origin:         origin,
			IdempotencyKey: key,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errKeyTaken
			}
			return err
		}
		if len(ekIDs) > 0 {
			rows, err := q.BindAttachments(ctx, sqlcdb.BindAttachmentsParams{
				Column1:   ekIDs,
				MessageID: uuid.NullUUID{UUID: msg.ID, Valid: true},
			})
			if err != nil {
				return err
			}
			if rows != int64(len(ekIDs)) {
				return fmt.Errorf("store: ekler bağlanamadı (%d/%d)", rows, len(ekIDs))
			}
		}
		run = r
		return nil
	})
	if errors.Is(err, errKeyTaken) {
		// Kaybettik: kazanan commit etti (ON CONFLICT bekler), koşusunu bul.
		run2, ok, err2 := s.chatRunByKey(ctx, bot, origin, key)
		if err2 != nil {
			return sqlcdb.Run{}, false, err2
		}
		if !ok {
			return sqlcdb.Run{}, false, fmt.Errorf("store: %q anahtarı kapıldı ama koşusu yok", key)
		}
		return run2, false, nil
	}
	if err != nil {
		return sqlcdb.Run{}, false, err
	}
	return run, true, nil
}

// ClearChat — konuşmayı temizler ("Sohbeti Temizle"): tek transaction ek
// dosyası yollarını toplar, bu konuşmanın motor imleçlerini siler ve tüm
// mesajları siler. attachments satırları ON DELETE CASCADE ile düşer; dönen
// yollar çağıranın disk dosyalarını da silmesi içindir. İmleçler gittiği
// için bot sonraki koşuya TAZE zincirle başlar — görünür tarih ile motor
// bağlamı birbirinden ayrı düşmez.
func (s *Store) ClearChat(ctx context.Context, bot sqlcdb.Bot) ([]string, error) {
	var yollar []string
	err := s.InTx(ctx, func(q *sqlcdb.Queries) error {
		conv, err := botConversation(ctx, q, bot)
		if err != nil {
			return err
		}
		yollar, err = q.ListAttachmentPathsByConversation(ctx, conv.ID)
		if err != nil {
			return err
		}
		if err := q.DeleteBotCursorsForConversation(ctx, sqlcdb.DeleteBotCursorsForConversationParams{
			BotID: bot.ID, ConversationID: conv.ID,
		}); err != nil {
			return err
		}
		return q.DeleteConversationMessages(ctx, conv.ID)
	})
	return yollar, err
}

// BotConversation — botun sohbet kanalını döndürür; yoksa açar (ADIM 4:
// sohbet ilk kullanımda doğar, bot başına tek kanalı unique index garanti
// eder). Ekip botunun kanalı grup sohbetidir.
func (s *Store) BotConversation(ctx context.Context, bot sqlcdb.Bot) (sqlcdb.Conversation, error) {
	return botConversation(ctx, s.Q, bot)
}

// botConversation — hem havuz hem transaction sorgu yapısıyla çalışır
// (ikisi de *Queries'dir). Çakışmada (eşzamanlı iki açılış) sıfır satır
// döner; kazananın satırı tekrar seçilir.
func botConversation(ctx context.Context, q *sqlcdb.Queries, bot sqlcdb.Bot) (sqlcdb.Conversation, error) {
	kind, title := "direct", ""
	if bot.Kind == "team" {
		kind, title = "group", bot.Name
	}
	c, err := q.UpsertBotConversation(ctx, sqlcdb.UpsertBotConversationParams{
		BotID: bot.ID, Kind: kind, Title: title,
	})
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlcdb.Conversation{}, err
	}
	return q.GetBotConversation(ctx, bot.ID)
}

// chatRunByKey — anahtara bağlı ilk koşuyu getirir (ok=false: anahtar bu
// (bot, origin) çiftinde hiç kullanılmamış).
func (s *Store) chatRunByKey(ctx context.Context, bot sqlcdb.Bot, origin, key string) (sqlcdb.Run, bool, error) {
	m, err := s.Q.GetChatMessageByKey(ctx, sqlcdb.GetChatMessageByKeyParams{
		BotID: bot.ID, Origin: origin, IdempotencyKey: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlcdb.Run{}, false, nil
	}
	if err != nil {
		return sqlcdb.Run{}, false, err
	}
	if !m.RunID.Valid {
		// Koşu silinmiş (ON DELETE SET NULL): anahtar tutulmuş, koşu yok.
		return sqlcdb.Run{}, false, fmt.Errorf("store: %q anahtarının koşusu silinmiş", key)
	}
	run, err := s.Q.GetRun(ctx, m.RunID.UUID)
	if err != nil {
		return sqlcdb.Run{}, false, err
	}
	return run, true, nil
}

// DelegateChat — botlar arası devir (ADIM 6, PRD Ek B.2-6). Tek transaction:
// hedef bota origin='bot' koşusu açar (reply_to_run_id = kaynak koşu — alıcının
// cevabı çip olarak döner), mesajı GÖNDEREN damgasıyla hedef sohbetine yazar,
// kaynak sohbetine "iletildi" çipi (role='event') düşer.
//
// Kullanıcı tarafından kabul edilmesi gereken onay yoktur: ask_bot zaten
// kullanıcının botu (derinlik 0) ısrarıyla çağrılır; devir, politika
// kapısından geçen bir araç çağrısının sonucudur.
func (s *Store) DelegateChat(ctx context.Context, from, to sqlcdb.Bot, metin string, fromRun sqlcdb.Run) (sqlcdb.Run, error) {
	var run sqlcdb.Run
	err := s.InTx(ctx, func(q *sqlcdb.Queries) error {
		hedefConv, err := botConversation(ctx, q, to)
		if err != nil {
			return err
		}
		imlec, err := botImleci(ctx, q, to.ID, to.Engine, hedefConv.ID)
		if err != nil {
			return err
		}
		r, err := q.CreateRun(ctx, sqlcdb.CreateRunParams{
			BotID:          to.ID,
			Prompt:         metin,
			Engine:         to.Engine,
			Model:          to.Model,
			ResumeCursor:   imlec,
			Origin:         originBot,
			ReplyToRunID:   uuid.NullUUID{UUID: fromRun.ID, Valid: true},
			ConversationID: uuid.NullUUID{UUID: hedefConv.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		if _, err := q.CreateMessage(ctx, sqlcdb.CreateMessageParams{
			BotID: to.ID, ConversationID: hedefConv.ID,
			RunID:       uuid.NullUUID{UUID: r.ID, Valid: true},
			Role:        "user",
			Text:        metin,
			Origin:      originBot,
			SenderBotID: uuid.NullUUID{UUID: from.ID, Valid: true},
		}); err != nil {
			return err
		}
		kaynakConv, err := botConversation(ctx, q, from)
		if err != nil {
			return err
		}
		if _, err := q.CreateMessage(ctx, sqlcdb.CreateMessageParams{
			BotID: from.ID, ConversationID: kaynakConv.ID,
			Role:   "event",
			Text:   "→ " + to.Name + "'e iletildi: " + devirKırp(metin),
			Origin: originBot,
		}); err != nil {
			return err
		}
		run = r
		return nil
	})
	if err != nil {
		return sqlcdb.Run{}, err
	}
	return run, nil
}

// originBot — devir koşularının kanal damgası. orchestrator.OriginBot'un
// kopyası: store katmanı orchestrator'ı import edemez (orchestrator store'un
// üstüne kurulur); sözleşme yorumu bu satırda.
const originBot = "bot"

// devirKırp — çip metnini tek satıra indirir (uzun görevler sohbeti boğmasın).
func devirKırp(metin string) string {
	metin = strings.Join(strings.Fields(metin), " ")
	if r := []rune(metin); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return metin
}

// botImleci — botun belirli motor + konuşma için birikmiş imleci (ADIM 7+10).
// Satır yoksa boş döner: yeni zincir boş başlar, koşu sonunda UpsertBotCursor
// satırı açar.
func botImleci(ctx context.Context, q *sqlcdb.Queries, botID uuid.UUID, engineName string, convID uuid.UUID) (string, error) {
	c, err := q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: botID, Engine: engineName, ConversationID: convID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return c.Cursor, nil
}

// GrupChat — ekip sohbetine mesaj (ADIM 10): mesaj EKİBİN kanalına yazılır
// (bot_id=ekip), koşu belirlenen ÜYEYE açılır; üyenin grup zinciri
// (bot, motor, grup-konuşma) imleciyle sürer. Kaynak kanal damgası korunur.
func (s *Store) GrupChat(ctx context.Context, ekip, uye sqlcdb.Bot, prompt, text, origin, key string, ekIDs []uuid.UUID) (sqlcdb.Run, bool, error) {
	if key != "" {
		if run, ok, err := s.chatRunByKey(ctx, ekip, origin, key); err != nil || ok {
			return run, ok, err
		}
	}

	errKeyTaken := errors.New("store: idempotency anahtarı eşzamanlı gönderimde kapıldı")
	var run sqlcdb.Run
	err := s.InTx(ctx, func(q *sqlcdb.Queries) error {
		conv, err := botConversation(ctx, q, ekip)
		if err != nil {
			return err
		}
		imlec, err := botImleci(ctx, q, uye.ID, uye.Engine, conv.ID)
		if err != nil {
			return err
		}
		r, err := q.CreateRun(ctx, sqlcdb.CreateRunParams{
			BotID:          uye.ID,
			Prompt:         prompt,
			Engine:         uye.Engine,
			Model:          uye.Model,
			ResumeCursor:   imlec,
			Origin:         origin,
			ConversationID: uuid.NullUUID{UUID: conv.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		msg, err := q.CreateChatMessage(ctx, sqlcdb.CreateChatMessageParams{
			BotID:          ekip.ID,
			ConversationID: conv.ID,
			RunID:          uuid.NullUUID{UUID: r.ID, Valid: true},
			Text:           text,
			Origin:         origin,
			IdempotencyKey: key,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errKeyTaken
			}
			return err
		}
		if len(ekIDs) > 0 {
			rows, err := q.BindAttachments(ctx, sqlcdb.BindAttachmentsParams{
				Column1:   ekIDs,
				MessageID: uuid.NullUUID{UUID: msg.ID, Valid: true},
			})
			if err != nil {
				return err
			}
			if rows != int64(len(ekIDs)) {
				return fmt.Errorf("store: ekler bağlanamadı (%d/%d)", rows, len(ekIDs))
			}
		}
		run = r
		return nil
	})
	if errors.Is(err, errKeyTaken) {
		run2, ok, err2 := s.chatRunByKey(ctx, ekip, origin, key)
		if err2 != nil {
			return sqlcdb.Run{}, false, err2
		}
		if !ok {
			return sqlcdb.Run{}, false, fmt.Errorf("store: %q anahtarı kapıldı ama koşusu yok", key)
		}
		return run2, false, nil
	}
	if err != nil {
		return sqlcdb.Run{}, false, err
	}
	return run, true, nil
}

// GrupHopChat — grup kanalında üye→üye devri (ADIM 10 ikinci aşama): bir
// üyenin cevabı @Adı ile başka bir üyeyi çağırdığında, çağrılan üyeye AYNI
// grup konuşmasında koşu açılır ve zincir derinliği (hop) bir artar.
//
// Mesaj satırı YAZILMAZ: çağrı zaten çağıranın balonu olarak zaman tünelinde
// duruyor; ikinci kez yazılsaydı kullanıcı aynı metni sohbette iki kez
// görürdü. Çağrılanın kendi cevabı normal koşu sonu yolundan (bot mesajı,
// gönderen damgalı) tünele düşer.
//
// kaynakRun izleme içindir; origin KULLANICININ kanalıdır (bot değil) —
// zincirin sonucu mesajı başlatan yüzeye bildirilebilsin diye.
func (s *Store) GrupHopChat(ctx context.Context, uye sqlcdb.Bot, convID uuid.UUID, prompt, origin string, hop int32, kaynakRun uuid.UUID) (sqlcdb.Run, error) {
	var run sqlcdb.Run
	err := s.InTx(ctx, func(q *sqlcdb.Queries) error {
		imlec, err := botImleci(ctx, q, uye.ID, uye.Engine, convID)
		if err != nil {
			return err
		}
		r, err := q.CreateRun(ctx, sqlcdb.CreateRunParams{
			BotID:          uye.ID,
			Prompt:         prompt,
			Engine:         uye.Engine,
			Model:          uye.Model,
			ResumeCursor:   imlec,
			Origin:         origin,
			ReplyToRunID:   uuid.NullUUID{UUID: kaynakRun, Valid: true},
			ConversationID: uuid.NullUUID{UUID: convID, Valid: true},
			GroupHop:       hop,
		})
		if err != nil {
			return err
		}
		run = r
		return nil
	})
	if err != nil {
		return sqlcdb.Run{}, err
	}
	return run, nil
}

// EditChat — kullanıcı mesajını düzenler ve botu YENİDEN üretir (ADIM 9,
// OpenMausBot rewind deseni). Motor oturum imleçleri FORKLANAMAZ; bu yüzden
// düzenlenen dal TAZE oturumda koşar (imleç boş) ve düzenleme noktasına
// kadarki sohbet prompta satır içi tekrar olarak gömülür. Eski sürüm
// superseded_by ile görünmez olur, satır kalır.
//
// İdem işaretleri: düzenlemenin key'i düzenlemeye aittir (aynı düzenlemenin
// retry'i ikinci dal açmaz); AYNI mesajın ikinci FARKLI düzenlemesi yarışı
// SupersedeMessage'in 0 satırıyla kaybeder ("ya hep ya hiç").
func (s *Store) EditChat(ctx context.Context, bot sqlcdb.Bot, eski sqlcdb.Message, yeniMetin, origin, key string) (sqlcdb.Run, bool, error) {
	if key != "" {
		if run, ok, err := s.chatRunByKey(ctx, bot, origin, key); err != nil || ok {
			return run, ok, err
		}
	}

	errKeyTaken := errors.New("store: idempotency anahtarı eşzamanlı gönderimde kapıldı")
	errEditRace := errors.New("store: mesaj çoktan düzenlenmiş (başka düzenleme kazandı)")
	var run sqlcdb.Run
	err := s.InTx(ctx, func(q *sqlcdb.Queries) error {
		prompt := yeniMetin
		if gecmis, err := dalGecmisi(ctx, q, eski); err != nil {
			return err
		} else if gecmis != "" {
			prompt = gecmis + "\n\n" + yeniMetin
		}
		r, err := q.CreateRun(ctx, sqlcdb.CreateRunParams{
			BotID: bot.ID, Prompt: prompt, Engine: bot.Engine, Model: bot.Model,
			ResumeCursor:   "", // TAZE oturum — eski zincir bu dalı görmesin
			Origin:         origin,
			ConversationID: uuid.NullUUID{UUID: eski.ConversationID, Valid: true},
		})
		if err != nil {
			return err
		}
		msg, err := q.CreateEditedMessage(ctx, sqlcdb.CreateEditedMessageParams{
			BotID: bot.ID, ConversationID: eski.ConversationID,
			RunID:          uuid.NullUUID{UUID: r.ID, Valid: true},
			Text:           yeniMetin,
			Origin:         origin,
			IdempotencyKey: key,
			ParentID:       uuid.NullUUID{UUID: eski.ID, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errKeyTaken
			}
			return err
		}
		rows, err := q.SupersedeMessage(ctx, sqlcdb.SupersedeMessageParams{
			ID: eski.ID, SupersededBy: uuid.NullUUID{UUID: msg.ID, Valid: true},
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errEditRace
		}
		run = r
		return nil
	})
	if errors.Is(err, errKeyTaken) {
		run2, ok, err2 := s.chatRunByKey(ctx, bot, origin, key)
		if err2 != nil {
			return sqlcdb.Run{}, false, err2
		}
		if !ok {
			return sqlcdb.Run{}, false, fmt.Errorf("store: %q anahtarı kapıldı ama koşusu yok", key)
		}
		return run2, false, nil
	}
	if err != nil {
		return sqlcdb.Run{}, false, err
	}
	return run, true, nil
}

// dalGecmisi — düzenlenen mesajdan önceki görünür sohbeti satır içi tekrar
// bloğuna çevirir. created_at zinciriyle gider (eski mesajlarda parent_id
// yok — ağaç ikinci aşamada); üst sınır 20 mesaj ve satır başına 400 rune.
func dalGecmisi(ctx context.Context, q *sqlcdb.Queries, eski sqlcdb.Message) (string, error) {
	rows, err := q.ListMessagesUntil(ctx, sqlcdb.ListMessagesUntilParams{
		ConversationID: eski.ConversationID,
		CreatedAt:      eski.CreatedAt,
		ID:             eski.ID,
		Limit:          20,
	})
	if err != nil {
		return "", err
	}
	satirlar := make([]string, 0, len(rows)+1)
	satirlar = append(satirlar, "ÖNCEKİ KONUŞMA (yeniden üretme bağlamı):")
	for i := len(rows) - 1; i >= 0; i-- {
		m := rows[i]
		if m.Role == "event" {
			continue
		}
		rol := "Bot"
		if m.Role == "user" {
			rol = "Kullanıcı"
		}
		metin := devirKırp(m.Text)
		satirlar = append(satirlar, rol+": "+metin)
	}
	if len(satirlar) == 1 {
		return "", nil
	}
	return strings.Join(satirlar, "\n"), nil
}
