package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine/pi"
)

func TestBotCreateReqValidate(t *testing.T) {
	cases := []struct {
		name    string
		req     botCreateReq
		wantErr bool
	}{
		{"varsayılanlar dolar", botCreateReq{Name: "Gelen Kutusu"}, false},
		{"ad zorunlu", botCreateReq{}, true},
		{"ad boşluktan ibaret olamaz", botCreateReq{Name: "   "}, true},
		{"kind bilinmeli", botCreateReq{Name: "x", Kind: "takım"}, true},
		{"temperature aralık dışı", botCreateReq{Name: "x", Temperature: f(1.5)}, true},
		{"tek bota üye verilemez", botCreateReq{Name: "x", Kind: "bot", Members: []uuid.UUID{uuid.New()}}, true},
		{"ekibe üye verilebilir", botCreateReq{Name: "x", Kind: "team", Members: []uuid.UUID{uuid.New()}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.req.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() = %v, hata beklentisi %v", err, c.wantErr)
			}
		})
	}

	t.Run("varsayılan değerler", func(t *testing.T) {
		req := botCreateReq{Name: "x"}
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
		if req.Kind != "bot" || req.Engine != "pi" || req.ColorPrimary == "" {
			t.Errorf("varsayılanlar dolmadı: %+v", req)
		}
		// Eklenti listesi verilmediyse onay kapısı gelir: onsuz bot izin
		// sormaz ve gateway araçlarını hiç görmez.
		if len(req.Extensions) != 1 || req.Extensions[0] != pi.OAGateway {
			t.Errorf("varsayılan eklenti oa-gateway olmalıydı: %+v", req.Extensions)
		}
	})

	// Açıkça boş liste "çıplak pi" demektir ve varsayılana EZİLMEMELİ.
	t.Run("açık boş eklenti listesi korunur", func(t *testing.T) {
		req := botCreateReq{Name: "x", Extensions: []string{}}
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
		if len(req.Extensions) != 0 {
			t.Errorf("boş liste korunmalıydı: %+v", req.Extensions)
		}
	})
}

func TestApprovalReqValidate(t *testing.T) {
	if err := (&approvalReq{}).Validate(); err == nil {
		t.Error("boş istek hata olmalı")
	}
	if err := (&approvalReq{Decision: "belki"}).Validate(); err == nil {
		t.Error("decision allow|deny dışı hata olmalı")
	}
	if err := (&approvalReq{Decision: "allow"}).Validate(); err != nil {
		t.Errorf("allow geçerli olmalı: %v", err)
	}
	if err := (&approvalReq{Answer: "mavi"}).Validate(); err != nil {
		t.Errorf("yalnız answer geçerli olmalı: %v", err)
	}
}

func f(v float64) *float64 { return &v }
