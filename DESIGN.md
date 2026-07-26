---
name: ScriptBoard Calibration Ledger
description: A light, precise operating ledger for observing one host and running scripts with confidence.
colors:
  canvas: "#F6F7F9"
  surface: "#FFFFFF"
  ink: "#171A1F"
  muted: "#5F6875"
  faint: "#7B8491"
  rule: "#D9DEE7"
  rule-strong: "#BBC3CF"
  accent: "#3659C9"
  accent-hover: "#2949B0"
  accent-soft: "#EDF1FF"
  success: "#18794E"
  success-soft: "#EAF6F0"
  warning: "#8A5A00"
  warning-soft: "#FFF5DA"
  danger: "#B42318"
  danger-soft: "#FFF0EE"
  danger-border: "#DFA19B"
  warning-border: "#E9C789"
  terminal: "#171A1F"
  terminal-ink: "#ECF0F5"
  terminal-rule: "#363B43"
  terminal-muted: "#AEB7C4"
  terminal-danger: "#FFAAA4"
typography:
  micro:
    fontSize: "0.7rem"
  detail:
    fontSize: "0.72rem"
  label:
    fontFamily: "Segoe UI Variable Text, Segoe UI, Microsoft YaHei UI, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 650
    lineHeight: 1.3
    letterSpacing: "0.025em"
  mono:
    fontFamily: "Cascadia Mono, SFMono-Regular, Consolas, monospace"
    fontSize: "0.8125rem"
    fontWeight: 450
    lineHeight: 1.55
  compact:
    fontSize: "0.85rem"
  table:
    fontSize: "0.86rem"
  supporting:
    fontSize: "0.88rem"
  body:
    fontFamily: "Segoe UI Variable Text, Segoe UI, Microsoft YaHei UI, system-ui, sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 450
    lineHeight: 1.55
  subheading:
    fontSize: "1rem"
    fontWeight: 650
    lineHeight: 1.3
  panelHeading:
    fontSize: "1.35rem"
  taskHeading:
    fontSize: "1.65rem"
  heading:
    fontFamily: "Segoe UI Variable Display, Segoe UI, Microsoft YaHei UI, system-ui, sans-serif"
    fontSize: "clamp(1.8rem, 3vw, 2.5rem)"
    fontWeight: 650
    lineHeight: 1.1
    letterSpacing: "-0.025em"
  verdict:
    fontSize: "clamp(1.35rem, 2.2vw, 1.8rem)"
  mobileHero:
    fontSize: "clamp(1.6rem, 8vw, 2.4rem)"
  section:
    fontFamily: "Segoe UI Variable Display, Segoe UI, Microsoft YaHei UI, system-ui, sans-serif"
    fontSize: "1.125rem"
    fontWeight: 650
    lineHeight: 1.25
rounded:
  compact: "4px"
  small: "5px"
  control: "6px"
  menu: "8px"
  panel: "10px"
  pill: "999px"
spacing:
  micro: "4px"
  control: "8px"
  group: "20px"
  section: "40px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
    padding: "9px 14px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "9px 11px"
---

# Design System: ScriptBoard Calibration Ledger

## Creative direction

ScriptBoard is a quiet operational ledger, not a dramatic command center. It should feel like a calibrated instrument sheet: cool white paper, graphite rules, compact labels, precise measurements, and one muted blue that always means selection or action.

The interface is visually light while remaining information-dense. Hierarchy comes from alignment, spacing, rule weight, and typography—not dark panels, gradients, ornamental canvases, or stacks of floating cards.

## Core principles

1. **Verdict before telemetry.** The overview begins with what deserves attention, then exposes measurements and recent activity.
2. **Flat at rest.** Persistent regions use tonal change and one-pixel rules. Shadows belong only to drawers, dialogs, and task panels.
3. **Blue has a job.** Calibration blue marks current selection, focus, and the primary action. It is not ambient decoration.
4. **Facts stay honest.** CPU and memory are measurements, not invented health judgments. Status is derived only from stale data, collection errors, and existing storage rules.
5. **One primary action.** Each page has one obvious next action. Advanced and destructive actions stay progressively disclosed.
6. **Bilingual by construction.** Chinese and English share the same information hierarchy. Layout must tolerate longer English labels without truncating meaning.
7. **Icons are functional.** Use Lucide only. Never use emoji, custom logo glyphs, or icons where text is clearer.

## Visual language

### Color

- Canvas `#F6F7F9`; working surfaces `#FFFFFF`.
- Primary text `#171A1F`; secondary text `#5F6875`.
- Rules `#D9DEE7`, strengthened to `#BBC3CF` where separation must be explicit.
- Calibration blue `#3659C9`, with `#EDF1FF` for selected rows and quiet focus context.
- Success, warning, and danger colors always pair with text or an icon; color never carries state alone.
- The log terminal is the only persistent dark surface because it is a distinct reading mode.

All body copy and controls target WCAG 2.2 AA contrast. Muted copy remains at least 4.5:1 against its resting surface.

### Typography

- Use the operating-system UI stack. Do not fetch fonts.
- Page titles are compact, sentence-case, and never become decorative monuments.
- Use monospace only for paths, identifiers, commands, log output, and numeric measurements that benefit from fixed alignment.
- Labels are short, slightly tracked, and structurally quiet.

### Shape and elevation

- Inline actions use 4–5px corners, standard controls use 6px, menus use 8px, and task panels or dialogs use 10px.
- Tables and page sections remain unboxed.
- Pills are reserved for short status labels and filters.
- No gradient, glass, glow, backdrop blur, or ambient drop shadow.

## Application shell

Desktop uses a fixed 232px sidebar with grouped destinations:

- Monitor: Overview
- Resources: Files, Variables
- Configuration: Quick runs, Schedules
- History: Runs, Audit

Status, language, settings, and account controls live at the bottom. The wordmark is plain text. Settings is a separate workspace that includes account and version protection.

Below 1024px the sidebar becomes a modal drawer with a scrim. The underlying page does not reflow into a second navigation system.

## Page patterns

### Overview

Use a single-page drilldown:

1. observation verdict and collection timestamp;
2. exceptional conditions, if any;
3. thin inline-SVG measurement bands for CPU, memory, and storage;
4. active and recent runs;
5. compact environment facts.

Measurements are not generic statistic cards. Their baseline, current value, and peak belong to one horizontal reading band.

### Dense records

Desktop records use flat tables with sticky or persistent column meaning where helpful. On mobile, each row becomes a compact labeled record while preserving the same actions and core facts.

Secondary record actions use the same 34px horizontal ellipsis trigger across files, variables, Quick Runs, and schedules, expanding to a 44px touch target on mobile. Open menus overlay record and table boundaries without being clipped by scrolling containers.

### Files

Breadcrumbs and a directory list define place. The canonical absolute managed-root location sits directly beneath the relative breadcrumb as selectable technical context. Search and the one primary creation action sit in the page header. A compact drop target above the directory list accepts files for immediate upload into the current path; replacement remains an explicit option in the full upload task. Destructive actions are secondary and explicit.

### Run detail

The log is the primary surface. Status, timing, script path, arguments, and stop/save actions form a compact header around it. Pause/resume is available by button and keyboard shortcut.

### Task panels

Create and edit flows use a right-side task panel on capable clients. Every panel owns a semantic GET URL and renders as a complete page without JavaScript.

## Motion

Motion explains state: drawer entry, task-panel entry, row insertion, acquisition refresh, or status transition. Use 140–220ms transitions with restrained easing. Honor `prefers-reduced-motion` by removing nonessential movement.

## Copy

- Use concrete verbs: “Run script”, “Create schedule”, “Restore file”.
- Localize human-facing status labels; preserve raw identifiers only in technical details.
- Empty states contain one sentence and one primary action.
- Confirmation language scales with recoverability: reversible moves are concise; permanent deletion states the consequence.
