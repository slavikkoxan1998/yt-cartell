---
name: "YT Cartell Studio (desktop)"
description: "A friendly Windows studio where a story becomes a video: soft white cards on a warm ground, plain Ukrainian words, and one bright orange for whatever you press next."
colors:
  # Light theme «Світла» (the default). The dark theme «Вечір» mirrors every role in the -dark keys below.
  # Alpha colours are hex8 (#RRGGBBAA). Source of truth: internal/gui/theme.go (LightPalette, DarkPalette).
  ground: "#FFF4E6"
  surface: "#F7EBDD"
  surface-hover: "#F0E1D0"
  card: "#FFFFFF"
  border: "#E7DACB"
  border-strong: "#D9C8B4"
  text: "#2B241F"
  text-2: "#6F625A"
  text-3: "#736960"
  action: "#FF6A3D"
  action-hover: "#FF7B52"
  action-pressed: "#F9602F"
  action-soft: "#FFE3D6"
  action-soft-hi: "#FFD5C2"
  action-text: "#AF492A"
  on-action: "#2B241F"
  focus: "#FF6A3D66"
  success: "#1F9D55"
  success-soft: "#DDF3E6"
  success-ink: "#187B43"
  running: "#7A5AF8"
  running-soft: "#ECE7FF"
  running-ink: "#6D50DE"
  warning: "#C98A00"
  warning-soft: "#FCF1D2"
  warning-ink: "#906300"
  error: "#D6334A"
  error-soft: "#FBE1E5"
  error-ink: "#C22E43"
  error-fill: "#CF2F46"
  scrim: "#2B241F6B"
  shadow: "#4A2E1C"
  tooltip-bg: "#2B241F"
  tooltip-fg: "#FFF8F1"
  selection: "#FF9A7066"
  shimmer: "#FFFCF8"
  track: "#F1E4D5"
  overlay: "#FFFFFFE6"
  overlay-ink: "#2B241F"
  ground-dark: "#1B1714"
  surface-dark: "#26201B"
  surface-hover-dark: "#322A24"
  card-dark: "#2C2520"
  border-dark: "#3D342D"
  border-strong-dark: "#52463D"
  text-dark: "#F6EEE6"
  text-2-dark: "#C2B4A6"
  text-3-dark: "#9E9083"
  action-dark: "#FF7A4D"
  action-hover-dark: "#FF8B62"
  action-pressed-dark: "#F0683A"
  action-soft-dark: "#4A2C20"
  action-soft-hi-dark: "#5A3527"
  action-text-dark: "#FF8A61"
  on-action-dark: "#1B1714"
  focus-dark: "#FF7A4D73"
  success-dark: "#4CC784"
  success-soft-dark: "#1E3A2A"
  success-ink-dark: "#5FD394"
  running-dark: "#A08CFF"
  running-soft-dark: "#2E2750"
  running-ink-dark: "#B3A3FF"
  warning-dark: "#E8B03A"
  warning-soft-dark: "#3D3118"
  warning-ink-dark: "#F0BF55"
  error-dark: "#FF6B7F"
  error-soft-dark: "#45232A"
  error-ink-dark: "#FF8292"
  error-fill-dark: "#C7364B"
  scrim-dark: "#000000A6"
  shadow-dark: "#000000"
  tooltip-bg-dark: "#F6EEE6"
  tooltip-fg-dark: "#1B1714"
  selection-dark: "#FF7A4D66"
  shimmer-dark: "#3E342C"
  track-dark: "#3A3029"
  overlay-dark: "#2C2520D9"
  overlay-ink-dark: "#F6EEE6"
typography:
  # Sizes are sp, written as px at 100 % Windows scaling. Source: internal/gui/text.go (DefaultTypography).
  display:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "40px"
    fontWeight: 600
    lineHeight: 1.12
  h1:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "32px"
    fontWeight: 600
    lineHeight: 1.15
  h2:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: 1.2
  h3:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "18px"
    fontWeight: 600
    lineHeight: 1.3
  body:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.5
  body-strong:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: 1.5
  label:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "14px"
    fontWeight: 500
    lineHeight: 1.3
  small:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.45
  caption:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.4
  stat:
    fontFamily: "'Fira Sans', 'Segoe UI', sans-serif"
    fontSize: "28px"
    fontWeight: 600
    lineHeight: 1.1
rounded:
  # dp written as px. Source: internal/gui/theme.go (Radius*).
  small: "8px"
  control: "12px"
  inner: "16px"
  card: "22px"
  pill: "999px"
spacing:
  # dp written as px. Source: internal/gui/theme.go (Sp*).
  sp4: "4px"
  sp8: "8px"
  sp12: "12px"
  sp16: "16px"
  sp20: "20px"
  sp24: "24px"
  sp32: "32px"
  sp40: "40px"
  sp56: "56px"
components:
  # Colour refs name the light tokens; in «Вечір» each resolves to its -dark twin.
  button-primary:
    backgroundColor: "{colors.action}"
    textColor: "{colors.on-action}"
    rounded: "{rounded.control}"
    padding: "0 20px"
    height: "44px"
  button-primary-hover:
    backgroundColor: "{colors.action-hover}"
    textColor: "{colors.on-action}"
  button-primary-pressed:
    backgroundColor: "{colors.action-pressed}"
    textColor: "{colors.on-action}"
  button-secondary:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "0 20px"
    height: "44px"
  button-secondary-hover:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
  button-tinted:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "0 20px"
    height: "44px"
  button-tinted-hover:
    backgroundColor: "{colors.surface-hover}"
    textColor: "{colors.text}"
  button-ghost:
    textColor: "{colors.text-2}"
    rounded: "{rounded.control}"
    padding: "0 20px"
    height: "44px"
  button-ghost-hover:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
  button-danger:
    backgroundColor: "{colors.error-fill}"
    textColor: "#FFFFFF"
    rounded: "{rounded.control}"
    padding: "0 20px"
    height: "44px"
  button-sm:
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0 14px"
    height: "36px"
  button-big:
    rounded: "{rounded.control}"
    padding: "0 26px"
    height: "52px"
  button-new-project:
    backgroundColor: "{colors.action}"
    textColor: "{colors.on-action}"
    rounded: "{rounded.pill}"
    size: "56px"
  icon-button:
    textColor: "{colors.text-2}"
    rounded: "{rounded.pill}"
    size: "32px"
  icon-button-hover:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
  link:
    textColor: "{colors.action-text}"
  card:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    rounded: "{rounded.card}"
    padding: "24px"
  card-well:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.inner}"
    padding: "16px"
  picker-tile:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    rounded: "{rounded.inner}"
    padding: "16px"
  next-step-arrow:
    backgroundColor: "{colors.action}"
    textColor: "{colors.on-action}"
    rounded: "{rounded.control}"
    size: "44px"
  field:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "11px 14px"
    height: "44px"
  field-flat:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "16px"
  search-field:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    typography: "{typography.body}"
    rounded: "{rounded.pill}"
    padding: "0 14px"
    height: "44px"
  chip:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text-2}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    padding: "0 14px"
    height: "36px"
  chip-hover:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
  chip-selected:
    backgroundColor: "{colors.action-soft}"
    textColor: "{colors.text}"
  status-running:
    backgroundColor: "{colors.running-soft}"
    textColor: "{colors.running-ink}"
    rounded: "{rounded.pill}"
    padding: "0 12px 0 10px"
    height: "28px"
  status-done:
    backgroundColor: "{colors.success-soft}"
    textColor: "{colors.success-ink}"
    rounded: "{rounded.pill}"
    padding: "0 12px 0 10px"
    height: "28px"
  status-stale:
    backgroundColor: "{colors.warning-soft}"
    textColor: "{colors.warning-ink}"
    rounded: "{rounded.pill}"
    padding: "0 12px 0 10px"
    height: "28px"
  status-error:
    backgroundColor: "{colors.error-soft}"
    textColor: "{colors.error-ink}"
    rounded: "{rounded.pill}"
    padding: "0 12px 0 10px"
    height: "28px"
  status-empty:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text-2}"
    rounded: "{rounded.pill}"
    padding: "0 12px 0 10px"
    height: "28px"
  badge-count:
    backgroundColor: "{colors.action}"
    textColor: "{colors.on-action}"
    rounded: "{rounded.pill}"
    padding: "0 8px"
    height: "22px"
  nav-rail:
    backgroundColor: "{colors.card}"
    width: "92px"
  nav-item:
    textColor: "{colors.text-2}"
    typography: "{typography.caption}"
    rounded: "{rounded.pill}"
    width: "56px"
    height: "32px"
  nav-item-active:
    backgroundColor: "{colors.action-soft}"
    textColor: "{colors.action-text}"
    rounded: "{rounded.pill}"
    width: "56px"
    height: "32px"
  pill-tabs:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.pill}"
    padding: "4px"
    height: "44px"
  pill-tab-active:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    padding: "0 16px"
  segmented:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.control}"
    padding: "3px"
    height: "40px"
  toggle-off:
    backgroundColor: "{colors.border-strong}"
    rounded: "{rounded.pill}"
    width: "44px"
    height: "26px"
  toggle-on:
    backgroundColor: "{colors.action}"
    rounded: "{rounded.pill}"
    width: "44px"
    height: "26px"
  tooltip:
    backgroundColor: "{colors.tooltip-bg}"
    textColor: "{colors.tooltip-fg}"
    typography: "{typography.caption}"
    rounded: "{rounded.small}"
    padding: "6px 10px"
  toast:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    typography: "{typography.body}"
    rounded: "{rounded.inner}"
    padding: "16px"
    width: "380px"
  dialog:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    rounded: "{rounded.card}"
    padding: "28px"
    width: "480px"
  drawer:
    backgroundColor: "{colors.card}"
    textColor: "{colors.text}"
    rounded: "{rounded.card}"
    padding: "24px"
    width: "440px"
---

# Design System: YT Cartell Studio (desktop)

## Overview

**Creative North Star: "The Warm Board"**

The studio lays its work out like cards pinned to a warm paper board. The whole window is a tinted ground (Warm Paper by day, the brown «Вечір» ground at night), and everything the user reads or edits sits on it as a soft white card with 22 dp corners, a warm hairline and a two-layer brown-tinted shadow. It is a consumer app for non-technical creators, not a pro tool: plain sentence-case Ukrainian, one humanist sans (Fira Sans), outlined Lucide icons, and exactly one bright colour, Studio Orange, which marks the thing you can press next. It refuses the category's dark neon dashboard; the dark theme is the same board at evening light, with the same grammar.

The board uses the whole window. There is no page max-width: cards spread across responsive column grids (8 + 4, auto-fill cards of at least 300 dp, a 360 dp context column), and the navigation rail turns into a bottom bar on narrow windows. Structure comes from cards on the ground; inside a card, grouping comes from tinted wells and flat inset fields, never from another card. Every stage of the pipeline shows as a state (ready, generating, changed, failed, not yet) with its own colour family, which keeps the orange free for the next action.

Motion is part of the material. Everything eases on a clock and asks for frames only while it moves: hover in 120 ms, press as a critically damped spring to 0.97 with no bounce, pages fading out in 90 ms and rising 8 dp into place in 180 ms, sheets sliding in 240 ms, dialogs growing from 0.96 in 180 ms, progress settling in 300 ms, lists staggering 12 ms apart. The theme switch crossfades every colour in 250 ms, and «Менше анімацій» makes all of it instant.

**Key Characteristics:**
- Warm tinted ground with soft white cards: 22 dp corners, a warm hairline, a two-layer warm shadow.
- One bright colour, Studio Orange, reserved for what you can press, always with dark ink on it.
- Four status families (running violet, done green, changed amber, failed red), each in strong, soft and ink steps.
- Fira Sans only: SemiBold headings, Medium labels, Regular body, and no kickers.
- Full-window card grids, a 92 dp rail on the left, a bottom bar under 900 dp.
- Cards never nest: wells and flat inset controls do the grouping inside a card.
- Clock-driven motion with a reduced-motion switch; light, dark and system themes.

## Colors

A warm paper-and-oat neutral family carries one saturated orange for action and four semantic status families; the dark «Вечір» palette mirrors every role step for step (the `-dark` tokens).

### Primary
- **Studio Orange** (#FF6A3D; «Вечір» #FF7A4D): the fill of what you press next: primary buttons, the round "+" for a new project, the logo button, the next-step arrow, a toggle or slider that is on, the current step number in the step tabs, the selected clip, grabbed trim handle and playhead on the timeline, the played part of a seek bar or a playing voice waveform, and the arc of step progress. Never used for text: on white it reads only 2.85:1.
- **Orange Hover** (#FF7B52) and **Orange Pressed** (#F9602F): hover brightens a touch; press deepens only as far as the dark ink still reads 4.90:1 («Вечір» #FF8B62 and #F0683A).
- **Ink on Orange** (#2B241F; «Вечір» #1B1714): every label and icon on an orange fill, at 5.37:1 (6.91:1 in «Вечір»).
- **Orange Wash** (#FFE3D6; «Вечір» #4A2C20), deepening to **Orange Wash Hover** (#FFD5C2) under the pointer: the active rail pill, selected chips, the circles behind next-step and empty-state icons, and the track under step progress.
- **Ember Ink** (#AF492A; «Вечір» #FF8A61): orange-family text and icons: links, the active rail label, icons on the wash (5.4:1 on Card White, 4.5:1 on the wash).
- **Focus Glow** (#FF6A3D66, the orange at 40 %; «Вечір» at 45 %): focus rings and the field focus glow.

### Neutral
- **Warm Paper** (#FFF4E6; «Вечір» #1B1714): the window ground and nothing else.
- **Card White** (#FFFFFF; «Вечір» #2C2520): cards, sheets, dialogs, drawers, fields and the rail.
- **Oat** (#F7EBDD; «Вечір» #26201B): tinted fills: wells inside cards, Tinted buttons, pill-tab and segmented tracks, search fields, flat inset controls, card-head icon boxes, skeletons. **Oat Hover** (#F0E1D0) is its hovered step.
- **Warm Hairline** (#E7DACB; «Вечір» #3D342D): card borders (at 75 %), dividers and resting field borders. **Strong Hairline** (#D9C8B4) takes hovered borders and the off track of a toggle.
- **Espresso** (#2B241F; «Вечір» #F6EEE6): primary text and icons.
- **Walnut** (#6F625A; «Вечір» #C2B4A6): secondary text: descriptions, meta lines, resting chip, tab and rail labels.
- **Driftwood** (#736960; «Вечір» #9E9083): tertiary text: placeholders, hints, counters, captions (5.36:1 on Card White, 4.56:1 on Oat).
- **Track** (#F1E4D5; «Вечір» #3A3029): progress and slider rails.
- **Scrim** (#2B241F6B, Espresso at 42 %; «Вечір» black at 65 %): behind dialogs, drawers and the tour.
- **Tooltip** (#2B241F with #FFF8F1 text, inverted in «Вечір»): tooltips and the slider value bubble.
- **Shadow Tint** (#4A2E1C; «Вечір» black): the colour every shadow is built from.
- Utility tints: **Selection** (#FF9A7066) for selected text in fields, **Shimmer** (#FFFCF8) for the skeleton band, **Overlay** (#FFFFFFE6 with Espresso icons) for play buttons and time bars over images.

### Status
- **Running Violet** (#7A5AF8, soft #ECE7FF, ink #6D50DE): generating: spinners in status pills, queue bars, live rings.
- **Done Green** (#1F9D55, soft #DDF3E6, ink #187B43): ready: checks, finished rings, done step numbers, "Готово" pills.
- **Changed Amber** (#C98A00, soft #FCF1D2, ink #906300): stale since it was generated ("Змінено — перегенеруй") and counters at their limit.
- **Failed Red** (#D6334A, soft #FBE1E5, ink #C22E43): failed items and field errors. **Danger Red** (#CF2F46; «Вечір» #C7364B) fills destructive buttons and error badges under white text (5.06:1; 5.18:1 in «Вечір»).
- Empty and queued states use Oat with Walnut text. In «Вечір» every status is brightened so its ink still reads 4.5:1 on the dark soft fill.

### Named Rules
**The Pressable Orange Rule.** Studio Orange fills only what can be pressed, so one glance finds the next step. The world has exactly two devices outside that: the arc of step progress (x/5), which points at the next step, and the short dash under the Home greeting. Status never borrows the orange.

**The Dark Ink Rule.** Labels and icons on an orange fill are Ink on Orange, never white; white measured 2.85:1 on the orange. Hover brightens and press deepens only within the range where the ink stays at or above 4.5:1.

**The Three-Step Status Rule.** Every status family comes as strong (dots, bars, rings), soft (pill and circle fills) and ink (text and icons on its soft fill, at least 4.5:1). Status colour always names a state and is never decoration.

## Typography

**Display Font:** Fira Sans (embedded; Segoe UI only fills glyphs Fira lacks)
**Body Font:** Fira Sans
**Label/Mono Font:** Fira Sans (one family; no monospace in the interface)

**Character:** A humanist sans with full Cyrillic that reads friendly rather than technical. Headings are SemiBold, labels Medium and running text Regular, so hierarchy comes from size and weight steps inside one family.

### Hierarchy
- **Display** (600, 40 sp, 1.12): the Home greeting only, with the short orange dash (56 × 6 dp, round ends) 12 dp under it; on phones the greeting drops to H2.
- **H1** (600, 32 sp, 1.15): page titles: Проєкти, Черга, Налаштування.
- **H2** (600, 24 sp, 1.2): section heads on the ground, the project title and stat-tile numbers; dialog titles and project section heads set it at 22 sp.
- **H3** (600, 18 sp, 1.3): card titles, next-step titles, empty-state titles; drawer titles set it at 20 sp.
- **Body** (400, 15 sp, 1.5): paragraphs, field text, descriptions. In Medium it labels Md buttons (line height 1.3) and titles activity rows.
- **Body Strong** (600, 15 sp, 1.5): inline emphasis; picker values set it at 16 sp.
- **Label** (500, 14 sp, 1.3): Sm buttons, field labels, chips and tabs; links set it at 15 sp.
- **Small** (400, 13 sp, 1.45): meta lines, hints and captions; card-head descriptions and next-step lines set it at 14 sp, status pills in Medium.
- **Caption** (400, 12 sp, 1.4): counters, timestamps, rail labels (Medium, SemiBold when active), badges (SemiBold), tooltips (Medium).
- **Stat** (600, 28 sp, 1.1): the numbers inside progress rings, scaled to 27 % of the ring's diameter.

Dark text under 20 sp is painted twice to restore its intended weight under Gio's linear-light blending, so text is always drawn through the kit's text styles rather than raw labels.

### Named Rules
**The No-Kicker Rule.** There is no overline or eyebrow style. A label above content is sentence case in Label or Small, never uppercase and never letter-spaced, and headings stand on their own. The world card's 11 px tracked overline was dropped on purpose.

**The One Family Rule.** Fira Sans is the only face and system fonts stay off. Hierarchy comes from the ten steps above, never from a second typeface.

## Layout

The window opens at 1440 × 900 dp (maximized when the screen's work area is smaller) and shrinks to 480 × 560 dp. Measurements are dp, written as px in the tokens (1 dp = 1 px at 100 % Windows scaling).

- **Rail:** 92 dp on the left in Card White with a hairline edge: the 48 dp orange logo button, four destinations (Головна, Проєкти, Черга, Налаштування), then the community link and the theme switch at the foot, and the 56 dp round "+" at the bottom. Under a 900 dp window it becomes a bottom bar with the "+" raised 16 dp in a centre slot.
- **Page padding:** 32 dp from a 1280 dp window, 24 dp from 900, 16 dp below that; 40 dp of air under the last row.
- **Grids, not max-widths:** Home splits 8 + 4 once its content is 1100 dp wide (story card and projects on the left; next step, queue, tip and quick actions on the right) and pairs cards below that. Card lists are auto-fill grids of cards at least 300 dp wide. Settings goes to two columns from a 1280 dp window. Montage option grids take 3 columns from 1600 dp and 2 from 1000. The project workspace adds a 360 dp context column (next step, plan check, scene list) once its content reaches 1340 dp and moves it into an «Огляд» drawer below that.
- **Rhythm:** 24 dp between cards and rows (the gutter), 20 dp between blocks inside a card, 8–12 dp inside a group, all on the 4 / 8 / 12 / 16 / 20 / 24 / 32 / 40 / 56 scale.

### Named Rules
**The Full-Window Rule.** Never center a narrow column. Extra width becomes more or wider cards in the grid, never wider margins.

## Elevation & Depth

Depth is a hybrid. Cards on the ground and overlays above the window carry soft shadows; everything inside a card is flat and separated by tone. Every shadow is two Gaussian layers (the CSS box-shadow equivalent) tinted with Shadow Tint on the light ground; on the dark ground the same layers use black at 2.6 × the alpha (capped at 60 %) so they still read.

### Shadow Vocabulary
- **Control** (`box-shadow: 0 1px 3px rgba(74,46,28,0.10), 0 2px 8px rgba(74,46,28,0.06)`): toggle and slider knobs, the sliding tab indicator, the segmented thumb, the logo at rest.
- **Card** (`box-shadow: 0 2px 10px rgba(74,46,28,0.06), 0 12px 28px rgba(74,46,28,0.05)`): every card resting on the ground.
- **Hover** (`box-shadow: 0 3px 10px rgba(74,46,28,0.06), 0 16px 32px rgba(74,46,28,0.09)`): a pressable card under the pointer, lifted 2 dp.
- **Pop** (`box-shadow: 0 2px 6px rgba(74,46,28,0.06), 0 14px 36px rgba(74,46,28,0.14)`): select popovers, toasts, tooltips.
- **Sheet** (`box-shadow: 0 2px 8px rgba(74,46,28,0.06), 0 24px 60px rgba(74,46,28,0.18)`): dialogs, drawers, the big picker, the tour bubble.

### Named Rules
**The Cards-Never-Nest Rule.** Shadows live only between the ground and what sits on it, and between the window and its overlays. Inside a card or sheet, a group is a tinted well (Oat, 16 dp corners, no border, no shadow) and a field is a flat inset control (Oat, 12 dp corners, hairline, no shadow). A card never sits inside a card.

**The Lift Rule.** A pressable card answers hover by rising 2 dp into the Hover shadow over 120 ms and answers a press by scaling to 0.985 on a spring; its fill never changes.

## Shapes

Soft, friendly geometry: one radius ladder, hairline borders, round ends everywhere.

- **Corner ladder:** Card 22 dp (cards, sheets, dialogs, drawers) → Inner 16 dp (wells, picker tiles, thumbnails, popovers, toasts, row hovers) → Control 12 dp (buttons, fields, selects, flat inset controls, segmented tracks, card-head icon boxes, the next-step arrow) → Small 8 dp (tooltips, the slider bubble, skeleton blocks) → Pill (chips, status pills, badges, pill tabs, search fields, toggles, round buttons).
- **Borders:** 1 dp warm hairlines; card borders at 75 %, sheet and dialog borders at 60 %. Secondary buttons carry a 1.25 dp Espresso outline at 85 %. A picked card shows a 2 dp orange outline and an open inset field a 1.5 dp one.
- **Focus:** a 2 dp Focus Glow ring 2 dp outside the shape, fading in over 120 ms; fields instead ease their border to orange and grow a 3 dp glow.
- **Circles:** icon circles (44 dp in activity rows, 52 dp in dialogs, 64 dp on next-step cards, 96 dp in empty states), avatars, progress rings with round caps, round icon buttons.
- **Icons:** Lucide outlines drawn as round-capped, round-joined strokes, 1.75 dp at 24 dp and never thinner than 1.5 dp; an icon takes the colour of the text beside it.

### Named Rules
**The Step-Down Corner Rule.** Each level inward drops one step of the ladder: card 22 → well, tile or thumbnail 16 → control 12 → tooltip 8. Pills stay pills at any level.

## Components

### Buttons
Friendly and certain: one orange button per view, everything else quieter.
- **Shape:** gently rounded (12 dp); `Round` makes a pill, or a circle when the button is icon-only.
- **Primary:** Studio Orange with Ink on Orange, Fira Sans Medium 15, 44 dp tall with 20 dp sides, an optional 18 dp leading icon and trailing arrow.
- **Hover / Focus:** the fill eases to Orange Hover in 120 ms; a press eases toward Orange Pressed and scales the button to 0.97 on a critically damped spring (about 220 ms, no bounce); keyboard focus shows the 2 dp ring. Busy swaps the icon for a spinner and ignores clicks; disabled drops to 45 % opacity.
- **Secondary / Tinted / Ghost / Danger:** Secondary is Card White with a 1.25 dp Espresso outline at 85 % and fills with Oat on hover. Tinted is Oat, Oat Hover on hover (Cancel in dialogs, ⋯ menus). Ghost is Walnut text until hovered, then an Oat fill. Danger is Danger Red with white text and only confirms deletes.
- **Sizes:** Sm 36 dp (Label, 14 dp sides, 16 dp icon), Md 44 dp, Big 52 dp (16 sp, 26 dp sides, 20 dp icon). The new-project "+" is a 56 dp orange circle.
- **Icon-only:** square or round, always with a tooltip; inline actions (close, remove, more) use a 32 dp round ghost icon button.
- **Links:** Ember Ink at 15 sp with a trailing chevron that slides 2 dp and a hairline underline that appears on hover.

### Chips
- **Style:** 36 dp pills (30 dp small) in Card White with a warm hairline and Label text in Walnut, turning Espresso on hover; an optional 16 dp icon or 8 dp status dot, and an inline × to remove.
- **State:** a selected chip takes the Orange Wash with a check in Ember Ink and Espresso text; hovering it draws a soft orange outline. Press scales a chip to 0.96.
- **Status pills:** 28 dp soft-fill pills (24 dp small) with ink text in Small Medium and a 14 dp icon: Готово (check), Генерується N % (spinner), Змінено — перегенеруй (refresh), Помилка (alert), У черзі (clock), Ще немає (dashed ring).
- **Badges:** 22 dp pills in Caption SemiBold for counts. Solid orange badges carry Ink on Orange, error counts sit on Danger Red with white text, and a badge without text is an 8 dp dot.

### Cards / Containers
- **Corner Style:** 22 dp; picker tiles and wells 16 dp.
- **Background:** Card White on the ground; Oat for wells inside a card.
- **Shadow Strategy:** Card at rest and Hover when pressable and under the pointer (see Elevation & Depth); wells and inset controls stay flat.
- **Border:** the warm hairline at 75 %; a picked card adds a 2 dp orange outline.
- **Internal Padding:** 24 dp (20 dp on narrow windows and for the project header and next-step cards, 12 dp for the compact header), with 20 dp between blocks.
- **Card head:** a 40 dp Oat icon box (12 dp corners, 20 dp Espresso icon), an H3 title and a 14 sp Walnut description; the big head is 48 dp and 24 dp with an H2 title at 22 sp.
- **Picker tiles:** 16 dp shadowed cards in grids on the ground: a 44 dp visual, a Small Walnut label, the value in Body Strong 16, a Small Driftwood caption, and a chevron that turns over while the chooser is open. Inside a card or sheet the same field becomes a flat inset control.

### Inputs / Fields
- **Style:** Card White, a 1 dp warm hairline (Strong Hairline on hover), 12 dp corners, at least 44 dp tall with 11 × 14 dp padding, Body text and a Driftwood placeholder. The label sits 8 dp above in Label, the hint 6 dp below in Small Driftwood, a counter at the right in Caption.
- **Focus:** the border eases to orange and thickens by 0.5 dp while a 3 dp Focus Glow grows in over 180 ms.
- **Error / Disabled:** an error turns border and glow Failed Red and replaces the hint with an alert icon and a Failed Red ink message; a disabled field sits on Oat with Walnut text.
- **Search:** a pill on Oat with a leading search icon that turns Card White on focus; a clear button appears with text, and Esc clears it.
- **Select:** a field that opens a 16 dp popover with the Pop shadow, anchored to it and flipping upward near the window's bottom; a search box appears above eight options; ↑ ↓ Enter Esc work; the picked row shows an orange check.
- **Toggle / Slider / Segmented:** a toggle is a 44 × 26 dp track (Strong Hairline off, Studio Orange on) with a white 20 dp knob that squashes while pressed. A slider runs a 6 dp Track with an orange fill and a white knob with an orange centre, and shows a value bubble while dragging. A segmented control is a 40 dp Oat track with a sliding white thumb.

### Navigation
- **Rail items:** a 56 × 32 dp pill holding a 22 dp icon over a Caption label. Hover fades in an Oat pill; the active item sits on the Orange Wash with an orange icon and an Ember Ink SemiBold label. Running work shows as a solid orange count badge on the icon; failures with nothing running show a red dot.
- **Step and section tabs:** a 44 dp Oat pill track with a white indicator (Control shadow) that slides in 200 ms; labels in Label, SemiBold when active, where an active tab's icon turns orange. Step numbers sit in 22 dp circles: done is Done Green with a check, the current step is orange with dark ink, the rest are Oat Hover with Walnut numbers.
- **Mobile:** under 900 dp the rail becomes a Card White bottom bar with the four destinations and the raised orange "+" in the middle, and the Home header moves the theme and community buttons into round Tinted buttons.

### Next-Step Card
The signature of the board: the whole card is a button that says what to do next. A 64 dp Orange Wash circle holds a 28 dp Espresso icon (on Home, a step ring instead), followed by an H3 title and a 14 sp Walnut line; on the right a 44 dp orange square carries a dark arrow that slides 3 dp on hover and becomes a spinner while that step runs.

### Progress and Activity
- **Progress rings and bars:** round-capped. Step progress (x/5) runs orange on an Orange Wash track, generation runs violet, finished work turns green and failures red; values glide to new numbers in 300 ms.
- **Activity rows:** a 44 dp tone circle with a 20 dp icon, a Body Medium title, a Small Walnut meta line, an optional 6 dp progress bar up to 360 dp wide, and a trailing value or action; hover lays an Oat wash with 16 dp corners behind the row.
- **Loading:** skeleton blocks on Oat with a Shimmer band that sweeps every 1.4 s in unison; spinners are a short arc over a faint track.
- **Empty states:** a 96 dp double Orange Wash circle with a 32 dp Ember Ink icon, an H3 title, a Walnut line up to 420 dp wide, and at most one primary button.

### Overlays
- **Dialog:** 480 dp wide with 28 dp padding, 22 dp corners and the Sheet shadow over the Scrim; it grows from 0.96 and fades in over 180 ms. An optional 52 dp tone circle sits above an H2 title at 22 sp and Walnut text; actions are right-aligned, primary last, 12 dp apart; Esc, the scrim or × close it.
- **Drawer:** 440 dp (the queue 460 dp), floating 12 dp in from the window's right, top and bottom edges with 22 dp corners; it slides in over 240 ms, with an H3 20 sp title, a Small subtitle and a footer pinned under a hairline.
- **Toast:** a 380 dp card at the bottom right with 16 dp corners and the Pop shadow, a 36 dp soft tone circle and Body text; it slides 32 dp in over 220 ms, leaves in 180 ms, and hides after 4 s unless hovered.
- **Tooltip:** inverted Tooltip colours in Caption Medium with 8 dp corners, shown after 400 ms of hover, 22 dp below the pointer.
- **Tour:** a spotlight cut out of the dimmed window with a 16 dp bubble card (Sheet shadow) carrying Далі and Пропустити.

## Do's and Don'ts

### Do:
- **Do** give each view one Primary (orange) button and make the rest Secondary, Tinted, Ghost or a Link.
- **Do** put Ink on Orange (#2B241F; #1B1714 in «Вечір») on every orange fill, and use Ember Ink (#AF492A) whenever text must be orange.
- **Do** lay cards on the ground in grids with 24 dp gutters, and group inside a card with Oat wells and flat inset controls.
- **Do** colour states by meaning only: violet generating, green done, amber changed, red failed, Oat for empty and queued.
- **Do** take every colour from the palette tokens so the light, dark and system switch crossfades it in 250 ms, and check new text against its fill at 4.5:1.
- **Do** animate on the kit's clock and durations (120 ms hover, 180 ms dialogs and routes, 200 ms tabs, 240 ms sheets, 300 ms values) and let «Менше анімацій» make every animation instant.
- **Do** write labels in plain, sentence-case Ukrainian and give every icon-only button a tooltip.
- **Do** fill the window: 32, 24 or 16 dp page padding and more columns as it grows.

### Don't:
- **Don't** put white text or icons on Studio Orange (2.85:1), and don't press the orange deeper than #F9602F.
- **Don't** use orange to decorate, to show status or to stress something that can't be pressed; the step-progress arc and the one greeting dash are the only exceptions, and the dash never goes under other headings.
- **Don't** nest a card inside a card, or give anything inside a card its own shadow.
- **Don't** add kickers, eyebrows, overlines or uppercase letter-spaced labels.
- **Don't** use hard, offset or pure black shadows on the light ground; shadows are soft, two-layer and warm.
- **Don't** confine a page to a narrow centred column.
- **Don't** bounce or overshoot: springs are critically damped and entries ease out.
- **Don't** drift toward the category's dark neon dashboard; «Вечір» is warm brown evening light with the same grammar.
- **Don't** introduce a second typeface or system fonts.
