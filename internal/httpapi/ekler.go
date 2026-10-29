package httpapi

import (
	"errors"
	"mime"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/attachments"
)

// POST /api/bots/{id}/attachments — multipart/form-data, "file" alanı.
// Akış (ADIM 5): istemci ekleri önce buraya yükler, aldığı id'leri mesaj
// gönderiminde attachment_ids ile verir; ekler mesaj yazımında bağlanır.
// O güne kadar tek başına duran (bağsız) ek zararsız atıktır — bot silinince
// satır/dosya birlikte gider.
func (a *api) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	// MaxBytesReader: sınır AŞILINCA multipart ayrıştırıcı hatası döner; hatayı
	// 413'e çeviririz (hafiza.go içe aktarma deseni).
	r.Body = http.MaxBytesReader(w, r.Body, attachments.EkAzami+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "multipart gövde bekleniyordu")
		return
	}
	part, err := mr.NextPart()
	if err != nil {
		var büyük *http.MaxBytesError
		if errors.As(err, &büyük) {
			writeErr(w, http.StatusRequestEntityTooLarge, "ek çok büyük")
			return
		}
		writeErr(w, http.StatusBadRequest, "ek okunamadı")
		return
	}
	if part.FormName() != "file" {
		writeErr(w, http.StatusBadRequest, `"file" alanı gerekli`)
		return
	}
	defer func() { _ = part.Close() }()

	ad := attachments.TemizAd(part.FileName())
	att, err := a.st.SaveAttachmentFile(r.Context(), a.dataDir, bot, ad, part)
	if errors.Is(err, attachments.ErrTooBig) {
		writeErr(w, http.StatusRequestEntityTooLarge, "ek çok büyük")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":   att.ID,
		"name": att.Name,
		"kind": att.Kind,
		"size": att.Size,
	})
}

// GET /api/attachments/{id}/file — ek dosyasını indirir. İçerik türü
// uzantı/kısa algılamayla, adı RFC 2231 ile taşınır (Türkçe adlar bozulmaz).
func (a *api) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz ek id")
		return
	}
	att, err := a.st.Q.GetAttachment(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "ek yok")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Yol id tabanlıdır (istemciden asla gelmez); Content-Disposition'da yalnız
	// DB'deki görünen ad taşınır. İçerik türünü ServeFile uzantıdan türetir.
	ap := attachments.Abs(a.dataDir, att.Path)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Name}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, ap)
}
