// Compares hostslot's version with the plugin's: the plugin and the binary
// share release tags, and an older binary may lack what the plugin reads.

type Parsed = { core: [number, number, number]; pre: boolean };

function parse(v: string): Parsed | undefined {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(-[^+\s]*)?/.exec(v.trim());
  if (!m) return undefined;
  return { core: [Number(m[1]), Number(m[2]), Number(m[3])], pre: Boolean(m[4]) };
}

/**
 * Whether `binary` is older than `plugin`. A Go pseudo-version such as
 * v0.4.1-0.20261106-abc (a build after v0.4.0) counts as newer than 0.4.0.
 * Unparsable versions (development builds) are never older.
 */
export function older(binary: string, plugin: string): boolean {
  const b = parse(binary);
  const p = parse(plugin);
  if (!b || !p) return false;
  for (const [x, y] of b.core.map((n, i) => [n, p.core[i] ?? 0] as const)) {
    if (x !== y) return x < y;
  }
  return b.pre && !p.pre;
}
