# Сборка host-агента (Windows / Linux)

Собирается на любой машине с Go (кросс-компиляция, не важно откуда — с
Mac/Linux/Windows). Требуется только `go` и `make`.

Все переменные ниже — реальные значения текущего сервера
(`146.103.40.203`, домены `rdpdemo.bestquiz.uz` / `turn.bestquiz.uz`).
`TURN_CREDENTIAL` актуальный пароль TURN — если он сменится, взять новый
из `/etc/turnserver.conf` на сервере (или из места, где он был сохранён
при выпуске).

```sh
cd rdpAiAnswer

SERVER="wss://rdpdemo.bestquiz.uz/ws/host"
ICE_SERVERS="stun:146.103.40.203:3478,turn:146.103.40.203:3478,turns:turn.bestquiz.uz:443?transport=tcp"
TURN_USERNAME="rdp"
TURN_CREDENTIAL="7sgxTSmvCqwTXiLRLT6oNUyl"
```

## Windows

```sh
make host SERVER="$SERVER" ICE_SERVERS="$ICE_SERVERS" \
  TURN_USERNAME="$TURN_USERNAME" TURN_CREDENTIAL="$TURN_CREDENTIAL"
```

Результат: `bin/rdp-host.exe` — один файл, двойной клик запускает без
консоли (собран с `-H windowsgui`). Лог пишется в `rdp-host.log` рядом с
exe, либо в `%TEMP%`, если та папка недоступна на запись.

## Linux (X11)

```sh
make host-linux SERVER="$SERVER" ICE_SERVERS="$ICE_SERVERS" \
  TURN_USERNAME="$TURN_USERNAME" TURN_CREDENTIAL="$TURN_CREDENTIAL"
```

Результат: `bin/rdp-host-linux` — голый исполняемый файл. Работает под
X11 (не Wayland — проверить сессию: `echo $XDG_SESSION_TYPE`).

Для двойного клика без терминала есть готовый лаунчер:

```sh
make host-linux-package SERVER="$SERVER" ICE_SERVERS="$ICE_SERVERS" \
  TURN_USERNAME="$TURN_USERNAME" TURN_CREDENTIAL="$TURN_CREDENTIAL"
```

Результат: `bin/rdp-host-linux.tar.gz`, внутри 2 файла —
`rdp-host-linux` и `RDP-Host.desktop`. Кладутся рядом куда угодно (диск,
флешка любой ФС), двойной клик по `RDP-Host.desktop` запускает —
`.desktop` сам находит бинарник рядом с собой, копирует во временную
папку и запускает оттуда (поэтому работает даже если флешка
смонтирована с `noexec`). Подробности — комментарии в
`packaging/linux/RDP-Host.desktop` и цель `host-linux-package` в
`Makefile`.

## Сервер (сигнальный + admin UI)

```sh
make server ADDR=":9000" ICE_SERVERS="$ICE_SERVERS" \
  TURN_USERNAME="$TURN_USERNAME" TURN_CREDENTIAL="$TURN_CREDENTIAL"
```

Результат: `bin/rdp-server`, деплой — см. `docs/deploy/vps-setup.md`.
Текущий продовый инстанс собран из `/opt/rdp-tool-src` (git clone этого
репо) и запущен как systemd-сервис `rdp-server` на `146.103.40.203`.
