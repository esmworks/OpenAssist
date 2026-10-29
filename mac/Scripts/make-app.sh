#!/usr/bin/env bash
# OpenAssist.app paketi.
#
# Neden gerekli: çıplak SPM executable'ı macOS için "paketsiz süreç"tir ve
# üç şey çalışmaz —
#   · bildirimler (UNUserNotificationCenter paket kimliği olmadan istisna atar)
#   · girişte başlat (SMAppService paket ister)
#   · Dock'ta doğru ad/ikon
# Ayarlar'daki karşılıkları bu yüzden yalnız paketli koşuda etkindir;
# paketsiz koşuda arayüz "paket olarak çalışmıyor" der (uydurma yapmaz).
#
# Kullanım:  ./Scripts/make-app.sh [debug|release]
set -euo pipefail
cd "$(dirname "$0")/.."

CONF="${1:-release}"
APP="${APP_OUT:-.build/OpenAssist.app}"
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)"
BUILD="$(git rev-list --count HEAD 2>/dev/null || echo 0)"

swift build -c "$CONF"
BIN=".build/$CONF/OpenAssistMac"
[ -x "$BIN" ] || { echo "derleme çıktısı yok: $BIN" >&2; exit 1; }

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
cp "$BIN" "$APP/Contents/MacOS/OpenAssist"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>OpenAssist</string>
  <key>CFBundleDisplayName</key><string>OpenAssist</string>
  <key>CFBundleIdentifier</key><string>com.openassist.mac</string>
  <key>CFBundleExecutable</key><string>OpenAssist</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${BUILD}</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# Ad-hoc imza: imzasız pakette bildirim izni ve giriş öğesi kaydı reddedilir.
codesign --force --sign - "$APP" >/dev/null

echo "$APP  (sürüm ${VERSION}, derleme ${BUILD})"
