//go:build integration

// Entegrasyon testlerinin ortak belirteç yardımcıları. AYRI dosya ve
// integration etiketli: etiketsiz derlemede kimse kullanmadığı için linter
// "unused" derdi.
package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
)

// belirtecliIstemci — httptest sunucusunun istemcisine Authorization başlığı
// ekler. Tek satırla tüm çağrı yerlerini kapsar; entegrasyon testlerinde her
// isteğe elle başlık yazmak okunurluğu bitirirdi.
func belirtecliIstemci(ts *httptest.Server) {
	ts.Client().Transport = belirtecTasiyici{alt: ts.Client().Transport}
}

type belirtecTasiyici struct{ alt http.RoundTripper }

func (b belirtecTasiyici) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+testBelirtec)
	alt := b.alt
	if alt == nil {
		alt = http.DefaultTransport
	}
	return alt.RoundTrip(r)
}

// belirtecliGet / belirtecliPost — paket seviyesindeki http.Get/http.Post'un
// belirteçli karşılıkları. Bazı testler httptest istemcisi yerine bunları
// kullanıyor; kapı devreye girince hepsi 401 alırdı.
func belirtecliGet(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+testBelirtec)
	return http.DefaultClient.Do(req)
}

func belirtecliPost(url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+testBelirtec)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return http.DefaultClient.Do(req)
}
