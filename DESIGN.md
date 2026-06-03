# Bio-Synthetic Design System Specification (Codename: _Verdant-Synth_)

## Version 1.0.0

A design system tailored for next-generation, ethical AI applications. This system deliberately subverts the cold, neon-cyberpunk clichés of traditional machine learning interfaces, opting instead for an organic, grounded canvas punctuated by a highly synthetic, high-visibility "AI Spark" accent.

---

## 1. Core Philosophy

The **Bio-Synthetic** aesthetic represents a bridge between technological advancement and human/ecological harmony. It assumes AI is an extension of natural workflows rather than an adversarial force.

- **Grounded Base:** Soft, dark, terrestrial canvas elements reduce cognitive fatigue and establish authority.
- **Intentional Contrast:** High-contrast text fields provide immediate legibility and layout definition.
- **The AI Spark:** The bright accent color is treated like a rare element—highly focused, reactive, and reserved strictly for intelligent machine interactions.

---

## 2. Color Tokens

### 2.1 Primary Palette

| Token                | Semantic Name | Hex Code  | RGB             | HSL              | Application                                                     |
| :------------------- | :------------ | :-------- | :-------------- | :--------------- | :-------------------------------------------------------------- |
| `--color-bg-base`    | Deep Moss     | `#0A110D` | `10, 17, 13`    | `146°, 26%, 5%`  | Global application background canvas.                           |
| `--color-bg-surface` | Forest Shadow | `#131F18` | `19, 31, 24`    | `145°, 24%, 10%` | Cards, sidebars, modal overlays, elevated surfaces.             |
| `--color-fg-base`    | Off-Alabaster | `#F1F4F2` | `241, 244, 242` | `140°, 9%, 95%`  | Primary headings, body copy, high-readability text.             |
| `--color-ui-muted`   | Muted Sage    | `#5C6B62` | `92, 107, 98`   | `144°, 8%, 39%`  | Inactive borders, secondary captions, icon frames.              |
| `--color-accent-ai`  | Cyber Lime    | `#B6FF00` | `182, 255, 0`   | `77°, 100%, 50%` | AI actions, generation triggers, model status, processing dots. |

### 2.2 Functional & Supporting Colors

| Token                   | Semantic Name | Hex Code  | Usage                                                |
| :---------------------- | :------------ | :-------- | :--------------------------------------------------- |
| `--color-border-subtle` | Foliage Line  | `#223329` | Default 1px structural borders and division rules.   |
| `--color-text-muted`    | Lichen Grey   | `#9CAF9F` | Paragraphs, meta-data, time-stamps.                  |
| `--color-feedback-err`  | Overheat Red  | `#FF3B30` | Validations, critical model errors, system failures. |

---

## 3. Typography & Scale

The system utilizes clean, neo-grotesque sans-serif typefaces paired with technical monospaced elements for data display and token streaming.

- **Primary Typeface:** `Geist` or `Inter` (Clean, legible, humanist terminal traits)
- **Code/AI Output Typeface:** `Geist Mono` or `JetBrains Mono` (For structural layout data and raw prompt returns)

### Type Scale Hierarchy

```css
--text-xs: 0.75rem (12px) /* Line height: 1.2  | Secondary Metadata */
  --text-sm: 0.875rem (14px)
  /* Line height: 1.4  | Labels, Sidebars, Small Buttons */ --text-base: 1rem
  (16px) /* Line height: 1.5  | Chat Messages, Prompt Inputs */
  --text-lg: 1.25rem (20px) /* Line height: 1.4  | Section Subheadings */
  --text-xl: 1.5rem (24px) /* Line height: 1.3  | Card Titles, Modal Headers */
  --text-2xl: 2.25rem (36px)
  /* Line height: 1.2  | Primary Hero Metrics / App Branding */;
```

---

## 4. UI Components & Architectural States

### 4.1 The Prompt Architecture (Input Box)

The focal point of any conversational or generative interface.

- **Default State:** `--color-bg-surface` background, 1px border of `--color-border-subtle`. Typography uses `--color-fg-base`.
- **Focused State:** Border transitions smoothly to `--color-ui-muted`. A soft internal box shadow (`0 0 0 2px rgba(92, 107, 98, 0.2)`) is applied.
- **Active Generation State:** When the user hits enter, the action button changes state (see 4.2), and the prompt container's bottom edge receives a 1px border gradient transitioning from `--color-border-subtle` to `--color-accent-ai`.

### 4.2 Interactive Triggers (Buttons)

#### Primary Action Button (Standard User Trigger)

- **Background:** `#1D2F24` (A medium-light variant of Deep Moss)
- **Text/Icon:** `--color-fg-base`
- **Hover:** Background changes to `#273F30`.

#### The AI Action Button (Triggering Generative Workflows)

- **Background:** `--color-accent-ai` (`#B6FF00`)
- **Text/Icon:** `#0A110D` (Pure Deep Moss text for extreme, crisp contrast)
- **Hover:** Background shifts slightly toward `#C4FF33`; minor 4px radial glow blur behind the button.
- **Disabled/Processing:** Background transitions to `#252E11` with text colored in `--color-ui-muted`.

### 4.3 Streaming Token Display & Chat Bubbles

- **User Messages:** Stored inside clean, un-bordered text blocks aligned right. Text uses `--color-fg-base`.
- **AI-Generated Messages:** Housed within a surface using `--color-bg-surface`. The left edge features a solid 2px accent bar of `--color-accent-ai` to visually anchor machine intelligence responses.
- **The Token Cursor:** During real-time data streaming, the trailing cursor is a solid rectangle block (`5px` width, `15px` height) colored in `--color-accent-ai`, blinking smoothly at `1.2s` intervals.

---

## 5. Micro-Interactions & Animation Guidelines

The Bio-Synthetic theme requires fluid, lifelike physics for transitions. Avoid sudden snaps; favor ease-out curves that resemble organic acceleration.

### 5.1 The AI Processing State (The Horizon Pulse)

When an inference is computing, avoid a generic spinning wheel. Instead, utilize a linear horizon pulse across the top edge of the interface viewport:

```css
@keyframes horizonPulse {
  0% {
    opacity: 0.3;
    transform: scaleX(0.8);
  }
  50% {
    opacity: 1;
    transform: scaleX(1);
    filter: drop-shadow(0 0 4px #b6ff00);
  }
  100% {
    opacity: 0.3;
    transform: scaleX(0.8);
  }
}
/* Applied to a 1px line of --color-accent-ai directly above the content frame */
```

### 5.2 Hover Elevation

Cards (`--color-bg-surface`) should not lift dynamically on the Z-axis via deep shadows. Instead, when hovered, their 1px border transforms from `--color-border-subtle` to `--color-ui-muted` via a `200ms ease-in-out` transition. This maintains a clean, minimalist structural look.

---

## 6. Accessibility & Contrast Compliance (WCAG)

Maintaining readable interfaces is a strict rule within the Bio-Synthetic theme, given the intensity of the Cyber Lime accent.

1. **Text over Accent:** Text rendered on top of a `--color-accent-ai` background must _always_ use dark canvas text (`#0A110D`). Never use white (`#FFFFFF`) or Alabaster (`#F1F4F2`) over Lime, as it instantly violates WCAG AA contrast standards.
2. **Lime for UI Components:** When Cyber Lime (`#B6FF00`) is used for graphical elements (like toggle states, active icons, or status points) against the Deep Moss canvas (`#0A110D`), it delivers an exceptional contrast ratio of over **11:1**, vastly exceeding the WCAG AAA requirement (7:1).
3. **Muted Sage Restrictions:** `--color-ui-muted` (`#5C6B62`) should _never_ be used for long body paragraphs, as its contrast ratio against Deep Moss sits around **3.4:1**. Restrict Sage to borders, decorative lines, and disabled interaction states. For regular secondary text, always promote to `--color-text-muted` (`#9CAF9F`), which hits a safe **6.5:1** contrast ratio.
