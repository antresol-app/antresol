# Comic archive fixtures

Made once and committed; the tests never call `rar` or `7zz`.

Contents of both archives: `1.jpg`, `2.jpg`, `3.jpg` (8×8 baseline JPEG, solid
red, green and blue, 600 bytes each, written by Go `image/jpeg` at quality 90)
and `__MACOSX/._1.jpg` (a 24-byte AppleDouble stub). All mtimes are
2026-10-25 00:00 UTC.

Tools:
- RAR 7.23 for macOS arm64 (`rarmacos-arm-723.tar.gz` from rarlab.com,
  sha256 `68b393c000758d477fde43c955ff7542f12f76f3f5e87cdda923152fc791bd4d`;
  the Homebrew `rar` cask is disabled)
- 7-Zip (z) 26.05 arm64 (Homebrew `sevenzip`)

Commands, run in the folder holding the pages:

```
rar a -ma5 -idq rar5.cbr 1.jpg 2.jpg 3.jpg __MACOSX/._1.jpg
7zz a -bso0 three.cb7 1.jpg 2.jpg 3.jpg __MACOSX/._1.jpg
```

`three.cb7` is a solid LZMA2 archive (one stream for all four files).

sha256:
- `rar5.cbr`  `a25fca0b4634a22f7f9c9bd444f24052f44d9e1c921683cdc12dde21cdb50c86`
- `three.cb7` `67adb0f8f0a92bd4241d651ba63f20f300d1c427c77c4bea283b0cc0642b2624`
