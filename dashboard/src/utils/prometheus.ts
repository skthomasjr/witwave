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
const SAMPLE_RE = /^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})?\s+(-?[0-9.+eEinfa]+)/;
const LABEL_RE = /(\w+)="([^"]*)"/g;

function family(name: string): string {
  for (const s of SUFFIXES) if (name.endsWith(s)) return name.slice(0, -s.length);
  return name;
}

function parseLabels(raw: string | undefined): Record<string, string> {
  if (!raw) return {};
  const out: Record<string, string> = {};
  let m: RegExpExecArray | null;
  LABEL_RE.lastIndex = 0;
  while ((m = LABEL_RE.exec(raw)) !== null) out[m[1]] = m[2];
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
      const m = SAMPLE_RE.exec(t);
      if (!m) continue;
      const name = m[1];
      const value = Number(m[3]);
      if (!Number.isFinite(value)) continue;
      ensure(family(name)).samples.push({
        name,
        labels: parseLabels(m[2]?.slice(1, -1)),
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

// Merge many per-agent FamilyMaps into one. Label breakdowns naturally sum;
// gauges use the last seen value (callers treat gauges as cluster-total-ish).
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
