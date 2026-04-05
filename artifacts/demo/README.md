# Demo Capture Workflow

The repo does not commit a large binary demo asset by default. The source of truth is the reproducible capture workflow below.

## Canonical Output Paths

- terminal transcript: `artifacts/demo/irongate-demo-<timestamp>.typescript`
- terminal log: `artifacts/demo/irongate-demo-<timestamp>.log`
- optional MP4: `artifacts/demo/irongate-demo-<timestamp>.mp4`
- optional GIF: derive from the MP4 outside the repo if you need a lighter shareable asset

## Capture A Fresh 2-Minute Demo

1. Make sure Docker Desktop is running.
2. If you want video capture, install `ffmpeg`:

   ```bash
   brew install ffmpeg
   ffmpeg -f avfoundation -list_devices true -i ""
   ```

3. Pick the terminal display source from the printed device list and export it, for example:

   ```bash
   export IRONGATE_CAPTURE_SOURCE="1:none"
   ```

4. Run:

   ```bash
   ./scripts/capture-demo.sh
   ```

The script always records the terminal transcript and demo log. If `ffmpeg` is installed and `IRONGATE_CAPTURE_SOURCE` is set, it also records an MP4 in the same directory while `./demo.sh` runs.

## Storage Guidance

- Keep the generated MP4 or GIF out of Git when it is large.
- If you need a shareable asset for a PR or portfolio page, upload the generated MP4 or derived GIF as a release asset or external file link and reference that URL in the PR description.
