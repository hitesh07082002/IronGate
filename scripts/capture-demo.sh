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
  echo "Recording screen capture to ${VIDEO_PATH} using source ${IRONGATE_CAPTURE_SOURCE}"
  ffmpeg -y \
    -f avfoundation \
    -framerate "${IRONGATE_CAPTURE_FRAMERATE:-30}" \
    -i "${IRONGATE_CAPTURE_SOURCE}" \
    -t "${CAPTURE_SECONDS}" \
    "${VIDEO_PATH}" \
    >/dev/null 2>&1 &
  capture_pid="$!"
else
  echo "No ffmpeg capture configured. Transcript-only mode is active."
  echo "Set IRONGATE_CAPTURE_SOURCE after checking devices with:"
  echo "  ffmpeg -f avfoundation -list_devices true -i \"\""
fi

script -q "${TRANSCRIPT_PATH}" /bin/zsh -lc "./demo.sh" | tee "${LOG_PATH}"

echo
echo "Artifacts:"
echo "  transcript: ${TRANSCRIPT_PATH}"
echo "  log:        ${LOG_PATH}"
if [[ -f "${VIDEO_PATH}" ]]; then
  echo "  video:      ${VIDEO_PATH}"
fi
