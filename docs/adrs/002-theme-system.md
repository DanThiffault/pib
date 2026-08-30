# ADR-002: Cyberpunk TUI Theme System

## Status
Accepted

## Context

The pib TUI currently uses ad-hoc lipgloss styles scattered across UI components. As we expand the interface with tabbed panes, issue browsers, and dense text views, we need a unified theme system that is visually distinctive, accessible in common terminals, and simple to maintain.

Four directions were prototyped (see `prototype/8-cyberpunk-themes/` and issue #8). Each was evaluated against:

- **Readability** — code blocks, dense issue bodies, selected vs unselected items
- **Contrast** — WCAG-like perceptual contrast in typical 256-color and true-color terminals
- **Performance** — lipgloss complexity (nested styles, gradient simulation, per-frame transforms)
- **Maintainability** — how many color tokens and style helpers the theme package must expose

## Prototype Evaluation

### Option A: Holographic (blue/cyan glow, rounded box-drawing)

**Palette**
- Background: `#0a0f1a` (deep navy)
- Foreground: `#c5e4f8` (soft ice blue)
- Dim: `#5a7a9a` (slate)
- Accents: `#00e5ff` (ice cyan), `#2979ff` (electric blue), `#64ffda` (aqua)
- Selected bg: `#1a2a4a`, fg: `#00e5ff`
- Borders: `#2979ff`, focus: `#00e5ff`
- Border style: `RoundedBorder()`

**Findings**
- **Pros:** Most "modern" of the four; rounded borders look polished; unified cool palette feels cohesive; excellent for long reading sessions (low eye strain).
- **Cons:** Subtle distinctions between the three blues can collapse in 256-color terminals; semantic coding is limited (everything is a shade of blue); less visually exciting than Synthwave or Tokyo Moon.
- **Contrast:** Good, but accent-to-background ratios are lower because all hues are similar.
- **Complexity:** Medium — requires careful ordering of the three blues, and rounded borders can render poorly in some terminals.

### Option B: Synthwave (purple/pink/orange on near-black)

**Palette**
- Background: `#0d001a` (deep purple-black)
- Foreground: `#e0d0ff` (soft lavender)
- Dim: `#705090` (muted violet)
- Accents: `#ff00aa` (hot pink), `#ff7700` (orange), `#aa00ff` (purple)
- Selected bg: `#3a0040`, fg: `#ff00aa`
- Borders: `#ff00aa`, focus: `#ff7700`
- Border style: `NormalBorder()`

**Findings**
- **Pros:** Very high visual impact; warm accents against cool background create strong focal points; the "sunset" progression is intuitive for severity levels.
- **Cons:** High saturation can cause chromatic aberration in some terminals; pink/orange on black is lower perceived contrast than cyan on black; long-form text readability suffers because lavender on purple-black is softer than white on dark grey.
- **Contrast:** Moderate — the warm accents pop, but body text is less crisp than Tokyo Moon.
- **Complexity:** Low — simple borders, flat colors, only the grid simulation adds any cost.

### Option C: Tokyo Moon (from tokyonight.nvim palette)

**Palette**
- Background: `#222436` (dark slate)
- Foreground: `#c8d3f5` (soft periwinkle)
- Dim: `#636da6` (muted indigo)
- Accents: `#82aaff` (sky blue), `#c099ff` (lavender), `#c3e88d` (sage green)
- Selected bg: `#2f334d`, fg: `#82aaff`
- Borders: `#82aaff`, focus: `#c099ff`
- Border style: `RoundedBorder()`

**Findings**
- **Pros:** Mature, battle-tested palette designed for 8+ hour coding sessions; excellent contrast without eye fatigue; three distinct accent hues provide clear semantic coding (blue = primary, lavender = secondary, green = success/tag); degrades gracefully to 256-color terminals because the palette was designed for terminal vim.
- **Cons:** Less "cyberpunk neon" than Synthwave; more subdued than Holographic's electric blues.
- **Contrast:** Excellent — `#c8d3f5` on `#222436` is high contrast, and the dim color `#636da6` is deliberately reserved for comments/metatext.
- **Complexity:** Low — rounded borders, 7 core tokens, no effects.

### Option D: Default (standard Charm/lipgloss aesthetic)

**Palette**
- Background: `#1A1A1A` (charcoal)
- Foreground: `#F4F4F4` (near-white)
- Dim: `#6B6B6B` (mid grey)
- Accents: `#F25D94` (pink), `#A49FEB` (lavender), `#ED567A` (coral)
- Selected bg: `#F25D94`, fg: `#1A1A1A`
- Borders: `#F25D94`, focus: `#A49FEB`
- Border style: `NormalBorder()`

**Findings**
- **Pros:** Immediately familiar to anyone who uses Charm tools; high contrast; lowest implementation risk.
- **Cons:** Least distinctive — looks like every other Bubble Tea app; the pink-heavy palette can feel juvenile for a developer workflow tool.
- **Contrast:** Excellent.
- **Complexity:** Lowest — normal borders, flat palette, standard lipgloss defaults.

## Decision

**Tokyo Moon (Option C)** is accepted as the default theme.

**Reasoning**

1. **Best readability** — `#c8d3f5` on `#222436` was designed for long coding sessions. It is crisp without the harshness of pure white on pure black.
2. **Strongest accessibility** — the tokyonight palette intentionally separates body text, dimmed metadata, and accent colors. The selected state uses a subtle background lift (`#2f334d`) rather than an aggressive inversion, which is easier on the eyes.
3. **Proven terminal compatibility** — folke/tokyonight.nvim is used by tens of thousands of developers in terminals daily. The colors map cleanly to 256-color fallback palettes.
4. **Semantic range** — three distinct accent hues (blue, lavender, green) give us enough range for primary actions, secondary/focus states, and success/tags without exploding the token count.
5. **Distinctive but not alienating** — it is recognizable as a dark theme but carries enough personality (the periwinkle body text, the sage green accent) to feel like pib's own identity.

Holographic, Synthwave, and Default were evaluated and ruled out. Holographic's monochrome-blue limits semantic differentiation; Synthwave's high saturation hurts long-form readability; Default is too generic.

## Chosen Theme Vocabulary

```go
package theme

import "github.com/charmbracelet/lipgloss"

// Color tokens — Tokyo Moon palette (folke/tokyonight.nvim)
type Palette struct {
    Bg          lipgloss.Color // #222436
    Fg          lipgloss.Color // #c8d3f5
    FgDim       lipgloss.Color // #636da6
    FgBright    lipgloss.Color // #e0e8f8
    Primary     lipgloss.Color // #82aaff  (sky blue)
    Secondary   lipgloss.Color // #c099ff  (lavender)
    Tertiary    lipgloss.Color // #c3e88d  (sage green)
    Border      lipgloss.Color // #82aaff
    BorderFocus lipgloss.Color // #c099ff
    SelectedBg  lipgloss.Color // #2f334d
    SelectedFg  lipgloss.Color // #82aaff
    Gutter      lipgloss.Color // #1e2030
}

// Style constructors
type Styles struct {
    Base        lipgloss.Style
    Title       lipgloss.Style
    Body        lipgloss.Style
    Dim         lipgloss.Style
    Code        lipgloss.Style
    Tag         lipgloss.Style
    Selected    lipgloss.Style
    Border      lipgloss.Style
    BorderFocus lipgloss.Style
    TabActive   lipgloss.Style
    TabInactive lipgloss.Style
    Header      lipgloss.Style
    Help        lipgloss.Style
}

func NewStyles(p Palette) Styles
```

## Consequences

Positive:
- A small, named palette makes it easy to add dark-mode variants later by replacing a single `Palette` value.
- The subtle background lift for selection (`#2f334d`) is immediately visible without the aggressive inversion of neon themes.
- Three accent colors give us enough semantic range for tags, errors, and focus states.
- Terminal compatibility is proven by widespread nvim usage.

Negative:
- Rounded borders can render as `?` or `+` in older terminals (e.g., Windows CMD without modern font). We should detect terminal capability and fall back to `NormalBorder()` if needed.
- The palette is cool-toned; users who prefer warm themes may ask for a variant. We should expose `Palette` as a public variable so users can override it at compile time or via config.
- `#222436` is slightly lighter than pure black; on OLED displays this may not achieve true black levels. This is acceptable for readability but may matter to users who want maximum battery savings.

## Next Steps

- Issue #10 will implement the `theme` package against the palette and `Styles` vocabulary above.
- The prototype directory (`prototype/8-cyberpunk-themes/`) can be discarded once implementation is complete.
