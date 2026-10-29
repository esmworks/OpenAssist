package engine

// Koşuyu öldürmeyen, motoru sonuca yürüten zaman aşımı kopyaları
// (Ek B.2-2 — OpenMausBot'tan uyarlandı). Motor-bağımsızdır: her adaptör
// aynı kopyaları kullanır ki bot davranışı motorlar arasında tutarlı kalsın.
const (
	QuestionTimeoutNote = "Cevap gelmedi — kendi kararını ver."
	DenyParkNote        = "OpenAssist: onay bekliyor, koşu park edildi. Bu adımı atla, onaysız yapabildiğini bitir."
	DenyByUserNote      = "Kullanıcı bu adımı reddetti — onu yapmadan durumu toparla ve bitir."
)
