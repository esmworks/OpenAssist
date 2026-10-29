//go:build integration

// Sohbet geçmişi araması gerçek Postgres üzerinden: tam metin indeksi,
// Türkçe ek soyma ve iki gizleme kuralı (event satırı, düzenlenmiş sürüm).
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestSohbetAramasi(t *testing.T) {
	st, base, bot := hafizaSunucu(t)
	ctx := context.Background()

	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("konuşma: %v", err)
	}
	yaz := func(rol, metin string) db.Message {
		t.Helper()
		m, err := st.Q.CreateMessage(ctx, db.CreateMessageParams{
			BotID: bot.ID, ConversationID: conv.ID, Role: rol, Text: metin, Origin: "api",
		})
		if err != nil {
			t.Fatalf("mesaj (%s): %v", rol, err)
		}
		// Ayrı temizlik yok: bot silinince mesajlar CASCADE ile gider.
		return m
	}

	yaz("user", "Postgres indeksini nasıl kuracağız?")
	yaz("bot", "GIN indeksi tam metin araması için uygun.")
	yaz("event", "koşu iptal edildi — indeks")
	eski := yaz("user", "indeks hakkında yanlış bir şey yazmıştım")

	// Düzenlenen mesajın ESKİ sürümü: akışta görünmüyor, aramada da
	// görünmemeli — kullanıcı sildiği cümleyi karşısında bulmasın.
	yeni := yaz("user", "düzeltilmiş hali")
	if _, err := st.Q.SupersedeMessage(ctx, db.SupersedeMessageParams{
		ID: eski.ID, SupersededBy: uuid.NullUUID{UUID: yeni.ID, Valid: true},
	}); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	ara := func(q string) []map[string]any {
		t.Helper()
		res, err := belirtecliGet(base + "/api/messages/search?q=" + url.QueryEscape(q))
		if err != nil {
			t.Fatalf("istek: %v", err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("beklenmeyen kod: %d", res.StatusCode)
		}
		var out []map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("çözüm: %v", err)
		}
		return out
	}

	// "indeks" araması hem "indeksini" hem "indeksi" geçen mesajı bulmalı.
	// Bu testin asıl koruduğu şey: Türkçe eklere önek eşleşmesiyle
	// yaklaşıyoruz. 'turkish' stemmer denendi ve BOZUK çıktı — "indeksini"yi
	// soyup "indeksi"yi olduğu gibi bırakıyor, sorgu ile belgeyi farklı
	// lexeme'lere indiriyordu. Biri stemmer'ı geri getirirse buradan görsün.
	got := ara("indeks")
	metinler := map[string]bool{}
	for _, m := range got {
		metinler[m["text"].(string)] = true
	}
	if !metinler["Postgres indeksini nasıl kuracağız?"] {
		t.Errorf("Türkçe eki soyulmadı, kayıt bulunamadı: %+v", got)
	}
	if !metinler["GIN indeksi tam metin araması için uygun."] {
		t.Errorf("bot cevabı bulunmalıydı: %+v", got)
	}
	if metinler["koşu iptal edildi — indeks"] {
		t.Error("event satırı arama sonucunu kirletmemeli")
	}
	if metinler["indeks hakkında yanlış bir şey yazmıştım"] {
		t.Error("düzenlenmiş mesajın eski sürümü dönmemeli")
	}

	// Bozuk sorgu 500 DEĞİL boş sonuç: girdi önce lexeme'lere ayrıldığı için
	// ")" ve tırnak temizlenir. Ham to_tsquery bu girdide patlar ve arama
	// kutusu kullanıcıya 500 gösterirdi.
	if out := ara(`bulunmayan) & "kelime`); len(out) != 0 {
		t.Errorf("bozuk sorgu boş dönmeliydi: %+v", out)
	}

	// q zorunlu: boş sorguyla "her şey" dönmesi arama değil listelemedir.
	res, err := belirtecliGet(base + "/api/messages/search?q=")
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("boş q için 400 beklenirdi: %d", res.StatusCode)
	}
}
