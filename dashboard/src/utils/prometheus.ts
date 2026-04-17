// Minimal Prometheus text-format parser. Just enough to pull out named
// series + their labels + numeric values for the metrics view. Doesn't try
// to be fully spec-compliant — exemplars, summaries, and quantile-heavy
// histograms are ignored. When we need more we reach for prom-client on
// the frontend (~30 kB); today's scope is single-sample snapshots.

export interface Sample {
  name: string;
  labels: Record<string, string>;
  value: number;
}

const SAMPLE_RE = /^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{([^}]*)\})?\s+(-?[0-9eE.+\-nNaAiIfFyY]+)/;
const LABEL_RE = /(\w+)="((?:[^"\\]|\\.)*)"/g;

function parseLabels(raw: string | undefined): Record<string, string> {
  if (!raw) return {};
  const out: Record<string, string> = {};
  let m: RegExpExecArray | null;
  LABEL_RE.lastIndex = 0;
  while ((m = LABEL_RE.exec(raw)) !== null) {
    out[m[1]] = m[2].replace(/\\"/g, '"').replace(/\\\\/g, "\\").replace(/\\n/g, "\n");
  }
  return out;
}

export function parseProm(text: string): Sample[] {
  const samples: Sample[] = [];
  for (const rawLine of text.split("\n")) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const m = SAMPLE_RE.exec(line);
    if (!m) continue;
    const value = Number(m[4]);
    if (!Number.isFinite(value)) continue;
    samples.push({
      name: m[1],
      labels: parseLabels(m[3]),
      value,
    });
  }
  return samples;
}

export function sumSamples(samples: Sample[], name: string): number {
  return samples
    .filter((s) => s.name === name)
    .reduce((acc, s) => acc + s.value, 0);
}

export function firstSample(samples: Sample[], name: string): Sample | null {
  return samples.find((s) => s.name === name) ?? null;
}
