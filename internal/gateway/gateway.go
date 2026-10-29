// Package gateway — motorun konektörlere tek yüzeyi: MCP Streamable HTTP
// sunucusu (K15/K18). claude CLI --mcp-config ile buraya bağlanır; araçlar
// mcp__<sunucu>__<araç> adıyla görünür ve her araç risk sınıfını AÇIKÇA
// bildirir (policy.ClassifyWith bunları sezgiselden önce okur).
//
// Araç yürütme hataları JSON-RPC hatası DEĞİL, isError'lı sonuçtur: model
// hatayı görür ve kullanıcıya devir kartı anlatır ("Google bağlı değil…").
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/esmworks/OpenAssist/internal/policy"
)

// ServerName — mcp-config'te sunucu adı; araçlar mcp__gw__<ad> olur.
// Onay proxy'sinin "oa" adıyla çakışmaz.
const ServerName = "gw"

// protocolVersion — desteklediğimiz MCP sürümü (Streamable HTTP).
const protocolVersion = "2025-06-18"

// Tool — gateway aracı: şema bizim kontratımız (katı), sınıf açık bildirim.
type Tool struct {
	Name        string
	Description string
	// InputSchema — JSON Schema (draft 2020-12); motor argümanları buna göre üretir.
	InputSchema json.RawMessage
	// Class — onay kapısına açık bildirim (K17): sezgisel devre dışı kalır.
	Class policy.Classification
	// Handler — metin içerik döner; hata isError'lı sonuç olur.
	Handler func(ctx context.Context, args json.RawMessage) (string, error)
}

type Gateway struct {
	tools []Tool
	byIdx map[string]int
	// source — dinamik araç kaynağı (runner köprüsü, K7): her tools/list ve
	// tools/call anında çağrılır. nil ise statik kayıt defteri kullanılır.
	source func() []Tool
}

func New() *Gateway {
	return &Gateway{byIdx: map[string]int{}}
}

// NewDynamic — araç listesi her istekte kaynaktan okunan gateway. Mac
// runner'ın "bağlıyken var, değilken yok" davranışı buradan gelir (K2).
func NewDynamic(source func() []Tool) *Gateway {
	return &Gateway{byIdx: map[string]int{}, source: source}
}

// current — bu isteğin araç kümesi.
func (g *Gateway) current() []Tool {
	if g.source != nil {
		return g.source()
	}
	return g.tools
}

// Register — kablolama sırasında çağrılır (sunum başladıktan sonra değil;
// kilit yok). Dinamik gateway'de kullanılmaz.
func (g *Gateway) Register(t Tool) {
	if g.source != nil {
		panic("gateway: dinamik gateway'e statik araç kaydedilemez")
	}
	if _, dup := g.byIdx[t.Name]; dup {
		panic(fmt.Sprintf("gateway: araç iki kez kayıtlı: %s", t.Name))
	}
	g.byIdx[t.Name] = len(g.tools)
	g.tools = append(g.tools, t)
}

// Tools — bu anın araç kümesinin kopyası (test ve tanılama).
func (g *Gateway) Tools() []Tool {
	cur := g.current()
	out := make([]Tool, len(cur))
	copy(out, cur)
	return out
}

// Classifications — onay kapısı için açık sınıf haritası
// (mcp__gw__<araç> → sınıf). Dinamik gateway'de sınıflar bu anın kümesini
// yansıtır; runner gibi sabit setli kaynaklar için StaticClasses tercih edilir
// (orkestratör haritası açılışta kurulur).
func (g *Gateway) Classifications() map[string]policy.Classification {
	cur := g.current()
	out := make(map[string]policy.Classification, len(cur))
	for _, t := range cur {
		out[fmt.Sprintf("mcp__%s__%s", ServerName, t.Name)] = t.Class
	}
	return out
}

// ── JSON-RPC 2.0 / MCP Streamable HTTP ─────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Handler — POST /mcp. Tek mesaj kabul edilir (MCP 2025-03-26+ batch'i
// kaldırdı); yanıt application/json (SSE modu gerekmiyor: araçlarımız kısa
// ömürlü tek sonuç döner).
func (g *Gateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// Spec: GET stream'i desteklemeyen sunucu 405 döner.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req rpcRequest
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
			return
		}

		// Bildirimler (id yok) cevap beklemez: 202.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			resp.Result = map[string]any{
				"protocolVersion": negotiate(req.Params),
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "openassist-gateway", "version": "1"},
			}
		case "ping":
			resp.Result = map[string]any{}
		case "tools/list":
			resp.Result = g.listTools()
		case "tools/call":
			resp = g.callTool(r.Context(), req)
		default:
			resp.Error = &rpcError{Code: -32601, Message: "yöntem yok: " + req.Method}
		}
		writeRPC(w, resp)
	})
}

// negotiate — istemcinin sürümünü tanıyorsak aynen, yoksa bizimkini döneriz.
func negotiate(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	switch p.ProtocolVersion {
	case "2025-03-26", "2025-06-18":
		return p.ProtocolVersion
	}
	return protocolVersion
}

func (g *Gateway) listTools() map[string]any {
	cur := g.current()
	tools := make([]map[string]any, 0, len(cur))
	for _, t := range cur {
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return map[string]any{"tools": tools}
}

func (g *Gateway) callTool(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		resp.Error = &rpcError{Code: -32602, Message: "geçersiz params: " + err.Error()}
		return resp
	}
	var tool *Tool
	for _, t := range g.current() {
		if t.Name == p.Name {
			t := t
			tool = &t
			break
		}
	}
	if tool == nil {
		resp.Error = &rpcError{Code: -32602, Message: "bilinmeyen araç: " + p.Name}
		return resp
	}
	text, err := tool.Handler(ctx, p.Arguments)
	if err != nil {
		// Araç hatası model'e görünür sonuçtur (devir kartı mekanizması).
		resp.Result = toolResult(err.Error(), true)
		return resp
	}
	resp.Result = toolResult(text, false)
	return resp
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Gövde yarıda kalır; istemci parse hatasıyla anlar.
		return
	}
}
