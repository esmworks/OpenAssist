// oa-gateway — OpenAssist'in pi eklentisi.
//
// pi çekirdeği bilinçli olarak küçüktür: yerleşik MCP'si, alt ajanı ve onay
// penceresi YOKTUR (pi docs/usage.md). OpenAssist'in üç yüzeyi bu yüzden
// buradan girer:
//
//   1. Onay kapısı — her araç çağrısı, karar OpenAssist'te verilsin diye
//      koşuyu yöneten sürece sorulur (kural motoru, Telegram/Mac kartları,
//      park/resume penceresi). pi'nin kendi izin sistemi olmadığı için bu
//      kapı olmadan bot her şeyi sorgusuz yapardı.
//   2. Gateway araçları — OpenAssist'in MCP yüzeylerindeki araçlar
//      (bot/rutin/hafıza yönetimi, konektörler, Mac runner).
//   3. ask_user / ask_bot — koşu ortasında soru sorma ve işi başka bota
//      devretme (ADIM 6).
//
// ARAÇ ADLARI: gateway araçları `mcp__<yüzey>__<araç>` adıyla kaydedilir.
// Bu ad MCP'den miras kalan bir süs değil, sistemin kimliğidir: politika
// motoru, araç sınıfları ve bot allowlist'leri bu adlara göre eşleşir.
// Çağrı yapılırken önek soyulur, gateway kendi kısa adını görür.
//
// Süreç ortamından okunur (argv'ye yazılmaz — `ps` çıktısında görünmesin):
//   OA_GATEWAY_URLS  — "ad=url" çiftleri, virgülle ayrılmış
//   OA_GATEWAY_TOKEN — bearer belirteci (varsa)

import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

type Yuzey = { ad: string; url: string };

const YUZEYLER: Yuzey[] = (process.env.OA_GATEWAY_URLS ?? "")
  .split(",")
  .map((p) => p.trim())
  .filter(Boolean)
  .map((p) => {
    const i = p.indexOf("=");
    return i > 0 ? { ad: p.slice(0, i).trim(), url: p.slice(i + 1).trim() } : null;
  })
  .filter((y): y is Yuzey => y !== null && y.ad !== "" && y.url !== "");

const GATEWAY_TOKEN = process.env.OA_GATEWAY_TOKEN ?? "";

// ASK_MARKER — bu diyaloğun OpenAssist'e ait olduğunu söyleyen işaret.
// Adaptör (internal/engine/pi/ask.go) title'a bakar; placeholder yükün
// JSON'u, dönen değer de cevabın JSON'udur. `input` seçildi çünkü serbest
// metin döndürür: `select` olsaydı değer bir seçenek listesine sıkışırdı.
const ASK_MARKER = "oa-ask";

// Araç adları orkestratörle paylaşılan sabitlerdir: devir akışı
// mcp__oa__ask_bot adını arar (internal/orchestrator).
const ASK_USER = "mcp__oa__ask_user";
const ASK_BOT = "mcp__oa__ask_bot";
const SEND_FILE = "mcp__oa__send_file";

type AskReply = { behavior?: string; message?: string; always?: boolean };

async function ask(
  ctx: ExtensionContext,
  envelope: Record<string, unknown>,
): Promise<AskReply> {
  const reply = await ctx.ui.input(ASK_MARKER, JSON.stringify(envelope));
  if (!reply) {
    // İptal/zaman aşımı = ret. Fail-closed: cevabı okuyamadığımızda
    // aracı çalıştırmayız.
    return { behavior: "deny", message: "OpenAssist: onay alınamadı" };
  }
  try {
    return JSON.parse(reply) as AskReply;
  } catch {
    return { behavior: "deny", message: "OpenAssist: onay cevabı çözülemedi" };
  }
}

let rpcId = 0;

// Gateway çağrılarının ZAMAN AŞIMI olmak zorunda: pi session_start
// kancasını promptu işlemeden ÖNCE bekler (0.84.2'de ölçüldü), yani
// cevapsız bir tools/list turu hiç başlatmaz. Süresiz fetch, sessizce
// asılı kalan bir koşu demekti.
const ZAMAN_ASIMI_MS = 15_000;

function zamanliSinyal(signal?: AbortSignal): AbortSignal {
  const sure = AbortSignal.timeout(ZAMAN_ASIMI_MS);
  // Aracın kendi iptali de geçerli: hangisi önce gelirse istek düşer.
  return signal ? AbortSignal.any([signal, sure]) : sure;
}

async function gatewayRPC(
  url: string,
  method: string,
  params: unknown,
  signal?: AbortSignal,
): Promise<any> {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (GATEWAY_TOKEN) headers.authorization = `Bearer ${GATEWAY_TOKEN}`;

  const res = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify({ jsonrpc: "2.0", id: ++rpcId, method, params }),
    signal: zamanliSinyal(signal),
  });
  if (!res.ok) throw new Error(`gateway ${method}: HTTP ${res.status}`);

  const body = (await res.json()) as { result?: any; error?: { message?: string } };
  if (body.error) throw new Error(`gateway ${method}: ${body.error.message ?? "bilinmeyen hata"}`);
  return body.result;
}

function metinSonuc(text: string, isError = false) {
  return { content: [{ type: "text" as const, text }], details: {}, isError };
}

export default function (pi: ExtensionAPI) {
  // ── 1. Onay kapısı ───────────────────────────────────────────────────
  //
  // Her araç çağrısı OpenAssist'e sorulur. Hızlı yol da orada: kural
  // motoru güvenli sınıfları anında geçirir, yalnız gerçekten karar
  // gerektiren çağrı kullanıcıya kart olarak düşer.
  //
  // ask_user/ask_bot kendileri zaten insana gider; onları ikinci kez
  // onaya sokmak her soruyu çift kart yapardı.
  pi.on("tool_call", async (event, ctx) => {
    // ask_user/ask_bot/send_file kendileri zaten OpenAssist'e gider ve
    // kararı orada verilir; ikinci kez onaya sokmak her çağrıyı çift
    // tur yapardı.
    if (event.toolName === ASK_USER || event.toolName === ASK_BOT || event.toolName === SEND_FILE) {
      return undefined;
    }

    const ans = await ask(ctx, {
      kind: "permission",
      tool: event.toolName,
      input: event.input,
    });
    if (ans.behavior !== "allow") {
      return { block: true, reason: ans.message || "OpenAssist onayı verilmedi" };
    }
    return undefined;
  });

  // ── 2. ask_user / ask_bot ────────────────────────────────────────────
  pi.registerTool({
    name: ASK_USER,
    label: "Kullanıcıya sor",
    description:
      "Bu botun sahibine bir soru sor ve cevabını bekle. Bir karar, tercih, eksik bilgi " +
      "ya da onay gerektiğinde kullan — sahibinin karar vermek isteyeceği konularda tahmin " +
      "yürütme. Cevap metin olarak döner.",
    parameters: {
      type: "object",
      properties: {
        question: {
          type: "string",
          description: "Tek bakışta cevaplanacak kadar bağlam içeren soru",
        },
        choices: {
          type: "array",
          items: { type: "string" },
          description: "İsteğe bağlı 2-5 önerilen cevap; tek-tık buton olarak gösterilir",
        },
      },
      required: ["question"],
    } as any,
    async execute(_id, params: any, _signal, _onUpdate, ctx) {
      const ans = await ask(ctx, {
        kind: "question",
        question: params?.question ?? "",
        choices: params?.choices ?? [],
      });
      return metinSonuc(ans.message ?? "");
    },
  });

  pi.registerTool({
    name: ASK_BOT,
    label: "Bota devret",
    description:
      "İşi başka bir bota devret. Hedef botu list_bots ile görüp id'sini (bot_id) ya da tam " +
      "adını (bot_name) ver; message'a devredilecek işi eksiksiz yaz — karşıdaki bot yalnız " +
      "bunu görür. Devir hemen açılır, karşı bot işi kendi sohbetinde yapar; sonucu " +
      "'← … cevabı' çipiyle senin sohbetine düşer.",
    parameters: {
      type: "object",
      properties: {
        bot_id: {
          type: "string",
          description: "Hedef botun UUID'si (list_bots'tan kopyala). bot_name verilmediyse zorunlu.",
        },
        bot_name: {
          type: "string",
          description: "Hedef botun tam adı (bot_id verilmediyse zorunlu).",
        },
        message: {
          type: "string",
          description: "Devredilecek iş — karşı botun göreceği mesajın tamamı",
        },
      },
      required: ["message"],
    } as any,
    async execute(_id, params: any, _signal, _onUpdate, ctx) {
      const ans = await ask(ctx, {
        kind: "bot_message",
        bot_id: params?.bot_id ?? "",
        bot_name: params?.bot_name ?? "",
        message: params?.message ?? "",
      });
      return metinSonuc(ans.message ?? "");
    },
  });

  pi.registerTool({
    name: SEND_FILE,
    label: "Dosya gönder",
    description:
      "Ürettiğin bir dosyayı kullanıcıya GÖNDER (rapor, grafik, dönüştürülmüş belge, ekran " +
      "görüntüsü). Dosyayı önce çalışma dizinine yaz, sonra yolunu bu araca ver: dosya sohbete " +
      "ek olarak düşer ve Telegram'a da gönderilir. Yalnız kendi çalışma dizinindeki dosyalar " +
      "gönderilebilir. Dosyanın kendisi cevabına eklenir — içeriğini ayrıca metne kopyalama.",
    parameters: {
      type: "object",
      properties: {
        path: {
          type: "string",
          description: "Gönderilecek dosyanın yolu (çalışma dizinine göreli ya da mutlak)",
        },
        name: {
          type: "string",
          description: "İsteğe bağlı görünen dosya adı; verilmezse yoldaki ad kullanılır",
        },
      },
      required: ["path"],
    } as any,
    async execute(_id, params: any, _signal, _onUpdate, ctx) {
      const ans = await ask(ctx, {
        kind: "file",
        path: params?.path ?? "",
        name: params?.name ?? "",
      });
      return metinSonuc(ans.message ?? "", ans.behavior !== "allow");
    },
  });

  // ── 3. Gateway araçları ──────────────────────────────────────────────
  //
  // session_start'ta kaydedilir: pi kayıt sonrası araç listesini aynı
  // oturumda tazeler, /reload gerekmez.
  pi.on("session_start", async (_event, ctx) => {
    for (const yuzey of YUZEYLER) {
      let tools: Array<{ name: string; description?: string; inputSchema?: unknown }> = [];
      try {
        const result = await gatewayRPC(yuzey.url, "tools/list", {});
        tools = result?.tools ?? [];
      } catch (err) {
        // Bir yüzey ölüyse koşu yine de yürüsün: diğer yüzey ve yerleşik
        // araçlarla çalışır. Sessiz kalmıyoruz — kullanıcı "araçlarım
        // nerede" diye sorduğunda cevap kayıtta olsun.
        ctx.ui.notify(
          `OpenAssist araçları yüklenemedi (${yuzey.ad}): ${err instanceof Error ? err.message : String(err)}`,
          "warning",
        );
        continue;
      }

      for (const tool of tools) {
        if (!tool.name) continue;
        const kayitliAd = `mcp__${yuzey.ad}__${tool.name}`;
        pi.registerTool({
          name: kayitliAd,
          label: tool.name,
          description: tool.description ?? "",
          // Gateway JSON Schema döner; pi'nin beklediği typebox şeması da
          // çalışma anında düz JSON Schema nesnesidir, olduğu gibi geçer.
          parameters: (tool.inputSchema ?? { type: "object", properties: {} }) as any,
          async execute(_toolCallId, params, signal) {
            try {
              // Gateway kendi KISA adını bekler; önek yalnız bizim
              // tarafımızdaki kimliktir.
              const result = await gatewayRPC(
                yuzey.url,
                "tools/call",
                { name: tool.name, arguments: params },
                signal,
              );
              const content = result?.content ?? [{ type: "text", text: "" }];
              return { content, details: {}, isError: result?.isError === true };
            } catch (err) {
              return metinSonuc(err instanceof Error ? err.message : String(err), true);
            }
          },
        });
      }
    }
  });
}
