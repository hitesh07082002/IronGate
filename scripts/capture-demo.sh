#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

OUT_DIR="${IRONGATE_DEMO_OUT_DIR:-artifacts/demo}"
STAMP="$(date +%Y%m%d-%H%M%S)"
TRANSCRIPT_PATH="${OUT_DIR}/irongate-demo-${STAMP}.typescript"
LOG_PATH="${OUT_DIR}/irongate-demo-${STAMP}.log"
VIDEO_PATH="${OUT_DIR}/irongate-demo-${STAMP}.mp4"
CAPTURE_SECONDS="${IRONGATE_CAPTURE_SECONDS:-120}"
CAPTURE_OS="$(uname -s)"

mkdir -p "${OUT_DIR}"

capture_pid=""
cleanup() {
  if [[ -n "${capture_pid}" ]] && kill -0 "${capture_pid}" >/dev/null 2>&1; then
    kill "${capture_pid}" >/dev/null 2>&1 || true
    wait "${capture_pid}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "Recording terminal transcript to ${TRANSCRIPT_PATH}"

if command -v ffmpeg >/dev/null 2>&1 && [[ -n "${IRONGATE_CAPTURE_SOURCE:-}" ]]; then
  if [[ "${CAPTURE_OS}" == "Darwin" ]]; then
    echo "Recording macOS screen capture to ${VIDEO_PATH} using avfoundation source ${IRONGATE_CAPTURE_SOURCE}"
    ffmpeg -y \
      -f avfoundation \
      -framerate "${IRONGATE_CAPTURE_FRAMERATE:-30}" \
      -i "${IRONGATE_CAPTURE_SOURCE}" \
      -t "${CAPTURE_SECONDS}" \
      "${VIDEO_PATH}" \
      >/dev/null 2>&1 &
    capture_pid="$!"
  else
    echo "ffmpeg capture is configured, but this script only wires automatic video capture for macOS avfoundation."
    echo "Transcript-only mode is active. See artifacts/demo/README.md for Linux and Windows capture notes."
  fi
else
  echo "No ffmpeg capture configured. Transcript-only mode is active."
  if [[ "${CAPTURE_OS}" == "Darwin" ]]; then
    echo "Set IRONGATE_CAPTURE_SOURCE after checking devices with:"
    echo "  ffmpeg -f avfoundation -list_devices true -i \"\""
  else
    echo "See artifacts/demo/README.md for Linux and Windows capture examples."
  fi
fi

DEMO_SHELL="${SHELL:-}"
if [[ -z "${DEMO_SHELL}" || ! -x "${DEMO_SHELL}" ]]; then
  if command -v bash >/dev/null 2>&1; then
    DEMO_SHELL="$(command -v bash)"
  else
    DEMO_SHELL="/bin/sh"
  fi
fi

if [[ "${CAPTURE_OS}" == "Darwin" ]]; then
  script -q "${TRANSCRIPT_PATH}" "${DEMO_SHELL}" -c "./demo.sh" | tee "${LOG_PATH}"
else
  printf -v SCRIPT_COMMAND "%q -c %q" "${DEMO_SHELL}" "./demo.sh"
  script -q -c "${SCRIPT_COMMAND}" "${TRANSCRIPT_PATH}" | tee "${LOG_PATH}"
fi

echo
echo "Artifacts:"
echo "  transcript: ${TRANSCRIPT_PATH}"
echo "  log:        ${LOG_PATH}"
if [[ -f "${VIDEO_PATH}" ]]; then
  echo "  video:      ${VIDEO_PATH}"
fi
