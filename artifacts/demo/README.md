# Demo Capture Workflow

The project does not commit a large binary demo asset by default. The source of truth is the reproducible capture workflow below.

## Canonical Output Paths

- terminal transcript: `artifacts/demo/irongate-demo-<timestamp>.typescript`
- terminal log: `artifacts/demo/irongate-demo-<timestamp>.log`
- optional MP4: `artifacts/demo/irongate-demo-<timestamp>.mp4`
- optional GIF: derive from the MP4 outside the repo if you need a lighter shareable asset

## Capture A Fresh 2-Minute Demo

1. Make sure Docker is running.
2. Confirm the walkthrough itself works first:

   ```bash
   ./demo.sh
   ```

   `./demo.sh` now succeeds even if `k6` is not installed locally. In that case it skips the optional smoke benchmark and prints the follow-up command.

   If you want the stack left running while you prepare capture tools or inspect Grafana/Prometheus first, use:

   ```bash
   ./demo.sh --keep-stack
   ```

   When you are done with that preserved stack, stop it with:

   ```bash
   ./demo.sh --teardown
   ```
3. If you want video capture, install `ffmpeg`.

   macOS uses the built-in `avfoundation` path shown below:

   ```bash
   brew install ffmpeg
   ffmpeg -f avfoundation -list_devices true -i ""
   ```

   Linux note: use your desktop capture input instead, for example `ffmpeg -f x11grab -framerate 30 -video_size 1440x900 -i :0.0 output.mp4`, or your Wayland-friendly equivalent/OBS workflow.

   Windows note: use a Windows capture source such as `gdigrab` or a recorder like OBS, then keep `./scripts/capture-demo.sh` in transcript-only mode.

4. On macOS, pick the terminal display source from the printed device list and export it, for example:

   ```bash
   export IRONGATE_CAPTURE_SOURCE="1:none"
   ```

   Linux and Windows users should either leave `IRONGATE_CAPTURE_SOURCE` unset and rely on the transcript/log pair, or adapt the ffmpeg invocation above to their platform-specific capture device.

5. Run:

   ```bash
   ./scripts/capture-demo.sh
   ```

The script always records the terminal transcript and demo log. On macOS, if `ffmpeg` is installed and `IRONGATE_CAPTURE_SOURCE` is set, it also records an MP4 in the same directory while `./demo.sh` runs. On Linux and Windows, the checked-in script stays transcript-first unless you provide your own platform-specific ffmpeg/recorder command.

## Storage Guidance

- Keep the generated MP4 or GIF out of Git when it is large.
- If you need a shareable asset for a PR or portfolio page, upload the generated MP4 or derived GIF as a release asset or external file link and reference that URL in the PR description.
