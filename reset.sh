#!/usr/bin/env bash
# reset.sh — остановить бота и очистить все данные для чистой установки
set -euo pipefail

SERVICE_NAME="vpnbot"
INSTALL_DIR="/opt/vpnbot"
DATA_DIR="/var/lib/vpnbot"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

if [[ $EUID -ne 0 ]]; then
    error "Запускайте от root: sudo bash reset.sh"
fi

# -- Остановка и отключение службы ------------------------------------------
if systemctl list-units --full -all 2>/dev/null | grep -q "${SERVICE_NAME}.service"; then
    info "Остановка службы ${SERVICE_NAME}..."
    systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
    systemctl disable "${SERVICE_NAME}" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    info "Служба остановлена и удалена."
else
    warn "Служба ${SERVICE_NAME} не найдена, пропуск."
fi

# -- Удаление бинарника и конфига -------------------------------------------
if [[ -d "${INSTALL_DIR}" ]]; then
    info "Удаление ${INSTALL_DIR}..."
    rm -rf "${INSTALL_DIR}"
fi

# -- База данных -------------------------------------------------------------
echo ""
warn "Удалить базу данных в ${DATA_DIR}? Это сотрёт всех пользователей и балансы!"
warn "Введите 'yes' для удаления или нажмите Enter для сохранения:"
read -r CONFIRM
if [[ "${CONFIRM}" == "yes" ]]; then
    if [[ -d "${DATA_DIR}" ]]; then
        rm -rf "${DATA_DIR}"
        info "База данных удалена."
    fi
else
    info "База данных сохранена в ${DATA_DIR}."
fi

# -- Go module cache (опционально) ------------------------------------------
echo ""
warn "Очистить кэш Go-модулей? (ускорит go mod tidy, но следующая сборка дольше скачивает зависимости)"
warn "Введите 'yes' для очистки или Enter для пропуска:"
read -r CONFIRM_GO
if [[ "${CONFIRM_GO}" == "yes" ]]; then
    go clean -modcache 2>/dev/null || true
    info "Кэш Go-модулей очищен."
fi

echo ""
info "Готово. Теперь запустите установку заново:"
info "  sudo bash install.sh"
