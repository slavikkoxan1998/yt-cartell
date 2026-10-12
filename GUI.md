# New UI — "Warm app" redesign (package `internal/gui`)

The old Gio UI in `internal/ui` is replaced by a new package **`internal/gui`**, built from scratch.
Until the switch, `internal/ui` keeps compiling and `main.go` keeps using it; the final step flips
`main.go` to `gui.Run` and deletes `internal/ui` + `cmd/shots` + `cmd/kitdemo`.

User complaints this redesign must fix (verbatim intent): "half of the screen is empty, elements are
badly structured, there is no smoothness". So: **use the full window**, **clear card structure**,
**smooth motion everywhere** (press, hover, route changes, sheets, progress, loading).

## Direction contract

- THESIS: A friendly studio that feels like a consumer app, not a pro tool: soft white cards on a warm
  ground, plain Ukrainian words, and exactly one bright colour — always something you can press. Refuses
  the category's dark neon dashboard.
- OWN-WORLD: warm tinted ground, white cards with 20–24 dp radii and soft shadows, warm hairline borders,
  a humanist sans (Fira Sans), outlined rounded icons (Lucide style, 1.75 stroke), one saturated action
  orange reserved for actionable elements; progress rings; quick-action tiles; "next step" cards with an
  arrow button; a dark warm variant with the same grammar.
- STORY: A newcomer pastes a story, picks a profile, and either presses Autopilot or walks the five steps
  guided by one "next step" card; an expert opens any card and edits every prompt.
- FIRST VIEWPORT (Home): left rail (logo, Головна, Проєкти, Черга, Налаштування, big round "+"), a greeting
  hero with a large story box + profile chips + "Автопілот" and "Покроково" buttons, a row of quick-action
  tiles, the projects grid with progress rings, and a right column with the live queue and a smart tip.
- FORM: challenger "warm consumer app surface" chosen by the user; seed key 189f35f8.
- FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict,
  and DESIGN.md.

## Tokens

Light (default) — ground `#FFF4E6`, surface (tinted card/inputs fill) `#F7EBDD`, card `#FFFFFF`,
border `#E7DACB`, border-strong `#D9C8B4`, text `#2B241F`, text-2 `#6F625A`, text-3 `#8E8177`
(check ≥4.5:1 on its surface; darken if needed), action `#FF6A3D`, action-hover `#F2582A`,
action-pressed `#E24E22`, action-soft (tint) `#FFE3D6`, focus ring `#FF6A3D` at 40% alpha.
Status (never used as decoration): success `#1F9D55` (soft `#DDF3E6`), running `#7A5AF8` (soft
`#ECE7FF`), warning/stale `#C98A00` (soft `#FCF1D2`), error `#D6334A` (soft `#FBE1E5`).
Dark ("Вечір") — ground `#1B1714`, surface `#26201B`, card `#2C2520`, border `#3D342D`,
text `#F6EEE6`, text-2 `#C2B4A6`, text-3 `#9C8E81`, action `#FF7A4D` (same roles), status colours
brightened for contrast. Theme: Світла / Темна / Як у системі (Windows app theme via registry
AppsUseLightTheme; fall back to light).
Radii: card 22, inner card/tile 16, control 12, pill 999. Elevation: card shadow y=2 blur=10 α=6%
+ y=12 blur=28 α=5%; hover lifts to y=6 blur=18 α=9%; sheets/dialogs y=24 blur=60 α=18%.
Spacing scale 4/8/12/16/20/24/32/40/56. Control height 44 (Md), 36 (Sm), 52 (Big). Body 15 sp,
small 13, label 14 medium, H3 18 semibold, H2 24 semibold, H1 32 semibold, Display 40 semibold
(Home greeting only). Numbers in stats use SemiBold.

## Layout (full window, no dead half)

- Left rail 92 dp (icon 24 + label 12 sp), active item = action-soft pill + action icon/label; the big
  round "+" (56 dp, action) opens "Новий проєкт" sheet. Rail collapses to a bottom bar under 900 dp.
- Content fills the rest with 32 dp page padding (24 under 1280, 16 under 900). No global max-width;
  instead **responsive column grids**: 12-column fluid grid, gutters 24. Pages lay out cards across the
  full width (e.g. 8+4, 6+6, 4+4+4) and switch at 1280/1600/2000 dp.
- Project workspace: header card (title, logline, progress ring with step x/5, key stats pills, profile
  chip, "Автопілот" button) → step tabs (pill tabs with numbers/checks) → body = main column (fluid) +
  right context column 360 dp at ≥1440 dp (next-step card, scene navigator mini-list, queue for this
  project, tips); under 1440 the context column moves below/into a drawer.
- Lists of scenes = responsive card grids (min card width 300 dp) — never a single narrow column on a
  wide screen.

## Motion (smoothness is a requirement)

Time-based animations, 60 fps, invalidate only while animating. Easing: `easeOutCubic` for enter,
`easeInOutCubic` for moves, spring (critically-damped, ~220 ms) for press/release. Durations: hover colour
120 ms; press scale 0.97 + shadow drop; route change: old view fades out 90 ms, new view fades+rises
8 dp in 180 ms; tabs: sliding pill indicator 200 ms; sheets/drawers slide 240 ms with scrim fade;
dialogs scale 0.96→1 + fade 180 ms; toasts slide in from bottom-right 220 ms, auto-hide; progress bars
and rings animate to new values 300 ms; skeleton shimmer 1.4 s loop while loading; list items appear
with a 12 ms stagger (max 12 items) on first load only (no bounce). Respect a "Менше анімацій" setting
(reduced motion → instant).

## Components (`internal/gui`, prefix-free exported names)

Theme, Fonts (Fira Sans Regular/Medium/SemiBold/Bold embedded), Icons (Lucide path data, ISC — embed
~70 icons as SVG path strings + a small SVG path parser → stroked `clip.Path`), Animator helpers.
Button (Primary / Secondary(outline) / Tinted(surface) / Ghost / Danger / Icon round; Sm/Md/Big;
leading/trailing icon; busy spinner; tooltip), Card (white, hoverable, clickable), Tile (quick action:
icon in soft square + label), Chip (toggle/filter, with icon/dot/remove), Pill tabs with sliding
indicator, Segmented, Toggle, Slider (with value bubble), TextField/TextArea (auto-grow, placeholder,
counter, focus ring, error text, password), SearchField, Select (popover list anchored to the field,
with search when >8 options, flips upward near the bottom), BigPicker (bottom-sheet/modal gallery for
styles/voices/models/languages with categories + search + previews), Dialog (confirm/forms), Drawer
(right side sheet), Toast, Tooltip, ProgressBar, ProgressRing (with % label), Skeleton, EmptyState
(illustrative icon + title + text + action), Badge, Avatar/Thumb (rounded image, placeholder), ImageView
(async, cover/contain, rounded, zoom on click → lightbox), VideoPlayer card (internal/player), AudioPill
(play/stop + time + mini waveform bar), StatusPill (asset/scene states: Готово / Генерується % /
Помилка / Змінено / Ще немає), StepTabs, NextStepCard, StatTile, ActivityRow (queue item), TipCard,
Tour overlay (spotlight cut-out + bubble card with Далі/Пропустити, steps anchored to widgets by key).

## Screens

1. **Головна** — greeting + big story box (paste text or import .txt) + profile chips (with "Керувати")
   + language chip + duration chip + buttons **Автопілот** (primary) / **Покроково** (secondary);
   quick actions: Нова історія, Автопілот, Перекласти проєкт, Своя озвучка, Обкладинка;
   projects grid (thumbnail, title, progress ring, state, duration, format, updated, menu: Відкрити,
   Перекласти, Дублювати, Показати в папці, Видалити); right column: live queue (all projects) + tip.
2. **Проєкт** (5 steps, full width):
   - Сценарій: story card (large editor, mode Ідея/Готовий сценарій/Своя озвучка, import .txt, word
     count, reading time) + settings cards grid: Тривалість (presets + custom), Формат, **Мова
     (Авто + 75 languages, searchable)**, Тон, Голос оповідача (picker with preview), Стиль (gallery
     picker with categories, 60+ styles, previews/palette tiles, "підходить для…"), Жанр/ніша, Камера,
     Темп, Модель кадрів, Режим відео (звичайний / **Розкадровка 3×3 (Omni Flash)** + **Ланцюжок кадрів**),
     Побажання режисеру; profile bar: apply profile / save as profile.
   - План: Загальне card, Акторський склад cards (image thumb if exists, name, role, @tag, look, voice),
     Локації cards, Сцени grid/list toggle (scene cards: number, title, speaker, location, cast chips,
     narration with speech meter, frame/motion prompts collapsible, rewrite with wish, move/insert/delete).
   - Розкадровка: references row (cast/locations) + scene frames grid; storyboard sheets 3×3 when enabled
     (show the sheet, click → lightbox with panels); regenerate/edit prompt/upload per card.
   - Відео й озвучка: scene cards with player/frame, voice pill, statuses, regenerate; summary stats.
   - Монтаж: **timeline editor** (horizontal track of scene clips with durations, drag to reorder, trim
     handles, per-scene transition picker, voice/music lanes), Звук (music, volumes, ducking), **Переходи
     (15+)**, **Ефекти руху (12)** for stills, **Фільтри (14)**, Субтитри (none/srt/burn + style), Обкладинка
     (**cover studio**: generate + text overlay editor with templates), final render card (player, open,
     reveal, save as, srt).
3. **Черга** (drawer from the rail + full page): running items across projects with %, done/errors,
   "Скасувати все", retry, open project.
4. **Налаштування** (cards): Підключення (Текст / G-Labs / Озвучка + голоси / Google / FFmpeg / STT for
   "Своя озвучка"), **Профілі** (list + create/edit/delete), **Автопілот**, **Сповіщення Telegram** (bot token,
   chat id, test, which events), Вигляд (тема, менше анімацій, показати підказки знову), Дані (папка,
   імпорт з веб-версії).
5. **Перекласти проєкт** dialog (target language → creates a copy with translated narration + new voices,
   reusing frames/videos), **Своя озвучка** flow (pick audio → transcribe → scenes timed to speech).
6. **Тур-підказки** on first run (Home → Story box → Profiles → Autopilot → Queue), replayable.

## Headless screenshots

`cmd/guishots -out <dir>` renders every screen/state of the new UI at 1920×1080, 1440×900 and 390×844
using `gioui.org/gpu/headless` (see how `internal/ui/kit_headless.go` does it), with a temp DataDir
seeded from `../data/studio` via Engine.ImportFromWeb when present. Never touch the user's real data
(`Documents/YT Cartell Studio`, `%AppData%/YT Cartell Studio`).
