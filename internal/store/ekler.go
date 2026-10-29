package store

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/attachments"

	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

// SaveAttachmentFile — ek dosyasını diske yazar ve satırını açar. Tutarlılık
// dosya→satır yönündedir: satır yazılamazsa dosya silinir (tersi değil —
// tek başına duran dosya zararsızdır, satırı olmayan ek yalan söyler).
func (s *Store) SaveAttachmentFile(ctx context.Context, dataDir string, bot sqlcdb.Bot, name string, r io.Reader) (sqlcdb.Attachment, error) {
	if name == "" {
		return sqlcdb.Attachment{}, errors.New("ek adı boş olamaz")
	}
	rel, n, err := attachments.Save(dataDir, r)
	if err != nil {
		return sqlcdb.Attachment{}, err
	}
	att, err := s.Q.CreateAttachment(ctx, sqlcdb.CreateAttachmentParams{
		BotID: bot.ID, Kind: attachments.TurSapta(attachments.Abs(dataDir, rel), name),
		Name: name, Size: n, Path: rel,
	})
	if err != nil {
		_ = attachments.Remove(dataDir, rel)
		return sqlcdb.Attachment{}, err
	}
	return att, nil
}

// SaveRunAttachment — botun ÜRETTİĞİ ek (ADIM 13): koşuya bağlı doğar, koşu
// bitince mesaja bağlanır (BindRunAttachments). Aynı dosya→satır tutarlılığı.
func (s *Store) SaveRunAttachment(ctx context.Context, dataDir string, botID, runID uuid.UUID, name string, r io.Reader) (sqlcdb.Attachment, error) {
	if name == "" {
		return sqlcdb.Attachment{}, errors.New("ek adı boş olamaz")
	}
	rel, n, err := attachments.Save(dataDir, r)
	if err != nil {
		return sqlcdb.Attachment{}, err
	}
	att, err := s.Q.CreateRunAttachment(ctx, sqlcdb.CreateRunAttachmentParams{
		BotID: botID, RunID: uuid.NullUUID{UUID: runID, Valid: true},
		Kind: attachments.TurSapta(attachments.Abs(dataDir, rel), name),
		Name: name, Size: n, Path: rel,
	})
	if err != nil {
		_ = attachments.Remove(dataDir, rel)
		return sqlcdb.Attachment{}, err
	}
	return att, nil
}
