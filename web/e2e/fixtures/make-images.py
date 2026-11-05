# Made the artwork in fixtures/images: python3 web/e2e/fixtures/make-images.py web/e2e/fixtures/images
# Replaces every image in DIR by a made-up one of the same name, size and
# format: gradients and simple shapes, the colours from a hash of the name.
import colorsys, hashlib, os, sys
from PIL import Image, ImageDraw

d = sys.argv[1]
for name in sorted(os.listdir(d)):
    p = os.path.join(d, name)
    src = Image.open(p)
    w, h = src.size
    hsh = hashlib.sha256(name.split('-')[0].encode()).digest()
    hue = hsh[0] / 255
    def rgb(hh, l, s):
        r, g, b = colorsys.hls_to_rgb(hh % 1, l, s)
        return int(r * 255), int(g * 255), int(b * 255)
    if name.endswith('-logo.png'):
        im = Image.new('RGBA', (w, h), (0, 0, 0, 0))
        dr = ImageDraw.Draw(im)
        m = h // 6
        dr.rounded_rectangle([m * 2, m, w - m * 2, h - m], radius=m, fill=rgb(hue, 0.92, 0.3) + (255,))
        n = 3 + hsh[1] % 4
        bw = (w - m * 6) // n
        for i in range(n):
            x = m * 3 + i * bw
            dr.rectangle([x + bw // 6, m * 2, x + bw - bw // 6, h - m * 2], fill=rgb(hue, 0.3, 0.5) + (255,))
        im.save(p, 'PNG')
        continue
    im = Image.new('RGB', (w, h))
    top, bottom = rgb(hue, 0.55, 0.45), rgb(hue + 0.15, 0.18, 0.5)
    vertical = h > w
    px = im.load()
    for y in range(h):
        for x in range(w):
            t = (y / max(h - 1, 1)) if vertical else (0.6 * x / max(w - 1, 1) + 0.4 * y / max(h - 1, 1))
            px[x, y] = tuple(int(a + (b - a) * t) for a, b in zip(top, bottom))
    dr = ImageDraw.Draw(im)
    r = min(w, h) // (3 + hsh[2] % 3)
    cx, cy = w * (0.3 + hsh[3] / 640), h * (0.3 + hsh[4] / 640)
    dr.ellipse([cx - r, cy - r, cx + r, cy + r], fill=rgb(hue + 0.5, 0.7, 0.5))
    dr.polygon([(0, h), (w * 0.5, h * 0.6), (w, h)], fill=rgb(hue + 0.3, 0.12, 0.4))
    im.save(p, 'JPEG', quality=80)
