---
name: ScriptBoard Signal Deck
description: An industrial signal console for operating scripts on a single host.
colors:
  void: "#0a0b0d"
  deck: "#101216"
  deck-raised: "#171a20"
  signal: "#d7ff45"
  signal-deep: "#b6df22"
  frost: "#f0f2ea"
  graphite: "#b5bab1"
  graphite-dim: "#858b82"
  seam: "#2c3038"
  danger: "#ff635e"
  warning: "#ffc857"
  nav-index: "#555b55"
  icon-muted: "#7e857c"
  interaction-seam: "#626975"
  signal-hover: "#e4ff81"
  danger-muted: "#ff928e"
  placeholder: "#7f867d"
  hover-seam: "#5a616c"
  trace-label: "#454b46"
  peak-trace: "#5f6854"
  row-seam: "#23272d"
  row-text: "#d5d8d1"
  status-seam: "#4d5932"
  terminal-ink: "#d9f6c4"
  signal-ink: "#445113"
  shadow-inset: "rgba(0,0,0,.18)"
  shadow-terminal: "rgba(0,0,0,.28)"
typography:
  display:
    fontFamily: "Bahnschrift SemiCondensed, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(2.75rem, 6.2vw, 5.25rem)"
    fontWeight: 700
    lineHeight: 0.94
    letterSpacing: "-0.03em"
  body:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 480
    lineHeight: 1.65
  label:
    fontFamily: "Cascadia Mono, SFMono-Regular, Consolas, monospace"
    fontSize: "0.6875rem"
    fontWeight: 650
    lineHeight: 1.35
    letterSpacing: "0.06em"
  micro:
    fontFamily: "Cascadia Mono, Consolas, monospace"
    fontSize: "10px"
    fontWeight: 650
    lineHeight: 1.35
  meta:
    fontFamily: "Cascadia Mono, Consolas, monospace"
    fontSize: "10px"
    fontWeight: 650
    lineHeight: 1.3
  control:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "13px"
    fontWeight: 650
    lineHeight: 1.3
  compact-control:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "12px"
    fontWeight: 650
    lineHeight: 1.3
  input:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "14px"
    fontWeight: 520
    lineHeight: 1.45
  mobile-input:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "16px"
    fontWeight: 520
    lineHeight: 1.45
  support:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "13px"
    fontWeight: 480
    lineHeight: 1.65
  brand:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "17px"
    fontWeight: 730
    lineHeight: 1.2
  section:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(20px, 2vw, 28px)"
    fontWeight: 700
    lineHeight: 1.2
  metric:
    fontFamily: "Bahnschrift SemiCondensed, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(40px, 5vw, 70px)"
    fontWeight: 680
    lineHeight: 0.94
  state:
    fontFamily: "Bahnschrift SemiCondensed, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(30px, 4vw, 54px)"
    fontWeight: 680
    lineHeight: 1
  empty-state:
    fontFamily: "Bahnschrift SemiCondensed, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(28px, 4vw, 52px)"
    fontWeight: 700
    lineHeight: 1.05
  login-display:
    fontFamily: "Bahnschrift SemiCondensed, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(52px, 12vw, 84px)"
    fontWeight: 720
    lineHeight: 0.9
  mobile-brand:
    fontFamily: "Segoe UI Variable Text, Microsoft YaHei UI, sans-serif"
    fontSize: "15px"
    fontWeight: 730
    lineHeight: 1.2
  mobile-display:
    fontFamily: "Bahnschrift SemiCondensed, Microsoft YaHei UI, sans-serif"
    fontSize: "clamp(38px, 12vw, 54px)"
    fontWeight: 700
    lineHeight: 0.98
rounded:
  micro: "4px"
  control: "6px"
  switcher: "8px"
  panel: "12px"
  pill: "999px"
spacing:
  micro: "4px"
  control: "10px"
  group: "20px"
  section: "48px"
components:
  button-primary:
    backgroundColor: "{colors.signal}"
    textColor: "{colors.void}"
    rounded: "{rounded.control}"
    padding: "10px 16px"
  input:
    backgroundColor: "{colors.deck}"
    textColor: "{colors.frost}"
    rounded: "{rounded.control}"
    padding: "10px 12px"
---

# Design System: ScriptBoard Signal Deck

## Overview

**Creative North Star: "Signal Deck"**

ScriptBoard feels like a live operations surface assembled from industrial control panels, technical publishing and broadcast telemetry. The interface is dark because operators may monitor it for long periods; depth comes from black material layers, precise seams and moving light rather than decorative glow.

Expression concentrates in scale, crop and motion. Page titles behave like equipment labels enlarged into architectural type, while real data remains compact and calm. The system rejects generic dashboard mosaics and neon cyberpunk ornament.

**Key Characteristics:**

- Asymmetric page composition with one oversized typographic anchor.
- Acid signal color reserved for active state and primary action.
- Live data expressed through motion, trace lines and temporal transitions.
- Dense tables remain flat, aligned and highly scannable.
- Lucide is the only icon language; the interface contains no emoji.

## Colors

The palette is restrained industrial black with a single high-visibility signal color.

### Primary

- **Signal Acid:** Used only for primary actions, live state and the most important current selection.

### Secondary

- **Failure Coral:** Reserved for destructive actions and states that require recovery.
- **Caution Amber:** Used for stale, waiting and degraded states.

### Neutral

- **Machine Void:** The page-scale background and deepest terminal surfaces.
- **Instrument Deck:** The default working surface.
- **Raised Deck:** Temporary panels and interactive elevation.
- **Cold Frost:** Primary text and high-contrast glyphs.
- **Graphite Trace:** Secondary text, timestamps and inactive labels.
- **Mechanical Seam:** Dividers and control boundaries.

**The Signal Is State Rule.** The acid color never acts as ambient decoration; it always identifies an action, selection or live condition.

## Typography

**Display Font:** Bahnschrift SemiCondensed with Chinese system fallbacks
**Body Font:** Segoe UI Variable Text with Microsoft YaHei UI fallback
**Label/Mono Font:** Cascadia Mono with Consolas fallback

**Character:** Condensed display type gives page titles the force of industrial labels without sacrificing Chinese legibility. Monospace appears only for code, paths, measurements and machine state.

### Hierarchy

- **Display:** Oversized page identity, limited to one per surface.
- **Headline:** Section orientation and modal task titles.
- **Title:** Record names and focused data labels.
- **Body:** Operational explanation, kept concise and under 72 characters where possible.
- **Label:** Machine state, timestamps, navigation indexes and field metadata.

**The One Monument Rule.** Every page gets one typographic monument; supporting headings stay compact.

## Layout

Desktop uses one fixed horizontal command rail and a wide working deck. The first content row is intentionally asymmetric: host or task identity leads while live context stays compact. Tables run wide and flat instead of being broken into cards. On mobile the command rail becomes a horizontally scrollable navigation strip without introducing a second application shell.

Spacing follows four roles: micro relationships, control interiors, grouped content and major section separation. Dense data may be tight; task changes receive obvious breathing room.

## Elevation & Depth

The system is flat at rest. Tonal layers and one-pixel seams establish hierarchy. Shadows appear only for floating editors, open menus and focused task panels, with a downward offset and broad black blur. A subtle canvas-rendered signal field gives the overall surface atmospheric depth without obscuring content.

**The Resting Surface Rule.** Persistent page regions do not float; only transient interaction earns elevation.

## Shapes

Controls use clipped, compact corners. Persistent panels use restrained 12px radii only where a boundary is functionally necessary; tables and page sections remain unboxed. Pills are limited to status and tightly scoped filters.

## Components

### Buttons

- **Shape:** Compact mechanical corners.
- **Primary:** Signal-acid fill with near-black type.
- **Hover / Focus:** Fast physical lift, stronger contrast and a clearly visible focus ring.
- **Secondary:** Transparent or raised-deck surface with a mechanical seam.

### Chips

- **Style:** Small mono labels with status color and explicit text.
- **State:** Selected filters use signal fill; status never relies on color alone.

### Cards / Containers

- **Corner Style:** Restrained panel corners only where grouping cannot be expressed by layout.
- **Background:** Tonal black layers.
- **Shadow Strategy:** Flat at rest; shadows are transient.
- **Border:** A single mechanical seam, never paired with an ambient card shadow.

### Inputs / Fields

- **Style:** The label, control and supporting copy form one recessed field unit; inputs are not isolated boxes floating inside forms.
- **Focus:** The complete field unit receives a signal boundary while the control keeps a precise inner baseline.
- **Error / Disabled:** Error color plus explanatory copy; disabled controls retain readable contrast.

### Navigation

The global command rail pairs Lucide icons with short Chinese labels and never shares its axis with a second global navigation. Active state uses a restrained signal wash and a precise baseline marker. Contextual navigation is visually quieter and owns only records or views inside the current destination. Mobile preserves global order in a scrollable dock.

### Live Trace

Real-time charts and status lines use thin traces, restrained peak overlays and animated acquisition feedback. Motion stops under reduced-motion preferences.

## Do's and Don'ts

### Do:

- **Do** make current state and next action understandable before adding atmosphere.
- **Do** use oversized type once per page to create identity.
- **Do** keep operational tables aligned, flat and horizontally recoverable on small screens.
- **Do** pair every icon-only control with an accessible name.

### Don't:

- **Don't** use emoji, icon fonts or mixed icon families.
- **Don't** scatter the signal color across passive decoration.
- **Don't** build dashboard mosaics from interchangeable cards.
- **Don't** use glow, blur or animation where it does not communicate state or depth.
