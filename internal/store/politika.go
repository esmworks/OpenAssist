package store

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/esmworks/OpenAssist/internal/policy"
)

// LoadPolicy — kayıtlı onay politikasını okur; okunamaz ya da geçersizse
// varsayılanlara döner (politikasız koşu, koşmayan koşudan iyidir).
//
// Store'da duruyor ki politikayı okuyan HER yol aynı satırları kullansın:
// orkestratörün onay kapısı ve gece hafıza konsolidasyonu. İki kopya olsaydı
// politikaya yeni bir alan eklendiğinde biri güncellenip diğeri unutulur ve
// bakım turu sessizce eski politikayla koşardı (ör. sessiz saat mantığı
// ayrışırsa tur gündüz çalışmaya başlar).
func (s *Store) LoadPolicy(ctx context.Context) policy.Settings {
	row, err := s.Q.GetSystemInfo(ctx, "policy")
	if err != nil {
		return policy.DefaultSettings()
	}
	var st policy.Settings
	if json.Unmarshal([]byte(row.Value), &st) != nil || st.Validate() != nil {
		slog.Warn("kayıtlı politika okunamadı — varsayılanlar kullanılıyor")
		return policy.DefaultSettings()
	}
	return st
}
