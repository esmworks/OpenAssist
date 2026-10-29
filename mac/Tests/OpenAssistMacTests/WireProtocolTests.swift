import XCTest

@testable import OpenAssistMac

/// Tel protokolü Go internal/runner ile birebir olmalı: alan adları ve
/// çerçeve tipleri buradaki altın örneklerle sabitlenir.
final class WireProtocolTests: XCTestCase {
    func testHelloFrameShape() throws {
        let hello = HelloFrame(name: "test-mac", version: "0.1.0", tools: MacTools.declarations)
        let data = try API.encoder.encode(hello)
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(obj["type"] as? String, "hello")
        let tools = try XCTUnwrap(obj["tools"] as? [[String: Any]])
        XCTAssertEqual(tools.count, 4)
        let names = tools.compactMap { $0["name"] as? String }.sorted()
        XCTAssertEqual(names, ["list_dir", "read_file", "trash_item", "write_file"])
        // Go tarafı `input_schema` ve `class` anahtarlarını okur.
        for tool in tools {
            XCTAssertNotNil(tool["input_schema"], "input_schema anahtarı şart")
            XCTAssertNotNil(tool["class"], "class anahtarı şart")
        }
        // Sınıflar Go runner.StaticClasses ile uyumlu.
        let byName = Dictionary(uniqueKeysWithValues: tools.map { ($0["name"] as! String, $0["class"] as? String) })
        XCTAssertEqual(byName["read_file"], "read")
        XCTAssertEqual(byName["list_dir"], "read")
        XCTAssertEqual(byName["write_file"], "write")
        XCTAssertEqual(byName["trash_item"], "write")
    }

    func testCallFrameDecode() throws {
        let json = #"{"type":"call","id":"abc","tool":"read_file","args":{"path":"/tmp/x.txt"}}"#
        let call = try API.decoder.decode(CallFrame.self, from: Data(json.utf8))
        XCTAssertEqual(call.tool, "read_file")
        XCTAssertEqual(call.args?.objectValue?["path"]?.stringValue, "/tmp/x.txt")
    }

    func testResultFrameShape() throws {
        let res = ResultFrame(id: "abc", ok: false, error: "izin reddedildi")
        let obj = try XCTUnwrap(
            JSONSerialization.jsonObject(with: API.encoder.encode(res)) as? [String: Any])
        XCTAssertEqual(obj["type"] as? String, "result")
        XCTAssertEqual(obj["id"] as? String, "abc")
        XCTAssertEqual(obj["ok"] as? Bool, false)
        XCTAssertEqual(obj["error"] as? String, "izin reddedildi")
    }

    func testInputSchemaIsStrictObject() throws {
        for decl in MacTools.declarations {
            let obj = try XCTUnwrap(decl.inputSchema.objectValue, decl.name)
            XCTAssertEqual(obj["additionalProperties"], .bool(false),
                           "\(decl.name): kendi kontratımız katıdır")
            XCTAssertNotNil(obj["properties"]?.objectValue?["path"], decl.name)
        }
    }
}
