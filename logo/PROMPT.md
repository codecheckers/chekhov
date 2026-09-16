# Profile image prompt — @chekhovbot avatar

Brand facts (from codecheckers.github.io): CODECHECK green **#008033**, white
**#FFFFFF**. The logo is a green rounded rectangle with heavy white "CODE
CHECK" lettering and a large hand-drawn-looking white tick to the right of it:
short down-left stroke, long tapering upstroke. The tick is the only part of
the logo that survives at avatar size — the wordmark must be dropped.

## Prompt A — the tick IS the robot (recommended)

> Flat vector logo mark for a software bot avatar, square format. A single
> bold white checkmark, thick tapering strokes, slightly hand-drawn character,
> centred on a solid deep green background (#008033). The checkmark doubles as
> a small robot: two simple white square eyes sit in the open space to the
> upper left of the tick, and a short straight antenna with a round dot rises
> from the top of the tick's long stroke. Absolutely flat, two colours only —
> white on green, no gradients, no shading, no outlines, no text, no letters.
> Thick even strokes, generous margin, geometric and friendly. Reads clearly
> when scaled down to 24 pixels. Style of a modern minimal open-source project
> icon.

## Prompt B — robot head with tick visor

> Flat vector avatar of a friendly minimal robot head, square format, solid
> deep green background (#008033). The robot head is white with rounded
> corners and a single small antenna; its visor is cut out in green and
> contains one bold white checkmark as the robot's face. Two colours only,
> white and green, no gradients, no outlines, no text. Chunky simple shapes,
> centred, generous padding, legible at 24 pixels. Modern open-source project
> icon style.

## Prompt C — tick as seagull (quiet Chekhov nod)

> Flat vector logo mark, square, solid deep green background (#008033). A bold
> white checkmark whose long upstroke sweeps like a bird's wing, so the mark
> reads simultaneously as a tick and as a gull in flight. One tiny white dot
> above it suggesting an antenna. Two colours only, no gradients, no text,
> thick confident strokes, generous margin, legible at 24 pixels.

(The Seagull is Chekhov's best-known play and the Moscow Art Theatre emblem —
a nod that only people who know will notice. Skip if that association is not
wanted.)

## Negative prompt

> text, letters, words, wordmark, gradients, drop shadow, 3d, bevel, glossy,
> photorealistic, metallic, humanoid robot body, cute mascot with limbs,
> circuit board patterns, gears, sparkles, busy background, thin hairlines,
> multiple colours, watermark, border frame

## Acceptance checklist

- [ ] Colours corrected in an editor to exactly `#008033` and `#FFFFFF`
      (generators will not hit the hex)
- [ ] Square, 400x400 or larger, and still readable at 24 px (GitHub timeline
      size) — squint test
- [ ] No text of any kind
- [ ] Sits recognisably beside the main logo without competing with it
- [ ] Final version redrawn as SVG for the docs/README, PNG for the GitHub
      avatar; keep the source in `codecheckers/chekhov`
- [ ] Licence it CC BY 4.0 to match `logo/` and `badges/`
