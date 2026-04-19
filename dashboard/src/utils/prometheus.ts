// Prometheus text-format parser matching the legacy ui/ behavior — groups
// samples by metric *family* (strips suffixes like _total / _count / _sum /
// _bucket) so counters and histograms can be queried cleanly.

export interface Sample {
  name: string;
  labels: Record<string, string>;
  value: number;
}

export interface Family {
  help: string;
  type: string;
  samples: Sample[];
}

export type FamilyMap = Map<string, Family>;

const SUFFIXES = ["_bucket", "_count", "_sum", "_total", "_created", "_info"];
// Metric name matcher; the `{...}` block is extracted manually so that
// label values containing `}` or escaped `\"` parse correctly (#1009).
const METRIC_NAME_RE = /^([a-zA-Z_:][a-zA-Z0-9_:]*)/;
const LABEL_NAME_RE = /[a-zA-Z_][a-zA-Z0-9_]*/y;

function family(name: string): string {
  for (const s of SUFFIXES) if (name.endsWith(s)) return name.slice(0, -s.length);
  return name;
}

// Tokenise a sample line. Returns { name, labelsBlock, valueToken, rest }
// where labelsBlock is the raw content between the matched `{` and `}`
// (without the braces). Walks the `{...}` region character-by-character so
// `}` inside a label value cannot terminate the block early. Returns null
// on malformed input.
interface SampleTokens {
  name: string;
  labelsBlock: string | null;
  valueStart: number;
}

function tokenizeSampleLine(line: string): SampleTokens | null {
  const nameMatch = METRIC_NAME_RE.exec(line);
  if (!nameMatch) return null;
  const name = nameMatch[1];
  let i = name.length;
  let labelsBlock: string | null = null;
  if (line[i] === "{") {
    i += 1;
    const start = i;
    let inString = false;
    while (i < line.length) {
      const ch = line[i];
      if (inString) {
        if (ch === "\\") {
          // Skip the escaped character so \" inside a value does not
          // prematurely close the string.
          i += 2;
          continue;
        }
        if (ch === '"') {
          inString = false;
          i += 1;
          continue;
        }
        i += 1;
        continue;
      }
      if (ch === '"') {
        inString = true;
        i += 1;
        continue;
      }
      if (ch === "}") {
        labelsBlock = line.slice(start, i);
        i += 1;
        break;
      }
      i += 1;
    }
    if (labelsBlock === null) return null; // unterminated {...}
  }
  // Skip whitespace between the name/labels and the value token.
  while (i < line.length && (line[i] === " " || line[i] === "\t")) i += 1;
  if (i >= line.length) return null;
  return { name, labelsBlock, valueStart: i };
}

function parseLabels(raw: string | undefined): Record<string, string> {
  if (!raw) return {};
  const out: Record<string, string> = {};
  let i = 0;
  while (i < raw.length) {
    // Skip whitespace and separating commas.
    while (i < raw.length && (raw[i] === "," || raw[i] === " " || raw[i] === "\t")) {
      i += 1;
    }
    if (i >= raw.length) break;
    LABEL_NAME_RE.lastIndex = i;
    const nm = LABEL_NAME_RE.exec(raw);
    if (!nm || nm.index !== i) return out;
    const key = nm[0];
    i += key.length;
    if (raw[i] !== "=") return out;
    i += 1;
    if (raw[i] !== '"') return out;
    i += 1;
    // Scan quoted string with `\\`-escape awareness.
    let valStart = i;
    let value = "";
    let buffered = false;
    while (i < raw.length) {
      const ch = raw[i];
      if (ch === "\\" && i + 1 < raw.length) {
        if (!buffered) {
          value = raw.slice(valStart, i);
          buffered = true;
        }
        const nxt = raw[i + 1];
        if (nxt === "\\") value += "\\";
        else if (nxt === '"') value += '"';
        else if (nxt === "n") value += "\n";
        else value += nxt;
        i += 2;
        continue;
      }
      if (ch === '"') {
        if (!buffered) value = raw.slice(valStart, i);
        i += 1;
        break;
      }
      if (buffered) value += ch;
      i += 1;
    }
    // When no escape was seen we can take the raw slice verbatim; when an
    // escape was seen the accumulated `value` already holds the unescaped
    // string, so just use it.
    out[key] = value;
  }
  return out;
}

export function parseProm(text: string): FamilyMap {
  const out: FamilyMap = new Map();
  const ensure = (name: string): Family => {
    let f = out.get(name);
    if (!f) {
      f = { help: "", type: "", samples: [] };
      out.set(name, f);
    }
    return f;
  };

  for (const rawLine of text.split("\n")) {
    const t = rawLine.trim();
    if (!t) continue;
    if (t.startsWith("# HELP ")) {
      const m = /^# HELP (\S+) (.*)$/.exec(t);
      if (m) {
        const f = ensure(family(m[1]));
        if (!f.help) f.help = m[2];
      }
    } else if (t.startsWith("# TYPE ")) {
      const m = /^# TYPE (\S+) (\S+)$/.exec(t);
      if (m) {
        const f = ensure(family(m[1]));
        f.type = m[2];
      }
    } else if (!t.startsWith("#")) {
      const tokens = tokenizeSampleLine(t);
      if (!tokens) continue;
      // Consume the numeric value token up to the next whitespace so
      // trailing exemplars (histograms) or timestamps don't bleed in.
      let end = tokens.valueStart;
      while (end < t.length && t[end] !== " " && t[end] !== "\t") end += 1;
      const valueToken = t.slice(tokens.valueStart, end);
      const value = Number(valueToken);
      if (!Number.isFinite(value)) continue;
      ensure(family(tokens.name)).samples.push({
        name: tokens.name,
        labels: parseLabels(tokens.labelsBlock ?? undefined),
        value,
      });
    }
  }
  return out;
}

// Legacy helpers — same semantics as ui/index.html's renderMetrics section.

export function scalarGauge(m: FamilyMap, key: string): number | null {
  const s = m.get(key)?.samples;
  return s && s.length ? s[0].value : null;
}

// Sum gauge samples across every agent in the merged family map. Use this
// for additive cluster-wide gauges (e.g. harness_active_sessions) where each
// agent contributes its own value and the total is their sum.
export function sumGauge(m: FamilyMap, key: string): number | null {
  const s = m.get(key)?.samples;
  if (!s || !s.length) return null;
  return s.reduce((a, x) => a + x.value, 0);
}

// Max gauge sample across every agent. Use this for non-additive gauges
// like harness_uptime_seconds, where summing has no physical meaning but the
// cluster-wide longest-running agent is informative.
export function maxGauge(m: FamilyMap, key: string): number | null {
  const s = m.get(key)?.samples;
  if (!s || !s.length) return null;
  return s.reduce((a, x) => (x.value > a ? x.value : a), -Infinity);
}

export function sumTotal(m: FamilyMap, key: string): number {
  return (m.get(key)?.samples ?? [])
    .filter((s) => s.name.endsWith("_total"))
    .reduce((a, s) => a + s.value, 0);
}

export function histAvg(m: FamilyMap, key: string): number | null {
  const samples = m.get(key)?.samples ?? [];
  const sum = samples.filter((s) => s.name.endsWith("_sum")).reduce((a, s) => a + s.value, 0);
  const cnt = samples.filter((s) => s.name.endsWith("_count")).reduce((a, s) => a + s.value, 0);
  return cnt > 0 ? sum / cnt : null;
}

export function breakdownByLabel(
  m: FamilyMap,
  key: string,
  labelName: string,
  onlySuffix: string | null = "_total",
): Record<string, number> {
  const out: Record<string, number> = {};
  for (const s of m.get(key)?.samples ?? []) {
    if (onlySuffix && !s.name.endsWith(onlySuffix)) continue;
    const lv = s.labels[labelName] ?? "(none)";
    out[lv] = (out[lv] ?? 0) + s.value;
  }
  return out;
}

// Merge many per-agent FamilyMaps into one. Counter/histogram breakdowns
// naturally sum via sumTotal/breakdownByLabel. Gauges retain every agent's
// sample — callers must pick an aggregation policy explicitly via
// sumGauge (additive) or maxGauge (non-additive). scalarGauge only returns
// the first sample and should not be used on the merged map for
// cluster-wide stats.
export function mergeFamilies(maps: FamilyMap[]): FamilyMap {
  const out: FamilyMap = new Map();
  for (const m of maps) {
    for (const [k, v] of m) {
      const existing = out.get(k);
      if (!existing) {
        out.set(k, { help: v.help, type: v.type, samples: [...v.samples] });
      } else {
        existing.samples.push(...v.samples);
        if (!existing.help) existing.help = v.help;
        if (!existing.type) existing.type = v.type;
      }
    }
  }
  return out;
}
