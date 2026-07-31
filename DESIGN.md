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
  website-success: "#007F5F"
  website-success-signal: "#00B884"
  website-success-soft: "#E2F8F0"
  website-fault-red: "#C42B21"
  website-fault-magenta: "#A32167"
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
  root:
    fontSize: "17px"
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
  button:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "8px 13px"
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.surface}"
    rounded: "{rounded.control}"
    padding: "8px 13px"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.control}"
    padding: "8px 13px"
  button-danger:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.danger}"
    rounded: "{rounded.control}"
    padding: "8px 13px"
  button-compact:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.small}"
    padding: "6px 10px"
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
- Website-monitoring success text uses accessible emerald `#007F5F`; compact success signals use brighter teal-green `#00B884` so healthy and fault states retain a strong luminance and hue difference under reduced red-green perception.
- Success, warning, and danger colors always pair with text, shape, or an icon; color never carries state alone.
- The log terminal is the only persistent dark surface because it is a distinct reading mode.

All body copy and controls target WCAG 2.2 AA contrast. Muted copy remains at least 4.5:1 against its resting surface.

### Typography

- Use a 17px root size with the operating-system UI stack. Do not fetch fonts.
- Page titles are compact, sentence-case, and never become decorative monuments.
- Use monospace only for paths, identifiers, commands, log output, and numeric measurements that benefit from fixed alignment.
- Labels are short, slightly tracked, and structurally quiet.

### Shape and elevation

- Inline actions use 4–5px corners, standard controls use 6px, menus use 8px, and task panels or dialogs use 10px.
- Tables and page sections remain unboxed.
- Pills are reserved for short status labels and filters.
- No gradient, glass, glow, backdrop blur, or ambient drop shadow.

### Button system

Buttons use an explicit, opt-in base class; raw `button` elements do not inherit the standard visual treatment. This keeps menu items, disclosure triggers, schedule choices, and icon controls semantically distinct.

| Function | Contract | Use |
| --- | --- | --- |
| Primary | `.button.button--primary` | The single page, task-panel, or confirmation action: create, save, upload, or the page’s principal search. |
| Secondary | `.button` | Visible alternatives such as create group, preview, download, or a neutral form action. |
| Quiet | `.button.button--quiet` | Back, cancel, clear, open, and edit actions that should not compete with the primary action. |
| Danger | `.button.button--danger` | Standalone stop, purge, disable, or destructive confirmation actions; destructive menu items use danger text instead. |
| Compact | `.button.button--compact` | Repeated record actions such as Run, Run now, Restore, and row-level Open/Edit. Compact never implies primary. |
| Icon | `.icon-button` or a named contextual control | Close, more actions, copy, reveal, and navigation toggles. Every icon-only control has a localized accessible name. |
| Choice/disclosure | Contextual selectors with `aria-pressed` or `aria-expanded` | Cron modes, weekdays, grouped records, menus, and technical disclosures. State remains legible without color alone. |

- A rendered page or task panel has at most one blue primary action. A single empty-state action may be primary only when the same scope has no other primary action.
- Standard buttons are 38px high; buttons aligned with fields are 42px; compact desktop actions are 34px. Key mobile controls and all compact/icon actions expand to at least 44px.
- Button-like navigation remains an anchor and uses GET. State changes remain explicit `button type="submit"` controls inside CSRF-protected POST forms; client-only controls use `type="button"`.
- Hover, active, focus-visible, disabled, busy, success, and error states preserve layout and communicate through text, icon, border, or shape as well as color.
- Icons are Lucide at the shared 1rem scale with an 8px text gap. Text remains the accessible name whenever it is present; icon-only controls require a localized `aria-label`.

## Application shell

Desktop uses a fixed 232px sidebar with grouped destinations:

- Monitor: Overview, Applications
- Resources: Files, Variables
- Configuration: Quick runs, Schedules
- History: Runs, Audit

Status, language, and settings controls live at the bottom. The wordmark is plain text. Settings is a separate workspace with one shared horizontal tab band below the page heading: Account is available to every signed-in user, Users appears only for the administrator, and status display, version protection, and application updates appear for the administrator and maintainer. The tab band scrolls horizontally on narrow screens instead of becoming a second sidebar. Website fault-color choice is a browser-local display preference and never changes monitoring truth.

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

### Applications

Lead with the current application snapshot. A flat fact strip reports host applications, Docker containers, pinned applications, and collection time. Pinned and running applications share one flat measurement-row grammar. Selecting a row opens a right-side details drawer while preserving the application list as context.

Pinned and running records own independent refresh switches. Pinned refresh starts on, while the running list starts off so a stable sorted snapshot remains readable. Search, kind, sort, and direction controls sit together before the running records and keep the same order on mobile.

Pinned application drawers expose History and Runtime details. Running-application drawers expose Runtime details only; they never imply that an unpinned item owns retained history. Drawer facts do not follow the five-second list refresh. They update only when the drawer opens, the history range changes, or the administrator requests a manual refresh.

Host applications show no redundant type tag. Docker containers use one quiet Docker tag beside the container name and show the image as technical context. Pin and Unpin remain CSRF-protected POST actions that work without JavaScript. The page never offers process termination, container control, health thresholds, or alert actions.

### Dense records

Desktop records use flat tables with sticky or persistent column meaning where helpful. On mobile, each row becomes a compact labeled record while preserving the same actions and core facts.

Secondary record actions use the same 34px horizontal ellipsis trigger across files, variables, Quick Runs, and schedules, expanding to a 44px touch target on mobile. Open menus overlay record and table boundaries without being clipped by scrolling containers; terminal-row menus open upward, and tables expose horizontal scrolling only when their content is genuinely wider than the available space.

### Application updates

The update page is an operational handoff, not a promotional release screen. It leads with the installed build, installation mode, latest verified stable release, check time, and active Run count. Release notes remain plain readable text. “Download and verify” is the only primary action until preparation succeeds; “Install and restart” replaces it and requires explicit confirmation.

During service handoff, an `aria-live` status notice explains the temporary disconnect and the page reconnects automatically. Signature, digest, platform, protocol, validation, rollback, and recovery failures remain visible as text rather than color-only states. Portable and development modes explain their capability boundary instead of showing a disabled installation control without context.

### Files

Breadcrumbs and a directory list define place. The canonical absolute managed-root location is available only in Files settings as selectable, read-only technical context; the file workspace stays relative to that root. New directory and Upload files form one header action group, with Upload files as the single primary action. Browser-local Quick access stores pinned folders, renders one folder per row, and starts collapsed. Dot-prefixed entries are hidden by default and a toolbar switch exposes them without losing the current search or sort state. The complete file-browser surface accepts dragged files for immediate upload into the current path and reveals its boundary and status only while a drag, upload, or error is active. Upload, move, rename, and Trash restore never overwrite a same-name item by default; a focused conflict dialog offers skip, recoverable overwrite, or rename. Destructive actions remain secondary and explicit, with Trash at the far edge of the file toolbar.

### Run detail

The log is the primary surface. Status, timing, script path, arguments, and stop/save actions form a compact header around it. Pause/resume is available by button and keyboard shortcut.

### Task panels

Create and edit flows use a right-side task panel on capable clients. Every panel owns a semantic GET URL and renders as a complete page without JavaScript. Opening a desktop panel keeps the workspace URL in the address bar while browser Back closes the panel and Forward restores it.

## Motion

Motion explains state: drawer entry, task-panel entry, row insertion, acquisition refresh, or status transition. Use 140–220ms transitions with restrained easing. Honor `prefers-reduced-motion` by removing nonessential movement.

## Copy

- Use concrete verbs: “Run script”, “Create schedule”, “Restore file”.
- Localize human-facing status labels; preserve raw identifiers only in technical details.
- Empty states contain one sentence and one primary action.
- Confirmation language scales with recoverability: reversible moves are concise; permanent deletion states the consequence.
