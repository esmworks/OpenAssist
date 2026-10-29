#!/bin/sh
# Kap root olarak başlar, /data sahipliğini düzeltir, sonra "node"a düşüp
# core'u exec eder.
#
# NEDEN: /data bind mount olarak gelir ve Docker eksik kaynak dizini ROOT
# olarak yaratır. İmajdaki chown bind mount tarafından örtülür, yani kap
# "node" kullanıcısıyla koşarken /data'ya yazamaz. Sonuç yalnız bir uyarı
# değildi: mkdir /data/workspaces düşünce koşunun çalışma dizini var olmuyor
# ve Linux'ta exec o durumda hatayı İKİLİYE yıkıyor —
# "fork/exec <motor>: no such file or directory" — motor gayet sağlamken
# saatlerce motor aranıyordu.
set -e

if [ "$(id -u)" = "0" ]; then
    # Özyineleme yalnız sahiplik yanlışken: /data büyüyebilir, her açılışta
    # ağacı gezmek deploy'u yavaşlatır. Yanlışsa zaten tek seferlik.
    if [ "$(stat -c %u /data 2>/dev/null)" != "1000" ]; then
        chown -R node:node /data 2>/dev/null || true
    fi
    # HOME ELLE: setpriv yalnız uid/gid değiştirir, ortamı olduğu gibi
    # bırakır — HOME "/root" kalırdı. Motor ve npm ev dizinine yazıyor,
    # node kullanıcısı /root'a yazamaz ve her koşu EACCES ile düşerdi.
    # (pi'nin kendi dizini ayrıca PI_CODING_AGENT_DIR ile /data'ya sabitlenir.)
    export HOME=/home/node
    # setpriv exec eder (araya süreç girmez): PID 1 core olur, SIGTERM
    # doğrudan ona ulaşır ve drain (stop_grace_period 60s) çalışır.
    exec setpriv --reuid=node --regid=node --init-groups /usr/local/bin/core "$@"
fi

exec /usr/local/bin/core "$@"
