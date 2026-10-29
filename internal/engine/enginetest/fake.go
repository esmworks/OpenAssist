// Package enginetest — testlerde gerçek CLI yerine geçen Executor'lar.
// CI'da claude yok; orkestratör ve API testleri motoru bununla sürer.
package enginetest

import (
	"context"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Func, tek fonksiyonluk Executor — testler davranışı satır içi yazar.
type Func func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error)

func (f Func) SendTurn(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
	return f(ctx, in, emit)
}

var _ engine.Executor = Func(nil)

// AskingWriter — gerçek adaptör davranışını taklit eden hazır senaryo:
// "Write" izni ister (request olaylarını da kendisi yayınlar, adaptördeki
// broker gibi); izin gelirse işi yapar, gelmezse kibarca özetleyip bitirir.
func AskingWriter(finalOnAllow, finalOnDeny string) Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "fake-oturum", Model: "fake-model"})
		emit(engine.Event{Kind: engine.KindRequestOpened, Request: &engine.RequestInfo{Kind: "permission", Tool: "Write"}})

		ans := engine.Answer{Behavior: "deny"}
		if in.OnAsk != nil {
			ans = in.OnAsk(ctx, engine.Ask{Kind: "permission", Tool: "Write", Input: []byte(`{"file_path":"not.txt"}`)})
		}
		emit(engine.Event{Kind: engine.KindRequestResolved, Request: &engine.RequestInfo{
			Kind: "permission", Tool: "Write", Behavior: ans.Behavior, Source: ans.Source,
		}})

		res := &engine.TurnResult{Ok: true, StopReason: "success", CostUSD: 0.001, NumTurns: 1, ResumeCursor: "fake-imlec"}
		if ans.Behavior == "allow" {
			emit(engine.Event{Kind: engine.KindToolStarted, Tool: &engine.ToolInfo{ID: "t1", Name: "Write"}})
			emit(engine.Event{Kind: engine.KindToolCompleted, Tool: &engine.ToolInfo{ID: "t1"}})
			res.FinalText = finalOnAllow
		} else {
			res.FinalText = finalOnDeny
		}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

// PlainResponder — soru sormayan basit motor: metin yayar, sonuç döner.
func PlainResponder(text string) Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "fake-oturum", Model: "fake-model"})
		emit(engine.Event{Kind: engine.KindAssistantText, Text: text})
		res := &engine.TurnResult{Ok: true, StopReason: "success", CostUSD: 0.0005, NumTurns: 1, FinalText: text, ResumeCursor: "fake-imlec"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}
