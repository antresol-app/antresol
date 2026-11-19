# Hardware transcoding

When a client cannot play a file as it is, the server re-encodes the video
to H.264. A GPU does that several times faster than the CPU and leaves the
CPU free. The image has the drivers for Intel and AMD (VA-API, and Intel
Quick Sync on amd64) and uses an NVIDIA GPU through the NVIDIA Container
Toolkit; the container only needs to be given the GPU:

- **Intel or AMD.** Uncomment `devices` and `group_add` in `compose.yml`.
  `group_add` takes the number of the host's `render` group, which owns
  `/dev/dri/renderD*`: `getent group render | cut -d: -f3`.
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
