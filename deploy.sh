#!/usr/bin/env bash
# 用法: deploy.sh start|stop|restart|status [cn|sg]
# 示例:
#   ./deploy.sh start cn       # 使用 .env.cn 启动
#   ./deploy.sh restart sg     # 使用 .env.sg 重启
#   ./deploy.sh start          # 使用默认 .env 启动
#   ./deploy.sh stop           # 停止（不需要指定区域）

set -euo pipefail

APP_NAME="easytv-api"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_DIR="${APP_DIR:-${SCRIPT_DIR}}"
BINARY="${APP_DIR}/${APP_NAME}"
LOG_DIR="${APP_DIR}/logs"
PID_FILE="${APP_DIR}/${APP_NAME}.pid"
PORT="${PORT:-8383}"

ACTION="${1:-restart}"
REGION="${2:-}"

# 根据区域参数确定 env 文件
ENV_FILE=".env"
if [[ -n "${REGION}" ]]; then
  ENV_FILE=".env.${REGION}"
  if [[ ! -f "${APP_DIR}/${ENV_FILE}" ]]; then
    echo "error: env file not found: ${APP_DIR}/${ENV_FILE}"
    exit 1
  fi
fi

mkdir -p "${LOG_DIR}"

start() {
  if [[ -f "${PID_FILE}" ]] && kill -0 "$(cat "${PID_FILE}")" 2>/dev/null; then
    echo "${APP_NAME} already running (pid $(cat "${PID_FILE}"))"
    return 0
  fi
  if [[ ! -x "${BINARY}" ]]; then
    echo "binary not found: ${BINARY}"
    exit 1
  fi
  cd "${APP_DIR}"
  export PORT
  echo "starting ${APP_NAME} with env file: ${ENV_FILE}"
  nohup "${BINARY}" --port "${PORT}" --log-dir "${LOG_DIR}" --env-file "${ENV_FILE}" \
    >>"${LOG_DIR}/stdout.log" 2>>"${LOG_DIR}/stderr.log" &
  echo $! > "${PID_FILE}"
  sleep 1
  if ! kill -0 "$(cat "${PID_FILE}")" 2>/dev/null; then
    echo "failed to start ${APP_NAME} (process exited). Last stderr:"
    tail -5 "${LOG_DIR}/stderr.log" 2>/dev/null || true
    rm -f "${PID_FILE}"
    exit 1
  fi
  echo "started ${APP_NAME} pid $(cat "${PID_FILE}") on http://127.0.0.1:${PORT}/"
}

stop() {
  if [[ -f "${PID_FILE}" ]]; then
    pid="$(cat "${PID_FILE}")"
    if kill -0 "${pid}" 2>/dev/null; then
      kill "${pid}" || true
      sleep 2
      kill -9 "${pid}" 2>/dev/null || true
    fi
    rm -f "${PID_FILE}"
  fi
  echo "stopped ${APP_NAME}"
}

status() {
  if [[ -f "${PID_FILE}" ]] && kill -0 "$(cat "${PID_FILE}")" 2>/dev/null; then
    echo "${APP_NAME} running (pid $(cat "${PID_FILE}"))"
    return 0
  fi
  echo "${APP_NAME} not running"
  return 1
}

restart() {
  stop
  start
}

case "${ACTION}" in
  start) start ;;
  stop) stop ;;
  restart) restart ;;
  status) status ;;
  *)
    echo "usage: $0 {start|stop|restart|status} [cn|sg]"
    exit 1
    ;;
esac
