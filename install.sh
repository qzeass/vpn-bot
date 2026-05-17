#!/usr/bin/env bash
set -euo pipefail

GO_VERSION="1.22.4"
INSTALL_DIR="/opt/vpnbot"
DATA_DIR="/var/lib/vpnbot"
SERVICE_NAME="vpnbot"
BINARY_NAME="vpnbot"
CONFIG_FILE="config.yaml"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

if [[ $EUID -ne 0 ]]; then
    error "Этот скрипт необходимо запускать от root или через sudo."
fi

info "Обновление списка пакетов..."
apt-get update -q

info "Установка зависимостей (curl, git, gcc)..."
apt-get install -y -q curl git gcc

GO_ARCHIVE="go${GO_VERSION}.linux-amd64.tar.gz"
GO_URL="https://go.dev/dl/${GO_ARCHIVE}"

if command -v go &>/dev/null; then
    CURRENT_GO=$(go version | awk '{print $3}' | sed 's/go//')
    info "Go уже установлен: версия $CURRENT_GO"
else
    info "Загрузка Go ${GO_VERSION}..."
    curl -fsSL "${GO_URL}" -o "/tmp/${GO_ARCHIVE}"

    info "Установка Go в /usr/local..."
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${GO_ARCHIVE}"
    rm "/tmp/${GO_ARCHIVE}"

    if ! grep -q 'export PATH=.*\/usr\/local\/go\/bin' /etc/profile; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    fi
    export PATH=$PATH:/usr/local/go/bin
    info "Go ${GO_VERSION} установлен."
fi

export PATH=$PATH:/usr/local/go/bin
go version

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

info "Переход в директорию проекта: ${SCRIPT_DIR}"
cd "${SCRIPT_DIR}"

info "Загрузка зависимостей Go..."
go mod tidy
go mod download

info "Сборка бинарного файла..."
CGO_ENABLED=0 go build \
    -ldflags="-s -w" \
    -trimpath \
    -o "${BINARY_NAME}" \
    ./cmd/bot

info "Бинарный файл собран: ${SCRIPT_DIR}/${BINARY_NAME}"

info "Создание директорий..."
mkdir -p "${INSTALL_DIR}"
mkdir -p "${DATA_DIR}"

info "Копирование файлов..."
cp "${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

if [[ ! -f "${INSTALL_DIR}/${CONFIG_FILE}" ]]; then
    cp "${CONFIG_FILE}" "${INSTALL_DIR}/${CONFIG_FILE}"
    warn "Конфигурационный файл скопирован в ${INSTALL_DIR}/${CONFIG_FILE}"
    warn "Обязательно отредактируйте его перед запуском!"
else
    info "Конфигурационный файл уже существует, пропуск перезаписи."
fi

sed -i "s|path: \"/var/lib/vpnbot/vpnbot.db\"|path: \"${DATA_DIR}/vpnbot.db\"|g" \
    "${INSTALL_DIR}/${CONFIG_FILE}" 2>/dev/null || true

info "Создание systemd-службы..."
cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=VPN Telegram Bot
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BINARY_NAME} -config ${INSTALL_DIR}/${CONFIG_FILE}
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

LimitNOFILE=65536
MemoryMax=200M

[Install]
WantedBy=multi-user.target
EOF

info "Перезагрузка systemd..."
systemctl daemon-reload

info "Включение автозапуска службы..."
systemctl enable "${SERVICE_NAME}"

if [[ ! -f "${INSTALL_DIR}/${CONFIG_FILE}" ]] || \
   grep -q "YOUR_TELEGRAM_BOT_TOKEN" "${INSTALL_DIR}/${CONFIG_FILE}"; then
    warn "================================================================"
    warn "ВНИМАНИЕ: Настройте конфигурацию перед запуском!"
    warn "Файл конфигурации: ${INSTALL_DIR}/${CONFIG_FILE}"
    warn ""
    warn "Обязательные параметры:"
    warn "  - telegram.token       — токен бота из @BotFather"
    warn "  - xui.base_url         — URL 3X-UI панели (обычно http://localhost:PORT)"
    warn "  - xui.username         — логин 3X-UI"
    warn "  - xui.password         — пароль 3X-UI"
    warn "  - xui.inbound_id       — ID inbound с VLESS+Reality в 3X-UI"
    warn "  - admin.ids            — ваш Telegram ID (узнать у @userinfobot)"
    warn "  - payments.ton.wallet_address — адрес TON кошелька"
    warn ""
    warn "После настройки запустите:"
    warn "  systemctl start ${SERVICE_NAME}"
    warn "================================================================"
else
    info "Запуск службы..."
    systemctl start "${SERVICE_NAME}"
    sleep 2
    systemctl status "${SERVICE_NAME}" --no-pager || true
    info "Служба запущена. Логи: journalctl -u ${SERVICE_NAME} -f"
fi

info ""
info "Установка завершена!"
info "Управление службой:"
info "  Запуск:    systemctl start ${SERVICE_NAME}"
info "  Остановка: systemctl stop ${SERVICE_NAME}"
info "  Перезапуск: systemctl restart ${SERVICE_NAME}"
info "  Логи:      journalctl -u ${SERVICE_NAME} -f"
info "  Статус:    systemctl status ${SERVICE_NAME}"
