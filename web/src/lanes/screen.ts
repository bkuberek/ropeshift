// A pane's screen as the server sends it: text, and the SGR sequences that set its colour and emphasis.
// The palette has four text colours, so a colour reads by its hue: red as refuse, blue and violet as
// marker, grey as muted, and the rest as ink. A background is left to the page, apart from inverse
// text, which a menu marks its choice with. Every other escape is dropped, so nothing a program printed
// reaches the page as anything but text.

export type Run = { text: string; className: string };

type Tone = "" | "muted" | "marker" | "refuse";

type Style = { bold: boolean; dim: boolean; italic: boolean; underline: boolean; inverse: boolean; tone: Tone };

const plain: Style = { bold: false, dim: false, italic: false, underline: false, inverse: false, tone: "" };

// The eight basic colours, in their SGR order: black, red, green, yellow, blue, magenta, cyan, white.
const basic: Tone[] = ["muted", "refuse", "", "", "marker", "marker", "marker", ""];

// The levels of the 256-colour cube.
const cube = [0, 95, 135, 175, 215, 255];

// A CSI sequence, an OSC string, a DCS, SOS, PM, or APC string, or a two-character escape.
const escapes = /\x1b(?:\[([0-?]*)[ -/]*([@-~])|\][^\x07\x1b]*(?:\x07|\x1b\\)?|[PX^_][^\x1b]*(?:\x1b\\)?|[ -/]*[0-~])/g;

// Control characters other than a tab and a new line, which a screen holds as text.
const controls = /[\x00-\x08\x0b-\x1f\x7f-\x9f]/g;

function toneOfRGB(r: number, g: number, b: number): Tone {
  const high = Math.max(r, g, b);
  const low = Math.min(r, g, b);
  const spread = high - low;
  if (spread < 48) {
    return high > 64 && high < 200 ? "muted" : "";
  }
  let hue = high === r ? ((g - b) / spread) % 6 : high === g ? (b - r) / spread + 2 : (r - g) / spread + 4;
  hue = (hue * 60 + 360) % 360;
  if (hue < 15 || hue >= 330) {
    return "refuse";
  }
  return hue >= 170 ? "marker" : "";
}

function toneOf256(n: number): Tone {
  if (n < 16) {
    return basic[n % 8] ?? "";
  }
  if (n >= 232) {
    const level = 8 + 10 * (n - 232);
    return toneOfRGB(level, level, level);
  }
  const at = n - 16;
  return toneOfRGB(cube[Math.floor(at / 36)] ?? 0, cube[Math.floor(at / 6) % 6] ?? 0, cube[at % 6] ?? 0);
}

// extended reads a colour given as 5;n or 2;r;g;b after a 38, and how many parameters it took.
function extended(rest: number[]): [Tone, number] {
  if (rest[0] === 5) {
    return [toneOf256(rest[1] ?? 0), 2];
  }
  if (rest[0] === 2) {
    return [toneOfRGB(rest[1] ?? 0, rest[2] ?? 0, rest[3] ?? 0), 4];
  }
  return ["", 0];
}

// colonColour reads a colour given in one parameter, as 38:5:n or 38:2:[space]:r:g:b.
function colonColour(parts: number[]): Tone {
  if (parts[1] === 5) {
    return toneOf256(parts[2] ?? 0);
  }
  if (parts[1] === 2) {
    const [r, g, b] = parts.length >= 6 ? parts.slice(3, 6) : parts.slice(2, 5);
    return toneOfRGB(r ?? 0, g ?? 0, b ?? 0);
  }
  return "";
}

function applySGR(style: Style, text: string): Style {
  const next = { ...style };
  const params = text.split(";");
  for (let at = 0; at < params.length; at++) {
    const param = params[at] ?? "";
    if (param.includes(":")) {
      const parts = param.split(":").map((part) => Number(part || 0));
      if (parts[0] === 38) {
        next.tone = colonColour(parts);
      } else if (parts[0] === 4) {
        next.underline = parts[1] !== 0;
      }
      continue;
    }
    const code = Number(param || 0);
    if (code === 38 || code === 48) {
      const [tone, took] = extended(params.slice(at + 1).map((part) => Number(part || 0)));
      if (code === 38) {
        next.tone = tone;
      }
      at += took;
    } else if (code === 0) {
      Object.assign(next, plain);
    } else if (code === 1) {
      next.bold = true;
    } else if (code === 2) {
      next.dim = true;
    } else if (code === 3) {
      next.italic = true;
    } else if (code === 4) {
      next.underline = true;
    } else if (code === 7) {
      next.inverse = true;
    } else if (code === 22) {
      next.bold = false;
      next.dim = false;
    } else if (code === 23) {
      next.italic = false;
    } else if (code === 24) {
      next.underline = false;
    } else if (code === 27) {
      next.inverse = false;
    } else if (code >= 30 && code <= 37) {
      next.tone = basic[code - 30] ?? "";
    } else if (code >= 90 && code <= 97) {
      next.tone = basic[code - 90] ?? "";
    } else if (code === 39) {
      next.tone = "";
    }
  }
  return next;
}

function classOf(style: Style): string {
  const names: string[] = [];
  if (style.bold) {
    names.push("sgr-bold");
  }
  if (style.italic) {
    names.push("sgr-italic");
  }
  if (style.underline) {
    names.push("sgr-underline");
  }
  if (style.inverse) {
    names.push("sgr-inverse");
  } else {
    const tone = style.tone || (style.dim ? "muted" : "");
    if (tone) {
      names.push(`sgr-${tone}`);
    }
  }
  return names.join(" ");
}

// trimScreen drops the blank rows a terminal keeps below its last line for what comes next, so a pane is
// as tall as what it shows. A row of nothing but spaces and escapes is blank.
export function trimScreen(screen: string): string {
  const rows = screen.split("\n");
  const blank = (row: string) => row.replace(escapes, "").replace(controls, "").trim() === "";
  while (rows.length > 0 && blank(rows[rows.length - 1] ?? "")) {
    rows.pop();
  }
  return rows.join("\n");
}

// parseScreen splits a screen into runs of text that share one style.
export function parseScreen(screen: string): Run[] {
  const runs: Run[] = [];
  let style = plain;
  const add = (raw: string) => {
    const text = raw.replace(controls, "");
    if (!text) {
      return;
    }
    const className = classOf(style);
    const last = runs[runs.length - 1];
    if (last && last.className === className) {
      last.text += text;
    } else {
      runs.push({ text, className });
    }
  };
  let from = 0;
  for (const found of screen.matchAll(escapes)) {
    add(screen.slice(from, found.index));
    from = found.index + found[0].length;
    if (found[2] === "m" && found[1] !== undefined && !/[<=>?]/.test(found[1])) {
      style = applySGR(style, found[1]);
    }
  }
  add(screen.slice(from));
  return runs;
}
