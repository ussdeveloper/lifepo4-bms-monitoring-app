"""Generate the application icon (Windows .ico) matching the UI logo mark.

The mark is a rounded square with a teal -> purple gradient and a white
rounded square inset, exactly like `.brand-mark` in the web UI.
"""

from __future__ import annotations

import os

from PIL import Image, ImageDraw

SIZES = (16, 24, 32, 48, 64, 128, 256)
SUPERSAMPLE = 4
OUT_PATH = os.path.join(os.path.dirname(__file__), "icon.ico")


def lerp(start: float, end: float, factor: float) -> float:
    return start + (end - start) * factor


def draw_mark(size: int) -> Image.Image:
    big = size * SUPERSAMPLE
    teal = (0, 194, 194)
    purple = (138, 111, 230)

    # Gradient fill painted pixel by pixel, then masked by a rounded rectangle.
    gradient = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    pixels = gradient.load()
    for y in range(big):
        for x in range(big):
            t = (x / max(1, big - 1) + y / max(1, big - 1)) / 2
            pixels[x, y] = (
                int(lerp(teal[0], purple[0], t)),
                int(lerp(teal[1], purple[1], t)),
                int(lerp(teal[2], purple[2], t)),
                255,
            )

    mask = Image.new("L", (big, big), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        (0, 0, big - 1, big - 1), radius=int(big * 0.24), fill=255
    )
    image = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    image = Image.composite(gradient, image, mask)

    # White inset mark.
    inset = int(big * 0.29)
    white = Image.new("RGBA", (big, big), (0, 0, 0, 0))
    ImageDraw.Draw(white).rounded_rectangle(
        (inset, inset, big - inset - 1, big - inset - 1),
        radius=int(big * 0.09),
        fill=(255, 255, 255, 235),
    )
    image = Image.alpha_composite(image, white)
    return image.resize((size, size), Image.LANCZOS)


def main() -> None:
    frames = [draw_mark(size) for size in SIZES]
    frames[-1].save(OUT_PATH, format="ICO", sizes=[(s, s) for s in SIZES])
    print(f"icon written: {OUT_PATH} ({os.path.getsize(OUT_PATH)} bytes)")


if __name__ == "__main__":
    main()
