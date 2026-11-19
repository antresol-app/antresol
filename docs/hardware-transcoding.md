# Hardware transcoding

When a client cannot play a file as it is, the server re-encodes the video
to H.264. A GPU does that several times faster than the CPU and leaves the
CPU free. The image has the drivers for Intel and AMD (VA-API, and Intel
Quick Sync on amd64) and uses an NVIDIA GPU through the NVIDIA Container
Toolkit; the container only needs to be given the GPU:

- **Intel or AMD.** Uncomment `devices` in `compose.yml`. The container
  starts as root only to add the group that owns `/dev/dri/renderD*` on this
  host to its `vault` user and then runs the server as `vault`, so the
  group's number (it differs per host) is not needed. If you run the
  container as another user (`user:` in `compose.yml`), uncomment
  `group_add` too and put the host's `render` group there:
  `getent group render | cut -d: -f3`.
- **NVIDIA.** Install the
  [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)
  on the host and uncomment `deploy` in `compose.yml`.

At start the server makes a short test encode on each GPU it sees and uses
the first that works (`VAULT_HWACCEL=auto`): NVIDIA NVENC, then Intel Quick
Sync, then VA-API, on macOS VideoToolbox; else the CPU. The log says what it
picked (`listening on :8420 (hw: vaapi)`), and so do the first-run wizard (it
says what to uncomment when no GPU was passed), Settings → Server → Statistics and
Settings → Server → Transcoding. There an administrator can pick the backend
(Auto, Intel Quick Sync, VA-API, NVIDIA, VideoToolbox, Off), the render node
when there are several, HDR tone mapping and HEVC output, and test the GPU
again; a change applies at once to new playback, what is playing goes on.
Until then the environment decides: `VAULT_HWACCEL` (`auto`, `qsv`,
`vaapi`, `nvenc`, `videotoolbox` or `none`) and `VAULT_HWACCEL_DEVICE` (the
render node for `vaapi`; default: the first, `/dev/dri/renderD128`). A
transcode the GPU refuses (a codec it does not decode, a driver error)
starts again on the CPU by itself.

- Intel and AMD decode, scale and encode on the GPU; AV1 and 10-bit H.264
  are decoded by the CPU. NVIDIA decodes and encodes on the GPU and scales on
  the CPU.
- **HDR tone mapping** (on by default): HDR video becomes SDR for a client
  that gets a transcode. HDR10 is tone-mapped on Intel and AMD GPUs
  (`tonemap_vaapi`; with Quick Sync the frames go through VA-API on the same
  GPU); HLG, HDR10 without mastering metadata, NVIDIA, macOS and the CPU
  path use ffmpeg's `zscale` and `tonemap` on the CPU. Off, HDR plays pale.
- **HEVC output** (on by default): a client that says it plays HEVC (the
  apps; a browser that decodes it) gets HEVC instead of H.264 from a GPU
  that passed its HEVC test encode; others, and the CPU, get H.264.
- Intel GPUs older than Broadwell (2014) use the `i965` driver, which the
  image has too.
- The binary on the host uses the host's ffmpeg and drivers in the same way.

## When the GPU fails its test

Settings → Server → Transcoding says why under “Now” (and “About This
Server” says it in one line), with the fix and ffmpeg's own words to copy;
the log has the same line (`media: the GPU failed its test encode: ...`). By
reason:

- **No access to `/dev/dri/renderD128`** (add group N to `group_add`). The
  server's user is not in the group that owns the render node. The image
  joins it by itself when it starts as root; it cannot when the container
  runs with `user:`. Add the number the page shows (the node's group on this
  host) under `group_add`, or check it on the host with
  `ls -ln /dev/dri/renderD128` (the fourth column), then
  `docker compose up -d` and “Test Again”.
- **The container cannot see `/dev/dri/...`.** Uncomment `devices` in
  `compose.yml`. On the host, `ls /dev/dri` must list a `renderD*` node; if
  it does not, the host's kernel has no driver for the GPU (or, when Docker itself runs inside a
  virtual machine or a system container, the node is not passed into it).
- **The GPU driver did not load.** The node opens but no VA-API driver takes
  the GPU. The image has, on amd64, Intel's `iHD` (Broadwell, 2014, and
  newer) and `i965` (older), and on every architecture AMD's `radeonsi`
  (mesa). libva picks the driver by itself; to force one, put
  `LIBVA_DRIVER_NAME=iHD` (or `i965`, `radeonsi`) in `.env`. A GPU newer than
  the image's driver needs a newer image. Intel on arm64 has no driver in the
  image.
- **No NVIDIA driver in the container.** ffmpeg could not load `libcuda` or
  `libnvidia-encode`. Install the NVIDIA Container Toolkit on the host,
  uncomment `deploy` in `compose.yml`, and check with
  `docker run --rm --gpus all ubuntu nvidia-smi`. The image sets
  `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility`; if you override it,
  keep `video` (without it the encoder library is not mounted).
- **The driver refused the encoder** (`h264_vaapi`, `h264_qsv`,
  `h264_nvenc`). The driver works but the GPU cannot encode H.264 that way
  (some older AMD GPUs encode nothing; an old NVIDIA driver is older than
  ffmpeg's NVENC API). The CPU transcodes; pick another backend under
  “Acceleration” if the GPU has one (Intel: VA-API when Quick Sync is
  refused).
- **The test did not finish in 20 seconds.** The GPU is busy or its driver
  hung; “Test Again”.
