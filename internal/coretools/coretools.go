// Package coretools — bot/rutin/hafıza/skill yönetiminin AGENT ARAÇLARI
// (K13: kurulum bir sohbettir; router bot roster'ı kendisi kurar). Araçlar
// MCP gateway'e kaydedilir (mcp__gw__*), sınıflar açık bildirimdir.
//
// Onay dengesi (K13): bot oluşturma/güncelleme ve hafıza yazımı HAFİFTİR —
// router botun allowed_tools listesine girer, sorulmadan çalışır. Rutin
// oluşturma/değiştirme/silme ve bot silme ONAYLIDIR (kalıcı otomasyon
// yaratmak dışa etkili işlemdir) — allowlist'e girmez, onay kapısına düşer.
package coretools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/engine/pi"
	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/routines"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// LightTools — "hafif" araçlar (K13): router botun allowed_tools'una girer,
// sorulmadan çalışır. Rutin CRUD ve bot silme BİLİNÇLİ olarak burada yok.
func LightTools() []string {
	return []string{
		"mcp__gw__list_bots", "mcp__gw__create_bot", "mcp__gw__update_bot",
		// Ekip kurmak roster kurmanın parçasıdır (K13): bot açmak sorulmadan
		// geçiyorsa o botları bir ekipte toplamak da geçmeli — dışa etkisi yok,
		// tek etkisi kenar çubuğundaki gruplama ve grup kanalı.
		"mcp__gw__create_team", "mcp__gw__add_team_member", "mcp__gw__remove_team_member",
		"mcp__gw__list_routines", "mcp__gw__pause_routine",
		"mcp__gw__memory_save", "mcp__gw__memory_list",
		"mcp__gw__memory_search", "mcp__gw__memory_history",
		"mcp__gw__chat_search",
		"mcp__gw__skill_save", "mcp__gw__skill_list", "mcp__gw__skill_enable",
	}
}

// Service — araçların paylaştığı bağımlılıklar.
type Service struct {
	st *store.Store
	// enq — rutin test çalıştırması için orkestratör; gateway orkestratörden
	// önce kurulduğundan sonradan bağlanır (SetEnqueuer).
	enq routines.Enqueuer
}

func NewService(st *store.Store) *Service { return &Service{st: st} }

// SetEnqueuer — orkestratör kurulduktan sonra bağlanır (cmd/core).
func (s *Service) SetEnqueuer(enq routines.Enqueuer) { s.enq = enq }

// decode — kendi kontratımız katıdır: bilinmeyen alan hatadır.
func decode[T any](raw json.RawMessage, v *T) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("geçersiz argüman: %w", err)
	}
	return nil
}

// schema — JSON Schema kurucu (own-contract: additionalProperties false).
func schema(props map[string]any, required ...string) json.RawMessage {
	m := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		m["required"] = required
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err) // kablolama hatası: şemalar sabittir
	}
	return b
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// kisalt — araç çıktısındaki uzun metni kırpar. Rune sayar, bayt değil:
// Türkçe metinde bayt kesimi çok baytlı bir karakteri ortadan böler ve motora
// bozuk UTF-8 gider.
func kisalt(s string, azami int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= azami {
		return s
	}
	return string(r[:azami]) + "…"
}

var (
	clsRead  = policy.Classification{Class: policy.ClassRead, Ruleable: true}
	clsWrite = policy.Classification{Class: policy.ClassWrite, Ruleable: true}
	// clsWriteNoRule — kural yazılamaz: her seferinde sorulur (bot silme).
	clsWriteNoRule = policy.Classification{Class: policy.ClassWrite, Ruleable: false}
	// clsInternal — güvenli yönde işlem (duraklatma).
	clsInternal = policy.Classification{Class: policy.ClassInternal, Ruleable: true}
)

// findBot — ada ya da UUID'ye göre bot bulur (agent genelde adla konuşur).
func (s *Service) findBot(ctx context.Context, ref string) (db.Bot, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Bot{}, errors.New("bot adı ya da id gerekli")
	}
	if id, err := uuid.Parse(ref); err == nil {
		bot, err := s.st.Q.GetBot(ctx, id)
		if err == nil {
			return bot, nil
		}
	}
	bots, err := s.st.Q.ListBots(ctx)
	if err != nil {
		return db.Bot{}, err
	}
	for _, b := range bots {
		if strings.EqualFold(b.Name, ref) {
			return b, nil
		}
	}
	return db.Bot{}, fmt.Errorf("bot bulunamadı: %q (list_bots ile mevcutları gör)", ref)
}

// Register — tüm yönetim araçlarını gateway'e kaydeder.
func Register(gw *gateway.Gateway, s *Service) {
	registerBotTools(gw, s)
	registerTeamTools(gw, s)
	registerRoutineTools(gw, s)
	registerMemoryTools(gw, s)
	registerSkillTools(gw, s)
	registerWebTools(gw)
}

// ── bot araçları ───────────────────────────────────────────────────────

func registerBotTools(gw *gateway.Gateway, s *Service) {
	gw.Register(gateway.Tool{
		Name:        "list_bots",
		Description: "Kayıtlı botları listeler (id, ad, tür, görev, model).",
		InputSchema: schema(map[string]any{}),
		Class:       clsRead,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			bots, err := s.st.Q.ListBots(ctx)
			if err != nil {
				return "", err
			}
			if len(bots) == 0 {
				return "Kayıtlı bot yok.", nil
			}
			// Ekip satırının kimlerden oluştuğu listede görünmeli: yoksa
			// agent "3E Satış" adında bir bot sanır ve ona tekil bot gibi
			// davranır (üye eklemeyi hiç düşünmez).
			uyelikler, err := s.st.Q.ListAllTeamMemberships(ctx)
			if err != nil {
				return "", err
			}
			ad := make(map[uuid.UUID]string, len(bots))
			for _, bot := range bots {
				ad[bot.ID] = bot.Name
			}
			uyeAdlari := make(map[uuid.UUID][]string)
			for _, u := range uyelikler {
				uyeAdlari[u.TeamID] = append(uyeAdlari[u.TeamID], ad[u.MemberID])
			}
			var b strings.Builder
			for _, bot := range bots {
				fmt.Fprintf(&b, "- %s (%s, %s) id=%s\n  Görev: %s\n", bot.Name, bot.Kind, bot.Model, bot.ID, bot.Description)
				if bot.Kind == "team" {
					liste := "(üye yok — add_team_member ile ekle)"
					if uyeler := uyeAdlari[bot.ID]; len(uyeler) > 0 {
						liste = strings.Join(uyeler, ", ")
					}
					fmt.Fprintf(&b, "  Üyeler (sıralı, ilki koordinatör): %s\n", liste)
				}
			}
			return b.String(), nil
		},
	})

	type createBotArgs struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		SystemPrompt string   `json:"system_prompt"`
		Section      string   `json:"section"`
		Model        string   `json:"model"`
		AllowedTools []string `json:"allowed_tools"`
	}
	gw.Register(gateway.Tool{
		Name: "create_bot",
		Description: "Yeni bot oluşturur. description botun kalıcı görev tanımıdır (sistem promptuna girer). " +
			"allowed_tools sorulmadan kullanabileceği araçlardır — dar tut, sonradan genişlet (K10).",
		InputSchema: schema(map[string]any{
			"name":          str("bot adı (kısa, benzersiz)"),
			"description":   str("kalıcı görev tanımı — bot ne yapar, sınırları ne"),
			"system_prompt": str("ek sistem talimatı (opsiyonel)"),
			"section":       str("kenar çubuğu bölümü (opsiyonel, örn. Satış)"),
			"model":         str("model adı (boşsa varsayılan)"),
			"allowed_tools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "ön-izinli araçlar (opsiyonel)"},
		}, "name", "description"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a createBotArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Name) == "" {
				return "", errors.New("name boş olamaz")
			}
			bot, err := s.st.Q.CreateBot(ctx, db.CreateBotParams{
				Kind: "bot", Name: a.Name, Description: a.Description,
				SystemPrompt: a.SystemPrompt, Section: a.Section,
				ColorPrimary: "#6fa8f5", ColorSecondary: "#3b6fd4",
				Engine: "pi", Model: a.Model,
				AllowedTools: a.AllowedTools,
				// Yeni bot oa-gateway ile doğar: onay kapısı ve OpenAssist
				// araçları o eklentiden gelir, listesiz bot sorulamaz ve
				// devredemez. Kullanıcı isterse ayarlardan çıkarır.
				Extensions: []string{pi.OAGateway},
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Bot oluşturuldu: %s (id=%s). Rol tarifini kullanıcıyla netleştir, gerekirse update_bot ile güncelle.", bot.Name, bot.ID), nil
		},
	})

	type updateBotArgs struct {
		Bot          string   `json:"bot"`
		Name         *string  `json:"name"`
		Description  *string  `json:"description"`
		SystemPrompt *string  `json:"system_prompt"`
		Section      *string  `json:"section"`
		Model        *string  `json:"model"`
		AllowedTools []string `json:"allowed_tools"`
	}
	gw.Register(gateway.Tool{
		Name: "update_bot",
		Description: "Botun adını/görev tanımını/modelini/araç listesini günceller. " +
			"Kalıcı tercihler buraya taşınır (K10: 'bu tercihi bot açıklamasına taşı').",
		InputSchema: schema(map[string]any{
			"bot":           str("hedef bot adı ya da id"),
			"name":          str("yeni ad (opsiyonel — bot kendini adlandırabilir)"),
			"description":   str("yeni görev tanımı (opsiyonel)"),
			"system_prompt": str("yeni sistem talimatı (opsiyonel)"),
			"section":       str("yeni bölüm (opsiyonel)"),
			"model":         str("yeni model (opsiyonel)"),
			"allowed_tools": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "yeni ön-izinli araç listesi (opsiyonel, tam liste)"},
		}, "bot"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a updateBotArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			bot, err := s.findBot(ctx, a.Bot)
			if err != nil {
				return "", err
			}
			pick := func(p *string, cur string) string {
				if p != nil {
					return *p
				}
				return cur
			}
			tools := bot.AllowedTools
			if a.AllowedTools != nil {
				tools = a.AllowedTools
			}
			upd, err := s.st.Q.UpdateBot(ctx, db.UpdateBotParams{
				ID: bot.ID, Name: pick(a.Name, bot.Name),
				Description:  pick(a.Description, bot.Description),
				SystemPrompt: pick(a.SystemPrompt, bot.SystemPrompt),
				ColorPrimary: bot.ColorPrimary, ColorSecondary: bot.ColorSecondary,
				Section: pick(a.Section, bot.Section), Engine: bot.Engine,
				Model: pick(a.Model, bot.Model), Temperature: bot.Temperature,
				AllowedTools: tools, Pinned: bot.Pinned,
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Bot güncellendi: %s (id=%s).", upd.Name, upd.ID), nil
		},
	})

	type deleteBotArgs struct {
		Bot string `json:"bot"`
	}
	gw.Register(gateway.Tool{
		Name:        "delete_bot",
		Description: "Botu SİLER (onaylı işlem — sohbet geçmişi ve rutinleri de gider).",
		InputSchema: schema(map[string]any{"bot": str("silinecek bot adı ya da id")}, "bot"),
		Class:       clsWriteNoRule,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a deleteBotArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			bot, err := s.findBot(ctx, a.Bot)
			if err != nil {
				return "", err
			}
			if err := s.st.Q.DeleteBot(ctx, bot.ID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Bot silindi: %s.", bot.Name), nil
		},
	})
}

// ── ekip araçları (ADIM 10) ────────────────────────────────────────────
//
// Ekip = `bots.kind='team'` satırı + `team_members`. Kendi sohbet kanalı
// vardır (kind='group'); kullanıcı ekibe yazdığında koşu **ilk üyeye**
// (position=0, koordinatör) açılır, o da cevabında @Ad ile diğerlerini
// çağırabilir. Ekip satırı KENDİ BAŞINA koşmaz — model çalıştırmaz, araç
// kullanmaz; sohbeti taşıyan kanaldır.

func registerTeamTools(gw *gateway.Gateway, s *Service) {
	// uyeCoz — ad/id listesini gerçek TEKİL botlara çevirir. Ekip üyesi ekip
	// olamaz: iç içe ekip, grup kanalının "cevap kimin adıyla görünür"
	// sözleşmesini bozar (httpapi da aynı kuralı uygular).
	uyeCoz := func(ctx context.Context, refs []string) ([]db.Bot, error) {
		out := make([]db.Bot, 0, len(refs))
		gorulen := map[uuid.UUID]bool{}
		for _, ref := range refs {
			b, err := s.findBot(ctx, ref)
			if err != nil {
				return nil, err
			}
			if b.Kind != "bot" {
				return nil, fmt.Errorf("%q bir ekip — ekip üyesi olamaz", b.Name)
			}
			if gorulen[b.ID] {
				continue
			}
			gorulen[b.ID] = true
			out = append(out, b)
		}
		return out, nil
	}

	type createTeamArgs struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Section     string   `json:"section"`
		Members     []string `json:"members"`
	}
	gw.Register(gateway.Tool{
		Name: "create_team",
		Description: "Var olan botlardan bir EKİP kurar. Ekibin kendi sohbet kanalı olur: " +
			"kullanıcı oraya yazdığında ilk üye (koordinatör) cevaplar, gerekirse cevabında " +
			"@Ad ile diğer üyeleri aynı kanala çağırır. Üyeler ÖNCE create_bot ile açılmalı; " +
			"sıra önemlidir — ilk verdiğin bot koordinatördür.",
		InputSchema: schema(map[string]any{
			"name":        str("ekip adı (kenar çubuğunda böyle görünür, örn. '3E Satış')"),
			"description": str("ekibin ne için kurulduğu"),
			"section":     str("kenar çubuğu bölümü (opsiyonel)"),
			"members": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
				"description": "üye bot adları ya da id'leri — SIRALI, ilki koordinatör",
			},
		}, "name", "members"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a createTeamArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Name) == "" {
				return "", errors.New("name boş olamaz")
			}
			if len(a.Members) == 0 {
				return "", errors.New("members boş olamaz — ekip en az bir bottan oluşur (önce create_bot)")
			}
			uyeler, err := uyeCoz(ctx, a.Members)
			if err != nil {
				return "", err
			}
			ekip, err := s.st.Q.CreateBot(ctx, db.CreateBotParams{
				Kind: "team", Name: a.Name, Description: a.Description,
				Section:      a.Section,
				ColorPrimary: "#b18cf0", ColorSecondary: "#7b4fd6",
				Engine: "pi", AllowedTools: []string{},
				// Ekip satırı koşmaz; eklenti listesi yine de tekil botlarla
				// aynı doğar — ileride ekip kendi turunu koşarsa (CHIEF)
				// kapısız kalmasın.
				Extensions: []string{pi.OAGateway},
			})
			if err != nil {
				return "", err
			}
			for i, u := range uyeler {
				if err := s.st.Q.AddTeamMember(ctx, db.AddTeamMemberParams{
					TeamID: ekip.ID, MemberID: u.ID, Position: int32(i),
				}); err != nil {
					return "", err
				}
			}
			adlar := make([]string, 0, len(uyeler))
			for _, u := range uyeler {
				adlar = append(adlar, u.Name)
			}
			return fmt.Sprintf("Ekip kuruldu: %s (id=%s). Üyeler: %s. Koordinatör: %s — "+
				"kullanıcı ekip kanalına yazdığında ilk o cevaplar. Koordinatörün görev "+
				"tanımında diğer üyeleri @Ad ile çağırması gerektiği YAZILI olmalı; "+
				"değilse update_bot ile ekle.",
				ekip.Name, ekip.ID, strings.Join(adlar, ", "), adlar[0]), nil
		},
	})

	type teamMemberArgs struct {
		Team string `json:"team"`
		Bot  string `json:"bot"`
	}
	// ekipCoz — hedefin gerçekten ekip olduğunu doğrular.
	ekipCoz := func(ctx context.Context, ref string) (db.Bot, error) {
		t, err := s.findBot(ctx, ref)
		if err != nil {
			return db.Bot{}, err
		}
		if t.Kind != "team" {
			return db.Bot{}, fmt.Errorf("%q bir ekip değil (create_team ile ekip kur)", t.Name)
		}
		return t, nil
	}
	gw.Register(gateway.Tool{
		Name:        "add_team_member",
		Description: "Var olan bir botu ekibe ekler (listenin SONUNA — koordinatör değişmez).",
		InputSchema: schema(map[string]any{
			"team": str("ekip adı ya da id"),
			"bot":  str("eklenecek bot adı ya da id"),
		}, "team", "bot"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a teamMemberArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			ekip, err := ekipCoz(ctx, a.Team)
			if err != nil {
				return "", err
			}
			uyeler, err := uyeCoz(ctx, []string{a.Bot})
			if err != nil {
				return "", err
			}
			mevcut, err := s.st.Q.ListTeamMembers(ctx, ekip.ID)
			if err != nil {
				return "", err
			}
			if err := s.st.Q.AddTeamMember(ctx, db.AddTeamMemberParams{
				TeamID: ekip.ID, MemberID: uyeler[0].ID, Position: int32(len(mevcut)),
			}); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s ekibe eklendi: %s (%d üye).", uyeler[0].Name, ekip.Name, len(mevcut)+1), nil
		},
	})

	gw.Register(gateway.Tool{
		Name: "remove_team_member",
		Description: "Botu ekipten çıkarır. Bot SİLİNMEZ, yalnız ekipten düşer; " +
			"kendi sohbeti ve rutinleri durur.",
		InputSchema: schema(map[string]any{
			"team": str("ekip adı ya da id"),
			"bot":  str("çıkarılacak bot adı ya da id"),
		}, "team", "bot"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a teamMemberArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			ekip, err := ekipCoz(ctx, a.Team)
			if err != nil {
				return "", err
			}
			bot, err := s.findBot(ctx, a.Bot)
			if err != nil {
				return "", err
			}
			mevcut, err := s.st.Q.ListTeamMembers(ctx, ekip.ID)
			if err != nil {
				return "", err
			}
			var kalan []db.ListTeamMembersRow
			for _, u := range mevcut {
				if u.ID != bot.ID {
					kalan = append(kalan, u)
				}
			}
			if len(kalan) == len(mevcut) {
				return "", fmt.Errorf("%q zaten %s ekibinde değil", bot.Name, ekip.Name)
			}
			if err := s.st.Q.RemoveTeamMember(ctx, db.RemoveTeamMemberParams{
				TeamID: ekip.ID, MemberID: bot.ID,
			}); err != nil {
				return "", err
			}
			// Sıra numaraları boşluksuz kalmalı: koordinatör "position=0"dır,
			// ilk üye çıkarıldığında yerine ikincisi geçmeli.
			for i, u := range kalan {
				if err := s.st.Q.AddTeamMember(ctx, db.AddTeamMemberParams{
					TeamID: ekip.ID, MemberID: u.ID, Position: int32(i),
				}); err != nil {
					return "", err
				}
			}
			if len(kalan) == 0 {
				return fmt.Sprintf("%s ekipten çıkarıldı — %s ekibi ARTIK BOŞ; "+
					"üye eklemezsen kanala yazılan mesaj cevapsız kalır.", bot.Name, ekip.Name), nil
			}
			return fmt.Sprintf("%s ekipten çıkarıldı. %s ekibinde %d üye kaldı, koordinatör: %s.",
				bot.Name, ekip.Name, len(kalan), kalan[0].Name), nil
		},
	})
}

// ── rutin araçları ─────────────────────────────────────────────────────

func registerRoutineTools(gw *gateway.Gateway, s *Service) {
	gw.Register(gateway.Tool{
		Name:        "list_routines",
		Description: "Tanımlı rutinleri listeler (bot, tarife, durum, son/sonraki çalışma).",
		InputSchema: schema(map[string]any{}),
		Class:       clsRead,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			rows, err := s.st.Q.ListRoutines(ctx)
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return "Tanımlı rutin yok.", nil
			}
			var b strings.Builder
			for _, r := range rows {
				state := "aktif"
				if !r.Enabled {
					state = "duraklatılmış"
				}
				next := "-"
				if r.NextRunAt != nil {
					next = r.NextRunAt.UTC().Format(time.RFC3339)
				}
				fmt.Fprintf(&b, "- %s (id=%s, bot=%s) cron=%q tz=%s durum=%s sonraki=%s\n",
					r.Name, r.ID, r.BotID, r.CronExpr, r.Timezone, state, next)
			}
			return b.String(), nil
		},
	})

	type createRoutineArgs struct {
		Bot            string `json:"bot"`
		Name           string `json:"name"`
		Cron           string `json:"cron"`
		Timezone       string `json:"timezone"`
		InputSource    string `json:"input_source"`
		ExpectedOutput string `json:"expected_output"`
		ApprovalNote   string `json:"approval_note"`
		StalePolicy    string `json:"stale_policy"`
	}
	routineSchema := schema(map[string]any{
		"bot":             str("sahip bot adı ya da id"),
		"name":            str("rutin adı"),
		"cron":            str("standart cron ifadesi (5 alan, örn. '0 7 * * 1-5')"),
		"timezone":        str("IANA timezone (örn. Europe/Istanbul) — tarife bu saatte yorumlanır"),
		"input_source":    str("girdi kaynağı — rutin neye bakar (K11 zorunlu)"),
		"expected_output": str("beklenen çıktı — ne üretmeli (K11 zorunlu)"),
		"approval_note":   str("onay sınırı — hangi adımlar onaya düşer"),
		"stale_policy":    map[string]any{"type": "string", "enum": []string{"fail", "warn_stale"}, "description": "kaynak yoksa: fail=durdur ve bildir (varsayılan), warn_stale=eski veriyle çalış ama açıkça söyle"},
	}, "bot", "name", "cron", "timezone", "input_source", "expected_output")
	gw.Register(gateway.Tool{
		Name: "create_routine",
		Description: "Zamanlanmış rutin oluşturur (ONAYLI işlem — kalıcı otomasyon). " +
			"Oluşturduktan sonra run_routine_now ile test çalıştırması ZORUNLU (K11).",
		InputSchema: routineSchema,
		Class:       clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a createRoutineArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			bot, err := s.findBot(ctx, a.Bot)
			if err != nil {
				return "", err
			}
			if a.StalePolicy == "" {
				a.StalePolicy = "fail"
			}
			if err := routines.Validate("cron", a.Cron, a.Timezone); err != nil {
				return "", err
			}
			next, err := routines.NextRun(a.Cron, a.Timezone, time.Now())
			if err != nil {
				return "", err
			}
			r, err := s.st.Q.CreateRoutine(ctx, db.CreateRoutineParams{
				BotID: bot.ID, Name: a.Name, TriggerKind: "cron",
				CronExpr: a.Cron, Timezone: a.Timezone,
				InputSource: a.InputSource, ExpectedOutput: a.ExpectedOutput,
				ApprovalNote: a.ApprovalNote, StalePolicy: a.StalePolicy,
				NextRunAt: &next,
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Rutin oluşturuldu: %s (id=%s), ilk çalışma %s. ŞİMDİ run_routine_now ile test çalıştırması yap (K11 — test gerçek iş yapar, yazma adımları onayda kalır).",
				r.Name, r.ID, next.Format(time.RFC3339)), nil
		},
	})

	type routineRefArgs struct {
		Routine string `json:"routine"`
	}
	findRoutine := func(ctx context.Context, ref string) (db.Routine, error) {
		if id, err := uuid.Parse(strings.TrimSpace(ref)); err == nil {
			r, err := s.st.Q.GetRoutine(ctx, id)
			if err == nil {
				return r, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return db.Routine{}, err
			}
		}
		rows, err := s.st.Q.ListRoutines(ctx)
		if err != nil {
			return db.Routine{}, err
		}
		for _, r := range rows {
			if strings.EqualFold(r.Name, strings.TrimSpace(ref)) {
				return r, nil
			}
		}
		return db.Routine{}, fmt.Errorf("rutin bulunamadı: %q", ref)
	}

	gw.Register(gateway.Tool{
		Name:        "pause_routine",
		Description: "Rutini duraklatır (güvenli yön — sorulmaz).",
		InputSchema: schema(map[string]any{"routine": str("rutin adı ya da id")}, "routine"),
		Class:       clsInternal,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a routineRefArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			r, err := findRoutine(ctx, a.Routine)
			if err != nil {
				return "", err
			}
			if _, err := s.st.Q.SetRoutineEnabled(ctx, db.SetRoutineEnabledParams{ID: r.ID, Enabled: false}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Rutin duraklatıldı: %s.", r.Name), nil
		},
	})

	gw.Register(gateway.Tool{
		Name:        "resume_routine",
		Description: "Duraklatılmış rutini yeniden başlatır (onaylı — otomasyonu geri açar).",
		InputSchema: schema(map[string]any{"routine": str("rutin adı ya da id")}, "routine"),
		Class:       clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a routineRefArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			r, err := findRoutine(ctx, a.Routine)
			if err != nil {
				return "", err
			}
			next, err := routines.NextRun(r.CronExpr, r.Timezone, time.Now())
			if err != nil {
				return "", err
			}
			if _, err := s.st.Q.SetRoutineEnabled(ctx, db.SetRoutineEnabledParams{ID: r.ID, Enabled: true, NextRunAt: &next}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Rutin yeniden başlatıldı: %s, sonraki çalışma %s.", r.Name, next.Format(time.RFC3339)), nil
		},
	})

	gw.Register(gateway.Tool{
		Name:        "delete_routine",
		Description: "Rutini siler (onaylı işlem).",
		InputSchema: schema(map[string]any{"routine": str("rutin adı ya da id")}, "routine"),
		Class:       clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a routineRefArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			r, err := findRoutine(ctx, a.Routine)
			if err != nil {
				return "", err
			}
			if _, err := s.st.Q.DeleteRoutine(ctx, r.ID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Rutin silindi: %s.", r.Name), nil
		},
	})

	gw.Register(gateway.Tool{
		Name: "run_routine_now",
		Description: "Rutini HEMEN bir kez çalıştırır (test çalıştırması, K11 zorunlu adım). " +
			"Test gerçek iş yapar — yazma adımları onay kapısından geçer.",
		InputSchema: schema(map[string]any{"routine": str("rutin adı ya da id")}, "routine"),
		Class:       clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a routineRefArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			if s.enq == nil {
				return "", errors.New("koşu kuyruğu hazır değil")
			}
			r, err := findRoutine(ctx, a.Routine)
			if err != nil {
				return "", err
			}
			bot, err := s.st.Q.GetBot(ctx, r.BotID)
			if err != nil {
				return "", err
			}
			run, err := s.enq.EnqueueChat(ctx, bot, routines.BuildPrompt(r), "routine")
			if err != nil {
				return "", err
			}
			if err := s.st.Q.RecordRoutineRun(ctx, db.RecordRoutineRunParams{RoutineID: r.ID, RunID: run.ID}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Test çalıştırması kuyruğa alındı: rutin=%s koşu=%s.", r.Name, run.ID), nil
		},
	})
}

// ── hafıza araçları ────────────────────────────────────────────────────

func registerMemoryTools(gw *gateway.Gateway, s *Service) {
	type memorySaveArgs struct {
		Scope         string  `json:"scope"`
		Bot           string  `json:"bot"`
		Kind          string  `json:"kind"`
		Content       string  `json:"content"`
		Confidence    float32 `json:"confidence"`
		ExpiresInDays int     `json:"expires_in_days"`
	}
	gw.Register(gateway.Tool{
		Name: "memory_save",
		Description: "Kalıcı hafızaya olgu/tercih/düzeltme ÖNERİR; core doğrular ve kaydeder " +
			"(dedup otomatik). scope=user tüm botların promptuna girer, scope=bot yalnız o bota. " +
			"kind=correction: kullanıcı seni düzelttiyse — bir daha aynı hatayı yapmamak için.",
		InputSchema: schema(map[string]any{
			"scope":           map[string]any{"type": "string", "enum": []string{"user", "bot"}, "description": "user=kullanıcı hakkında, bot=bu botun işine özel"},
			"bot":             str("scope=bot ise hedef bot adı ya da id"),
			"kind":            map[string]any{"type": "string", "enum": []string{"fact", "preference", "correction"}},
			"content":         str("tek cümlelik, kendi başına anlaşılır olgu"),
			"confidence":      map[string]any{"type": "number", "description": "0-1 güven (varsayılan 1)"},
			"expires_in_days": map[string]any{"type": "integer", "description": "gün cinsinden geçerlilik (0=süresiz)"},
		}, "scope", "kind", "content"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a memorySaveArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			a.Content = strings.TrimSpace(a.Content)
			if a.Content == "" {
				return "", errors.New("content boş olamaz")
			}
			// Sabit talimat bloğuna motor YAZAMAZ. Şemadaki enum yalnız bir
			// tariftir — modelin gönderdiği değeri kapıda kesmek gerekir,
			// çünkü 'instruction' veritabanı kısıtından geçen geçerli bir
			// değer. Bu blok her koşuda prompta giriyor ve konsolidasyon ona
			// dokunamıyor; oraya yazma yetkisi kullanıcıda kalmazsa okunan bir
			// sayfadan gelen "bundan sonra hep şunu yap" cümlesi kalıcı ve
			// korumalı bir kurala dönüşür.
			if a.Kind == "instruction" {
				return "", errors.New("sabit talimatları yalnız kullanıcı yazabilir " +
					"(Hafıza ekranı); olgu için kind=fact, düzeltme için kind=correction kullan")
			}
			var expires *time.Time
			if a.ExpiresInDays > 0 {
				t := time.Now().UTC().AddDate(0, 0, a.ExpiresInDays)
				expires = &t
			}

			// Yazma store.SaveMemory'den geçer: content_hash ve revizyon
			// geçmişi orada, tek yerde (K20).
			taslak := store.MemoryDraft{
				Scope: a.Scope, Kind: a.Kind, Content: a.Content,
				Confidence: a.Confidence, ExpiresAt: expires,
			}
			if a.Scope == "bot" {
				bot, berr := s.findBot(ctx, a.Bot)
				if berr != nil {
					return "", berr
				}
				taslak.BotID = uuid.NullUUID{UUID: bot.ID, Valid: true}
			}
			mem, err := s.st.SaveMemory(ctx, taslak)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Hafızaya kaydedildi (id=%s, %s/%s, revizyon %d).",
				mem.ID, mem.Scope, mem.Kind, mem.Revision), nil
		},
	})

	type memorySearchArgs struct {
		Query    string `json:"query"`
		Bot      string `json:"bot"`
		Archived bool   `json:"archived"`
	}
	gw.Register(gateway.Tool{
		Name: "memory_search",
		Description: "Hafızada içerik araması. Sistem promptuna artık özet giriyor — " +
			"bir konuda ne bilindiğini öğrenmek için tam kayıtları buradan çek. " +
			"archived=true, konsolidasyonun emekliye ayırdığı kayıtlarda arar.",
		InputSchema: schema(map[string]any{
			"query":    str("aranacak metin parçası"),
			"bot":      str("yalnız bu botun kayıtları + user kapsamı (opsiyonel)"),
			"archived": map[string]any{"type": "boolean", "description": "emekliye ayrılmış (birleştirilmiş/elenmiş) kayıtlarda ara"},
		}, "query"),
		Class: clsRead,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a memorySearchArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			a.Query = strings.TrimSpace(a.Query)
			if a.Query == "" {
				return "", errors.New("query boş olamaz")
			}
			var botID uuid.NullUUID
			if strings.TrimSpace(a.Bot) != "" {
				bot, err := s.findBot(ctx, a.Bot)
				if err != nil {
					return "", err
				}
				botID = uuid.NullUUID{UUID: bot.ID, Valid: true}
			}
			rows, err := s.st.Q.SearchMemories(ctx, db.SearchMemoriesParams{
				BotID: botID, Q: a.Query, Arsiv: a.Archived, Lim: 50,
			})
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return fmt.Sprintf("%q için hafızada kayıt yok.", a.Query), nil
			}
			var b strings.Builder
			for _, m := range rows {
				fmt.Fprintf(&b, "- [%s/%s] %s (id=%s)\n", m.Scope, m.Kind, m.Content, m.ID)
			}
			return b.String(), nil
		},
	})

	// chat_search — hafıza araçlarının yanında duruyor ama BAŞKA bir şeye
	// bakıyor: hafıza damıtılmış kalıcı olgudur, sohbet ham geçmiştir.
	// "Bunu daha önce konuşmuş muyduk?" sorusunun cevabı hafızada yoktur ve
	// olmamalıdır da — her konuşmayı kalıcı olguya çevirmek hafızayı çöpe
	// çevirir. Cevap konuşmanın kendisinde durur; eksik olan ona bakabilmekti.
	type chatSearchArgs struct {
		Query string `json:"query"`
		Bot   string `json:"bot"`
	}
	gw.Register(gateway.Tool{
		Name: "chat_search",
		Description: "Geçmiş KONUŞMALARDA arama: 'bunu daha önce konuşmuş muyduk?'. " +
			"memory_search damıtılmış kalıcı olgulara bakar, bu araç mesajların kendisine. " +
			"Türkçe ekler elenir (\"hafızayı\" araması \"hafıza\" geçen mesajı bulur), " +
			"tırnak içinde yazılan tam öbek arar, - ile kelime dışlanır.",
		InputSchema: schema(map[string]any{
			"query": str("aranacak kelimeler; \"tam öbek\" ve -dışlanan desteklenir"),
			"bot":   str("yalnız bu botun konuşmaları (opsiyonel; boşsa tüm botlar)"),
		}, "query"),
		Class: clsRead,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a chatSearchArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			a.Query = strings.TrimSpace(a.Query)
			if a.Query == "" {
				return "", errors.New("query boş olamaz")
			}
			var botID uuid.NullUUID
			if strings.TrimSpace(a.Bot) != "" {
				bot, err := s.findBot(ctx, a.Bot)
				if err != nil {
					return "", err
				}
				botID = uuid.NullUUID{UUID: bot.ID, Valid: true}
			}
			rows, err := s.st.Q.SearchMessages(ctx, db.SearchMessagesParams{
				BotID: botID, Q: a.Query, Lim: 20,
			})
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return fmt.Sprintf("%q için geçmiş konuşmalarda sonuç yok.", a.Query), nil
			}
			// Bot adları tek sorguda çözülür: satır başına GetBot çağırmak 20
			// sonuç için 20 gidiş-dönüş demekti.
			adlar := map[uuid.UUID]string{}
			if bots, err := s.st.Q.ListBots(ctx); err == nil {
				for _, b := range bots {
					adlar[b.ID] = b.Name
				}
			}
			var b strings.Builder
			for _, m := range rows {
				kim := "kullanıcı"
				if m.Role == "bot" {
					kim = adlar[m.BotID]
					if kim == "" {
						kim = "bot"
					}
				}
				fmt.Fprintf(&b, "- [%s] %s: %s\n",
					m.CreatedAt.Format("2006-01-02 15:04"), kim, kisalt(m.Text, 300))
			}
			return b.String(), nil
		},
	})

	type memoryRestoreArgs struct {
		ID string `json:"id"`
	}
	gw.Register(gateway.Tool{
		Name: "memory_restore",
		Description: "Konsolidasyonun emekliye ayırdığı hafıza kaydını geri getirir. " +
			"Kaydı bulmak için önce memory_search'ü archived=true ile çağır. " +
			"memory_revert'ten farkı: o hedefin İÇERİĞİNİ eski haline döndürür, " +
			"bu ise katılan kaydı yeniden yaşayanlar arasına alır.",
		InputSchema: schema(map[string]any{
			"id": str("memory_search(archived=true) çıktısındaki kayıt id'si"),
		}, "id"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a memoryRestoreArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			id, err := uuid.Parse(strings.TrimSpace(a.ID))
			if err != nil {
				return "", fmt.Errorf("geçersiz id: %w", err)
			}
			mem, err := s.st.RestoreMemory(ctx, id)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Kayıt hafızaya geri alındı: %s", mem.Content), nil
		},
	})

	type memoryHistoryArgs struct {
		ID string `json:"id"`
	}
	gw.Register(gateway.Tool{
		Name: "memory_history",
		Description: "Bir hafıza kaydının içerik geçmişi: revizyon no, kim yazdı " +
			"(agent/consolidation/user), ne zaman. Geri almadan önce buraya bakılır.",
		InputSchema: schema(map[string]any{"id": str("memory_list'ten gelen kayıt id'si")}, "id"),
		Class:       clsRead,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a memoryHistoryArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			id, err := uuid.Parse(strings.TrimSpace(a.ID))
			if err != nil {
				return "", fmt.Errorf("geçersiz id: %w", err)
			}
			rows, err := s.st.Q.ListMemoryRevisions(ctx, id)
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return "Bu kaydın geçmişi yok.", nil
			}
			var b strings.Builder
			for _, r := range rows {
				fmt.Fprintf(&b, "- r%d [%s] %s — %s", r.Revision, r.Author,
					r.CreatedAt.Format("2006-01-02 15:04"), r.Content)
				if r.Note != "" {
					fmt.Fprintf(&b, " (%s)", r.Note)
				}
				b.WriteString("\n")
			}
			return b.String(), nil
		},
	})

	type memoryRevertArgs struct {
		ID       string `json:"id"`
		Revision int32  `json:"revision"`
	}
	gw.Register(gateway.Tool{
		Name: "memory_revert",
		Description: "Hafıza kaydını eski bir revizyonuna döndürür (kötü bir konsolidasyon " +
			"turundan dönüş). Sayaç geri sarılmaz: geri alma da yeni bir revizyon yazar.",
		InputSchema: schema(map[string]any{
			"id":       str("memory_list'ten gelen kayıt id'si"),
			"revision": map[string]any{"type": "integer", "description": "memory_history'den dönülecek revizyon no"},
		}, "id", "revision"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a memoryRevertArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			id, err := uuid.Parse(strings.TrimSpace(a.ID))
			if err != nil {
				return "", fmt.Errorf("geçersiz id: %w", err)
			}
			mem, err := s.st.RevertMemory(ctx, id, a.Revision)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("r%d içeriğine dönüldü (yeni revizyon %d): %s",
				a.Revision, mem.Revision, mem.Content), nil
		},
	})

	gw.Register(gateway.Tool{
		Name:        "memory_list",
		Description: "Kalıcı hafıza kayıtlarını listeler.",
		InputSchema: schema(map[string]any{}),
		Class:       clsRead,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			rows, err := s.st.Q.ListMemories(ctx, 100)
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return "Hafıza boş.", nil
			}
			var b strings.Builder
			for _, m := range rows {
				fmt.Fprintf(&b, "- [%s/%s] %s (id=%s)\n", m.Scope, m.Kind, m.Content, m.ID)
			}
			return b.String(), nil
		},
	})

	type memoryDeleteArgs struct {
		ID string `json:"id"`
	}
	gw.Register(gateway.Tool{
		Name:        "memory_delete",
		Description: "Hafıza kaydını siler (yanlışlanan/bayatlayan olgular için).",
		InputSchema: schema(map[string]any{"id": str("memory_list'ten gelen kayıt id'si")}, "id"),
		Class:       clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a memoryDeleteArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			id, err := uuid.Parse(a.ID)
			if err != nil {
				return "", fmt.Errorf("geçersiz id: %w", err)
			}
			n, err := s.st.Q.DeleteMemory(ctx, id)
			if err != nil {
				return "", err
			}
			if n == 0 {
				return "", errors.New("kayıt bulunamadı")
			}
			return "Hafıza kaydı silindi.", nil
		},
	})
}

// ── skill araçları ─────────────────────────────────────────────────────

func registerSkillTools(gw *gateway.Gateway, s *Service) {
	type skillSaveArgs struct {
		Name         string `json:"name"`
		WhenToUse    string `json:"when_to_use"`
		Instructions string `json:"instructions"`
	}
	gw.Register(gateway.Tool{
		Name: "skill_save",
		Description: "Kanıtlanmış bir süreci yeniden kullanılabilir skill olarak kaydeder (K11 adım 3 — " +
			"önce tek seferlik görevde güvenilir sonuç al, sonra kaydet). Aynı adla çağrı günceller.",
		InputSchema: schema(map[string]any{
			"name":         str("skill adı (kısa, benzersiz)"),
			"when_to_use":  str("hangi durumda devreye girer"),
			"instructions": str("adımlar, doğrulama, çıktı formatı, onay gerektiren adımlar"),
		}, "name", "instructions"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a skillSaveArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			sk, err := s.st.Q.UpsertSkill(ctx, db.UpsertSkillParams{
				Name: a.Name, WhenToUse: a.WhenToUse, Instructions: a.Instructions,
			})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Skill kaydedildi: %s (id=%s). skill_enable ile botlara aç.", sk.Name, sk.ID), nil
		},
	})

	gw.Register(gateway.Tool{
		Name:        "skill_list",
		Description: "Kayıtlı skill'leri listeler.",
		InputSchema: schema(map[string]any{}),
		Class:       clsRead,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			rows, err := s.st.Q.ListSkills(ctx)
			if err != nil {
				return "", err
			}
			if len(rows) == 0 {
				return "Kayıtlı skill yok.", nil
			}
			var b strings.Builder
			for _, sk := range rows {
				fmt.Fprintf(&b, "- %s (id=%s) — %s\n", sk.Name, sk.ID, sk.WhenToUse)
			}
			return b.String(), nil
		},
	})

	type skillEnableArgs struct {
		Bot   string `json:"bot"`
		Skill string `json:"skill"`
	}
	gw.Register(gateway.Tool{
		Name:        "skill_enable",
		Description: "Skill'i bir bot için etkinleştirir (skill botun sistem promptuna girer).",
		InputSchema: schema(map[string]any{
			"bot":   str("bot adı ya da id"),
			"skill": str("skill adı"),
		}, "bot", "skill"),
		Class: clsWrite,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a skillEnableArgs
			if err := decode(raw, &a); err != nil {
				return "", err
			}
			bot, err := s.findBot(ctx, a.Bot)
			if err != nil {
				return "", err
			}
			sk, err := s.st.Q.GetSkillByName(ctx, a.Skill)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return "", fmt.Errorf("skill bulunamadı: %q", a.Skill)
				}
				return "", err
			}
			if err := s.st.Q.EnableSkillForBot(ctx, db.EnableSkillForBotParams{BotID: bot.ID, SkillID: sk.ID}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Skill %q botu %q için etkin.", sk.Name, bot.Name), nil
		},
	})
}
